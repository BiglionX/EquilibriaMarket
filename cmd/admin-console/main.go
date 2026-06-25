// admin-console 是 Equilibria 的最小可行前端控制台。
//
// 目标：
//   - 提供 HTTP API 让管理员发货币、看指标。
//   - 在浏览器中实时显示基尼系数（Chart.js 柱状图）。
//
// 设计原则：
//   - 与生产 marketd 分离：避免单点故障。
//   - 直接引用 centralized.Plugin 实例（通过 type assertion 获得 Snapshot 能力）。
//   - 使用 embed.FS 把 web 目录直接打包进二进制，部署只需要 1 个文件。
//   - 使用 Gin（per 任务要求）。
//
// 启动：
//
//	GIN_MODE=release ./admin-console -addr :8080 -plugin centralized
//
// 访问：浏览器打开 http://localhost:8080/
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"market-engine/pkg/governance"
	"market-engine/pkg/governance/centralized"
	"market-engine/pkg/metrics"
)

//go:embed static
var staticFS embed.FS

//go:embed index.html
var indexHTML []byte

// Snapshotter 是治理插件的可选接口（centralized.Plugin 已实现）。
//
// 通过 type assertion 探测能力，避免修改核心 Plugin 接口。
type Snapshotter interface {
	Snapshot() map[string]int64
}

// Server 是 admin-console 的应用主体。
type Server struct {
	plugin governance.Plugin
	logger *slog.Logger
}

// ===== API Handlers =====

// GET /api/health 健康检查。
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "equilibria-admin-console",
		"plugin":  s.plugin.Name(),
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

// POST /api/mint 发行货币。
func (s *Server) handleMint(c *gin.Context) {
	var req governance.MintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"accepted": false,
			"message":  "invalid json: " + err.Error(),
		})
		return
	}
	if req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"accepted": false,
			"message":  "user_id is required",
		})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"accepted": false,
			"message":  "amount must be positive",
		})
		return
	}
	if req.Reason == "" {
		req.Reason = "admin-console grant"
	}
	if req.Timestamp == 0 {
		req.Timestamp = time.Now().Unix()
	}

	resp, err := s.plugin.MintCurrency(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"accepted": false,
			"message":  err.Error(),
		})
		return
	}
	status := http.StatusOK
	if !resp.Accepted {
		status = http.StatusBadRequest
	}
	s.logger.Info("mint via console",
		"user_id", req.UserID, "amount", req.Amount,
		"accepted", resp.Accepted, "new_balance", resp.NewBalance,
	)
	c.JSON(status, resp)
}

// GET /api/metrics 返回基尼系数 + 总览指标。
func (s *Server) handleMetrics(c *gin.Context) {
	snap, err := s.snapshot()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	report := metrics.Compute(snap)
	c.JSON(http.StatusOK, report)
}

// GET /api/users 返回 Top 持有者（默认 10）。
func (s *Server) handleUsers(c *gin.Context) {
	snap, err := s.snapshot()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	topK := 10
	if v := c.Query("limit"); v != "" {
		fmt.Sscanf(v, "%d", &topK)
	}
	tops := metrics.TopHolders(snap, topK)
	c.JSON(http.StatusOK, gin.H{
		"count": len(tops),
		"users": tops,
		"as_of": time.Now().UTC().Format(time.RFC3339),
	})
}

// snapshot 从治理插件拉取余额快照。
func (s *Server) snapshot() (metrics.Snapshot, error) {
	snap, ok := s.plugin.(Snapshotter)
	if !ok {
		return metrics.Snapshot{}, fmt.Errorf("plugin %q does not implement Snapshotter", s.plugin.Name())
	}
	return metrics.Snapshot{
		Balances:  snap.Snapshot(),
		Timestamp: time.Now(),
	}, nil
}

// ===== 路由构建 =====

func (s *Server) buildEngine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.requestLogger())

	// API
	api := r.Group("/api")
	{
		api.GET("/health", s.handleHealth)
		api.GET("/metrics", s.handleMetrics)
		api.GET("/users", s.handleUsers)
		api.POST("/mint", s.handleMint)
	}

	// 静态文件：css / js
	staticSubFS, _ := fs.Sub(staticFS, "static")
	r.StaticFS("/static", http.FS(staticSubFS))

	// 根路径：返回嵌入的 index.html
	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	return r
}

// requestLogger 简洁的请求日志中间件。
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		s.logger.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"elapsed_ms", time.Since(start).Milliseconds(),
		)
	}
}

// ===== 启动 =====

func main() {
	var (
		addr     = flag.String("addr", ":8080", "HTTP listen address")
		pluginID = flag.String("plugin", "centralized", "governance plugin name")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 加载治理插件
	plugin, err := governance.Load(*pluginID, nil)
	if err != nil {
		logger.Error("failed to load plugin", "name", *pluginID, "err", err)
		os.Exit(1)
	}
	logger.Info("plugin loaded", "name", plugin.Name())

	srv := &Server{plugin: plugin, logger: logger}

	// 启动时塞种子数据（仅 centralized 插件能直接调私有方法）
	if cp, ok := plugin.(*centralized.Plugin); ok {
		seedDemoData(context.Background(), cp, logger)
	} else {
		logger.Warn("plugin is not *centralized.Plugin, skipping seed data")
	}

	// 启动 HTTP 服务
	ginEngine := srv.buildEngine()
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           ginEngine,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("admin-console starting", "addr", *addr, "url", "http://localhost"+*addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	// 优雅关闭
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down…")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	logger.Info("goodbye")
}

// seedDemoData 启动时塞一些演示账户，让界面有数据可看。
//
// 用通胀率（默认 0.05）放大原始量：
//   - 实际到账 = amount * 0.05
//   - 想要 alice 到账 1000 → amount = 20000
func seedDemoData(ctx context.Context, p *centralized.Plugin, logger *slog.Logger) {
	type seed struct {
		user   string
		amount int64 // 请求量（实际到账 = amount * 0.05）
	}
	seeds := []seed{
		{"alice", 20000},  // 到账 1000
		{"bob", 10000},     // 到账 500
		{"carol", 6000},    // 到账 300
		{"dave", 2000},     // 到账 100
		{"eve", 1000},      // 到账 50
		{"frank", 1000},    // 到账 50
		{"grace", 1000},    // 到账 50
	}
	for _, s := range seeds {
		// 只在用户没余额时种数据（避免重启后覆盖）
		if p.GetBalance(s.user) == 0 && s.amount > 0 {
			resp, err := p.MintCurrency(ctx, governance.MintRequest{
				UserID: s.user,
				Amount: s.amount,
				Reason: "seed",
			})
			if err != nil {
				logger.Warn("seed mint failed", "user", s.user, "err", err)
				continue
			}
			logger.Info("seeded user", "user", s.user, "balance", resp.NewBalance)
		}
	}
}
