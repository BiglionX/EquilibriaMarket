package prebid

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"market-engine/pkg/engine"
	"market-engine/pkg/governance"
	"market-engine/pkg/openrtb"
	_ "market-engine/pkg/governance/centralized"
)

// newTestAdapter 构造一个测试用 adapter（含 mock bidder 和 mock 治理插件）。
func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()

	// 构造治理插件（中心化模式，100% 通胀便于测试）
	plugin, err := governance.Load("centralized", map[string]any{
		"admin_address":  "0xTest",
		"inflation_rate": 1.0,
	})
	if err != nil {
		t.Fatalf("Load plugin failed: %v", err)
	}

	mm := engine.NewMarketMaker(
		engine.AuctioneerFunc(engine.SecondPriceAuction),
		plugin,
		engine.WithTaxRate(0.1),
		engine.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	// 注册内容-创作者映射
	mm.RegisterContent("creative-merchant-premium-imp-banner-home-1", "creator-premium")
	mm.RegisterContent("creative-merchant-standard-imp-banner-home-1", "creator-standard")

	return NewAdapter(mm,
		WithBidders(
			NewMockBidder("merchant-premium", 12.0),
			NewMockBidder("merchant-standard", 7.0),
			NewMockBidder("creator-livestream", 4.5),
		),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

// ===== Adapter 转换测试 =====

func TestAdapter_HandleOpenRTB_HappyPath(t *testing.T) {
	a := newTestAdapter(t)

	req := BuildSampleRequest()
	resp, err := a.HandleOpenRTB(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != req.ID {
		t.Errorf("response ID = %s, want %s", resp.ID, req.ID)
	}
	if len(resp.SeatBid) != 1 {
		t.Fatalf("expected 1 SeatBid, got %d", len(resp.SeatBid))
	}

	seatBid := resp.SeatBid[0]
	if len(seatBid.Bid) != 1 {
		t.Fatalf("expected 1 Bid in SeatBid, got %d", len(seatBid.Bid))
	}
	if seatBid.Seat != SeatName {
		t.Errorf("seat = %s, want %s", seatBid.Seat, SeatName)
	}

	bid := seatBid.Bid[0]
	// merchant-premium 出价 12，其他 7 和 4.5
	// clearPrice = 7 + 0.01 = 7.01
	if bid.Price < 7.0 || bid.Price > 12.0 {
		t.Errorf("price = %f, want in [7, 12]", bid.Price)
	}
	if bid.ImpID != "imp-banner-home-1" {
		t.Errorf("ImpID = %s, want imp-banner-home-1", bid.ImpID)
	}
	if bid.CRID == "" {
		t.Error("CRID should not be empty")
	}
	if bid.AdM == "" {
		t.Error("AdM should not be empty")
	}
}

func TestAdapter_HandleOpenRTB_NoBids(t *testing.T) {
	// 所有 bidder 都低于底价时返回 NoBid
	plugin, _ := governance.Load("centralized", nil)
	mm := engine.NewMarketMaker(
		engine.AuctioneerFunc(engine.SecondPriceAuction),
		plugin,
	)
	a := NewAdapter(mm,
		WithBidders(
			NewMockBidder("low-bidder", 0.1), // 远低于底价 1.0
		),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	req := BuildSampleRequest()
	resp, err := a.HandleOpenRTB(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.NoBid == 0 {
		t.Error("expected NoBid to be set when no bids")
	}
	if len(resp.SeatBid) != 0 {
		t.Errorf("expected 0 SeatBids, got %d", len(resp.SeatBid))
	}
}

func TestAdapter_HandleOpenRTB_MultiImps(t *testing.T) {
	a := newTestAdapter(t)

	req := &openrtb.BidRequest{
		ID: "req-multi",
		Imp: []openrtb.Imp{
			{ID: "imp-1", Banner: &openrtb.Banner{W: 300, H: 250}, BidFloor: 1.0},
			{ID: "imp-2", Banner: &openrtb.Banner{W: 728, H: 90}, BidFloor: 1.0},
			{ID: "imp-3", Banner: &openrtb.Banner{W: 160, H: 600}, BidFloor: 1.0},
		},
	}

	resp, err := a.HandleOpenRTB(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.SeatBid) != 3 {
		t.Errorf("expected 3 SeatBids (one per imp), got %d", len(resp.SeatBid))
	}
}

func TestAdapter_ValidationErrors(t *testing.T) {
	a := newTestAdapter(t)

	t.Run("nil request", func(t *testing.T) {
		_, err := a.HandleOpenRTB(context.Background(), nil)
		if err == nil {
			t.Error("expected error for nil request")
		}
	})

	t.Run("no impressions", func(t *testing.T) {
		_, err := a.HandleOpenRTB(context.Background(), &openrtb.BidRequest{ID: "x"})
		if err == nil {
			t.Error("expected error for empty imps")
		}
	})
}

// ===== Bidder 转换测试 =====

func TestMockBidder_BidFloorRespected(t *testing.T) {
	b := NewMockBidder("test", 1.0)

	// 底价 2.0 > 出价 1.0，应该放弃
	imp := &openrtb.Imp{ID: "i1", BidFloor: 2.0}
	_, ok := b.Bid(context.Background(), nil, imp)
	if ok {
		t.Error("expected bidder to skip when bid < floor")
	}

	// 底价 0.5 < 出价 1.0，应该出价
	imp.BidFloor = 0.5
	bid, ok := b.Bid(context.Background(), nil, imp)
	if !ok {
		t.Error("expected bidder to bid when bid > floor")
	}
	if bid.Price != 1.0 {
		t.Errorf("price = %f, want 1.0", bid.Price)
	}
}

func TestMockBidder_WithNoise(t *testing.T) {
	b := NewMockBidderWithNoise("noisy", 10.0, 2.0)

	minPrice, maxPrice := 100.0, 0.0
	imp := &openrtb.Imp{ID: "i1", BidFloor: 0, Banner: &openrtb.Banner{W: 728, H: 90}}

	for i := 0; i < 100; i++ {
		bid, ok := b.Bid(context.Background(), nil, imp)
		if !ok {
			t.Fatal("expected bid")
		}
		if bid.Price < minPrice {
			minPrice = bid.Price
		}
		if bid.Price > maxPrice {
			maxPrice = bid.Price
		}
	}

	if minPrice < 7.9 || maxPrice > 12.1 {
		t.Errorf("price range [%f, %f] out of expected [8, 12]", minPrice, maxPrice)
	}
}

func TestMockBidder_Disable(t *testing.T) {
	b := NewMockBidder("test", 5.0)
	b.Disable()
	_, ok := b.Bid(context.Background(), nil, &openrtb.Imp{ID: "i1", BidFloor: 0})
	if ok {
		t.Error("disabled bidder should not bid")
	}
	b.Enable()
	_, ok = b.Bid(context.Background(), nil, &openrtb.Imp{ID: "i1", BidFloor: 0})
	if !ok {
		t.Error("enabled bidder should bid")
	}
}

// ===== 转换函数单测 =====

func TestExtractUserTags(t *testing.T) {
	req := BuildSampleRequest()
	imp := &req.Imp[0]
	tags := extractUserTags(req, imp)

	expectedTags := map[string]bool{
		"user_id":      true,
		"gender":       true,
		"country":      true,
		"region":       true,
		"device_os":    true,
		"device_type":  true,
		"site_domain":  true,
		"tag_id":       true,
		"seg_tech_enthusiast": true,
	}
	for k := range expectedTags {
		if _, ok := tags[k]; !ok {
			t.Errorf("expected tag %q in result, got: %v", k, tags)
		}
	}
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello world", "hello_world"},
		{"Tech-Enthusiast", "tech-enthusiast"},
		{"AI/ML", "ai_ml"},
		{"plain", "plain"},
		{"  spaces  ", "spaces"},
	}
	for _, tt := range tests {
		got := sanitizeKey(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ===== Mock Server HTTP 测试 =====

func TestMockPrebidServer_AuctionEndpoint(t *testing.T) {
	a := newTestAdapter(t)
	server := NewMockPrebidServer(a, "127.0.0.1:0")
	// 用 httptest 包装避免端口冲突
	ts := httptest.NewServer(server.httpServer.Handler)
	defer ts.Close()

	req := BuildSampleRequest()
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", ts.URL+"/openrtb2/auction", strings.NewReader(string(body)))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(bodyBytes))
	}

	var bidResp openrtb.BidResponse
	if err := json.NewDecoder(resp.Body).Decode(&bidResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if bidResp.ID != req.ID {
		t.Errorf("response ID = %s, want %s", bidResp.ID, req.ID)
	}
	if len(bidResp.SeatBid) != 1 {
		t.Errorf("expected 1 SeatBid, got %d", len(bidResp.SeatBid))
	}
}

func TestMockPrebidServer_Status(t *testing.T) {
	a := newTestAdapter(t)
	server := NewMockPrebidServer(a, "127.0.0.1:0")
	ts := httptest.NewServer(server.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// ===== 完整字段映射测试 =====

func TestAdapter_BidFieldsMapping(t *testing.T) {
	a := newTestAdapter(t)

	// 注册所有内容
	a.market.RegisterContent("creative-merchant-premium-imp-1", "creator-premium")

	req := &openrtb.BidRequest{
		ID: "req-mapping",
		Imp: []openrtb.Imp{
			{
				ID:     "imp-1",
				Banner: &openrtb.Banner{W: 300, H: 250},
				TagID:  "test-tag",
			},
		},
		User: &openrtb.User{
			ID:       "user-123",
			Geo:      &openrtb.Geo{Country: "US"},
		},
	}

	resp, err := a.HandleOpenRTB(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.SeatBid) == 0 || len(resp.SeatBid[0].Bid) == 0 {
		t.Fatal("no bid returned")
	}

	bid := resp.SeatBid[0].Bid[0]

	if bid.W != 300 || bid.H != 250 {
		t.Errorf("banner size = (%d, %d), want (300, 250)", bid.W, bid.H)
	}
	if bid.ImpID != "imp-1" {
		t.Errorf("ImpID = %s, want imp-1", bid.ImpID)
	}
	if bid.CRID == "" {
		t.Error("CRID should map from ContentID")
	}
	if bid.CID == "" {
		t.Error("CID should map from BidderID")
	}
}

// ===== 性能测试：批量处理多个 OpenRTB 请求 =====

func BenchmarkAdapter_HandleOpenRTB(b *testing.B) {
	a := newTestAdapterHelper()
	req := BuildSampleRequest()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = a.HandleOpenRTB(ctx, req)
	}
}

// newTestAdapterHelper 是 newTestAdapter 的非 testing.T 版本（用于 bench）。
func newTestAdapterHelper() *Adapter {
	plugin, _ := governance.Load("centralized", map[string]any{
		"admin_address":  "0xTest",
		"inflation_rate": 1.0,
	})
	mm := engine.NewMarketMaker(
		engine.AuctioneerFunc(engine.SecondPriceAuction),
		plugin,
		engine.WithTaxRate(0.1),
		engine.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	mm.RegisterContent("creative-merchant-premium-imp-banner-home-1", "creator-premium")
	return NewAdapter(mm,
		WithBidders(
			NewMockBidder("merchant-premium", 12.0),
			NewMockBidder("merchant-standard", 7.0),
		),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}