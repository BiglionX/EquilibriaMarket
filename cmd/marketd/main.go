// marketd 是 EquilibriaMarket 引擎的 HTTP 服务入口。
//
// 端点设计（Phase 1 MVP）：
//   POST /v1/bid           提交一次竞价请求，返回拍卖+结算结果
//   POST /v1/mint          货币发行（开发/调试用）
//   GET  /healthz          健康检查
//   GET  /v1/governance    列出已加载的治理插件
//   GET  /v1/balance/{uid} 查询用户余额（仅用于开发调试）
//
// 设计原则：
//   - 使用 Go 1.22+ 标准库 net/http 的增强路由（ServeMux 支持方法和路径参数）。
//   - 无第三方依赖：MVP 阶段先把核心跑通，再按需引入框架。
//   - 错误格式统一为 { "error": "..." }。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"market-engine/pkg/engine"
	"market-engine/pkg/governance"
	// 触发 centralized 包的 init()，将插件注册到全局注册表。
	_ "market-engine/pkg/governance/centralized"
	"market-engine/pkg/types"
)

func main() {
	var (
		addr           = flag.String("addr", ":8080", "HTTP 服务监听地址")
		governanceName = flag.String("governance", "centralized", "治理插件名称")
		adminAddr      = flag.String("admin", "0xAdmin0000000000000000000000000000000000", "管理员地址")
		inflationRate  = flag.Float64("inflation", 0.05, "货币通胀率")
		taxRate        = flag.Float64("tax", 0.001, "平台抽税比例")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// 加载治理插件
	plugin, err := governance.Load(*governanceName, map[string]any{
		"admin_address":  *adminAddr,
		"inflation_rate": *inflationRate,
		"tax_rate":       *taxRate,
	})
	if err != nil {
		logger.Error("failed to load governance plugin", "name", *governanceName, "err", err)
		os.Exit(1)
	}
	logger.Info("governance plugin loaded", "name", plugin.Name())

	// 构造做市商
	mm := engine.NewMarketMaker(
		engine.AuctioneerFunc(engine.SecondPriceAuction),
		plugin,
		engine.WithTaxRate(*taxRate),
		engine.WithLogger(logger),
	)
	logger.Info("market maker ready",
		"governance", plugin.Name(),
		"tax_rate", mm.EffectiveTaxRate(),
	)

	// 构造 HTTP 处理器
	srv := &server{
		marketMaker: mm,
		plugin:      plugin,
		logger:      logger,
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// 优雅启停
	idleClosed := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			logger.Error("shutdown error", "err", err)
		}
		close(idleClosed)
	}()

	logger.Info("marketd starting", "addr", *addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
	<-idleClosed
	logger.Info("marketd stopped")
}

// server 持有 HTTP 处理所需的依赖。
type server struct {
	marketMaker *engine.MarketMaker
	plugin      governance.Plugin
	logger      *slog.Logger
}

// routes 注册所有路由（Go 1.22+ 增强 ServeMux）。
//
// 返回 http.Handler 以便包装中间件。
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	// 业务接口
	mux.HandleFunc("POST /v1/bid", s.handleBid)
	mux.HandleFunc("POST /v1/mint", s.handleMint)

	// 运维接口
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/governance", s.handleGovernance)
	mux.HandleFunc("GET /v1/balance/{userID}", s.handleBalance)

	// 中间件：访问日志
	return loggingMiddleware(s.logger, mux)
}

// ===== 请求/响应数据结构 =====

// bidRequest HTTP 层的竞价请求。
//
// 包含原始 BidRequest 和一组 BidResponse，方便业务方一次提交完整拍卖。
type bidRequest struct {
	SlotID    string             `json:"slot_id"`
	UserTags  map[string]string  `json:"user_tags,omitempty"`
	FloorPrice float64            `json:"floor_price"`
	Bids      []types.BidResponse `json:"bids"`
}

// bidResponse HTTP 层的竞价响应。
type bidResponse struct {
	AuctionResult *types.AuctionResult `json:"auction_result"`
	Settlement    *engine.Settlement   `json:"settlement,omitempty"`
	Error         string               `json:"error,omitempty"`
}

// mintRequest HTTP 层的货币发行请求。
type mintRequest struct {
	UserID string `json:"user_id"`
	Amount int64  `json:"amount"`
	Reason string `json:"reason"`
}

// ===== 处理器 =====

// handleBid 处理竞价请求：审核 → 拍卖 → 结算。
func (s *server) handleBid(w http.ResponseWriter, r *http.Request) {
	var req bidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SlotID == "" {
		writeError(w, http.StatusBadRequest, "slot_id is required")
		return
	}
	if len(req.Bids) == 0 {
		writeError(w, http.StatusBadRequest, "bids must not be empty")
		return
	}

	// 转换为内部类型并附带时间戳
	bidReq := types.BidRequest{
		SlotID:     req.SlotID,
		UserTags:   req.UserTags,
		FloorPrice: req.FloorPrice,
		Timestamp:  time.Now(),
	}

	result, settle, err := s.marketMaker.HandleBidRequest(r.Context(), bidReq, req.Bids)
	if err != nil {
		// 即便有错误，拍卖结果（如果有）也返回给客户端，便于排查
		status := http.StatusInternalServerError
		if errors.Is(err, engine.ErrContentRejected) || errors.Is(err, engine.ErrInsufficientBalance) {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, bidResponse{
			AuctionResult: result,
			Settlement:    settle,
			Error:         err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, bidResponse{
		AuctionResult: result,
		Settlement:    settle,
	})
}

// handleMint 货币发行（开发/调试用，生产应由治理层独立接口提供）。
func (s *server) handleMint(w http.ResponseWriter, r *http.Request) {
	var req mintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}

	resp, err := s.plugin.MintCurrency(r.Context(), governance.MintRequest{
		UserID:    req.UserID,
		Amount:    req.Amount,
		Reason:    req.Reason,
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !resp.Accepted {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"accepted": false,
			"message":  resp.Message,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted":    true,
		"tx_id":       resp.TxID,
		"new_balance": resp.NewBalance,
		"message":     resp.Message,
	})
}

// handleHealth 健康检查。
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// handleGovernance 列出已加载的治理插件。
func (s *server) handleGovernance(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"active":   s.plugin.Name(),
		"registry": governance.List(),
	})
}

// handleBalance 查询用户余额（仅开发/调试；生产应由独立的钱包服务提供）。
func (s *server) handleBalance(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userID")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "userID is required")
		return
	}

	// 通过治理插件的 MintResponse.NewBalance 查询余额（中心化模式下间接方式）。
	// 实际生产应提供专门的 GetBalance 接口。
	resp, err := s.plugin.MintCurrency(r.Context(), governance.MintRequest{
		UserID:    userID,
		Amount:    0, // 查询用，Amount=0 会被拒绝
		Reason:    "balance_query",
		Timestamp: time.Now().Unix(),
	})
	balance := int64(0)
	balanceKnown := false
	if err == nil && resp.NewBalance > 0 {
		balance = resp.NewBalance
		balanceKnown = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":       userID,
		"balance":       balance,
		"balance_known": balanceKnown,
		"hint":          "use POST /v1/mint to add funds; this endpoint is for dev only",
	})
}

// ===== 工具函数 =====

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// loggingMiddleware 简单的访问日志中间件。
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"remote", clientIP(r),
		)
	})
}

// statusRecorder 包装 ResponseWriter 以捕获状态码。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// clientIP 提取客户端 IP（X-Forwarded-For 优先）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return host
}