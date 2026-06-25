package prebid

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"market-engine/pkg/openrtb"
)

// MockPrebidServer 模拟 Prebid Server：接收 HTTP OpenRTB 请求，调用 adapter，返回响应。
//
// 用途：
//   - 端到端测试：在没有真实 Prebid Server 时，验证 adapter 工作正常。
//   - 演示：可作为"如何接入 Prebid Server"的最小参考实现。
//   - 集成测试：供其他服务调用我们的引擎。
//
// 注意：真实的 Prebid Server 会做：
//   - 并行调用多个 bidder adapter
//   - 合并所有 bidder 的 bid
//   - 选择最优出价
//
// Mock 服务器省略这些步骤，直接把请求转发给我们的 Adapter（充当一个特殊 bidder）。
type MockPrebidServer struct {
	adapter    *Adapter
	httpServer *http.Server
	addr       string
}

// NewMockPrebidServer 构造 mock server（未启动）。
func NewMockPrebidServer(adapter *Adapter, addr string) *MockPrebidServer {
	s := &MockPrebidServer{
		adapter: adapter,
		addr:    addr,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /openrtb2/auction", s.handleAuction)
	mux.HandleFunc("GET /status", s.handleStatus)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	return s
}

// Start 启动 HTTP 服务（阻塞）。
func (s *MockPrebidServer) Start() error {
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// StartBackground 在后台启动 HTTP 服务。
func (s *MockPrebidServer) StartBackground() <-chan error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()
	return errCh
}

// Shutdown 优雅停止。
func (s *MockPrebidServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// handleAuction 处理 OpenRTB 拍卖请求（Prebid Server 风格的入口）。
func (s *MockPrebidServer) handleAuction(w http.ResponseWriter, r *http.Request) {
	var req openrtb.BidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid OpenRTB request: "+err.Error())
		return
	}

	resp, err := s.adapter.HandleOpenRTB(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleStatus 健康检查。
func (s *MockPrebidServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"adapter": AdapterName,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// BuildSampleRequest 构造一个示例 OpenRTB 请求（用于测试和演示）。
//
// 包含：1 个 banner imp、用户画像、设备信息、地理位置、IAB 分类。
func BuildSampleRequest() *openrtb.BidRequest {
	return &openrtb.BidRequest{
		ID: "req-demo-001",
		Imp: []openrtb.Imp{
			{
				ID:       "imp-banner-home-1",
				Banner:   &openrtb.Banner{W: 728, H: 90, Pos: 1, MIMEs: []string{"image/jpeg", "image/png"}},
				BidFloor: 1.0,
				TagID:    "homepage-top",
			},
		},
		Site: &openrtb.Site{
			ID:     "site-techblog",
			Name:   "TechBlog",
			Domain: "techblog.example.com",
			Page:   "https://techblog.example.com/articles/ai-economics",
			Publisher: &openrtb.Publisher{ID: "pub-techblog", Name: "TechBlog Inc."},
		},
		User: &openrtb.User{
			ID:       "user-uuid-abc123",
			Gender:   "M",
			Keywords: "ai, machine learning, economics",
			Geo:      &openrtb.Geo{Country: "CN", Region: "BJ", City: "Beijing"},
			Data: []openrtb.Data{
				{
					ID:   "data-tech-enthusiast",
					Name: "Tech Enthusiast",
					Segment: []openrtb.Segment{
						{ID: "seg-001", Name: "AI/ML"},
						{ID: "seg-002", Name: "Web3"},
					},
				},
			},
		},
		Device: &openrtb.Device{
			UA:         "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)",
			IP:         "203.0.113.42",
			DeviceType: 1, // 手机
			OS:         "iOS",
			OSV:        "17.0",
			Make:       "Apple",
			Model:      "iPhone15,2",
			W:          390,
			H:          844,
			Language:   "zh-CN",
		},
		TMax: 200,
		At:   2, // 第二价格密封拍卖
	}
}