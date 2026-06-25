package prebid

import (
	"context"
	"fmt"
	"math/rand"

	"market-engine/pkg/openrtb"
)

// MockBidder 一个简单的测试 bidder，按固定价格或随机价格出价。
//
// 用于演示和测试：在没有真实 bidder 接入时，提供可控的出价行为。
type MockBidder struct {
	name      string
	basePrice float64
	noise     float64 // 价格浮动范围（±）
	enabled   bool
}

// NewMockBidder 构造一个固定出价的 mock bidder。
func NewMockBidder(name string, price float64) *MockBidder {
	return &MockBidder{
		name:      name,
		basePrice: price,
		noise:     0,
		enabled:   true,
	}
}

// NewMockBidderWithNoise 构造一个带价格浮动的 mock bidder。
//
// 每次出价在 [basePrice - noise, basePrice + noise] 区间内随机。
func NewMockBidderWithNoise(name string, basePrice, noise float64) *MockBidder {
	return &MockBidder{
		name:      name,
		basePrice: basePrice,
		noise:     noise,
		enabled:   true,
	}
}

// Name 实现 Bidder 接口。
func (m *MockBidder) Name() string { return m.name }

// Bid 实现 Bidder 接口。
//
// 简单的出价逻辑：基础价 ± 浮动。
// 如果 imp 的底价高于出价，放弃（return false）。
func (m *MockBidder) Bid(ctx context.Context, req *openrtb.BidRequest, imp *openrtb.Imp) (*Bid, bool) {
	if !m.enabled {
		return nil, false
	}

	price := m.basePrice
	if m.noise > 0 {
		price += (rand.Float64()*2 - 1) * m.noise
	}

	// 低于底价则不出价（bidder 保护机制）
	if price < imp.BidFloor {
		return nil, false
	}

	// 默认 banner 素材
	w, h := 300, 250
	if imp.Banner != nil {
		w, h = imp.Banner.W, imp.Banner.H
	}

	return &Bid{
		BidderID:  m.name,
		Price:     price,
		ContentID: fmt.Sprintf("creative-%s-%s", m.name, imp.ID),
		AdM: fmt.Sprintf(
			`<div class="ad" data-bidder="%s" data-imp="%s">Ad by %s</div>`,
			m.name, imp.ID, m.name,
		),
		Cat:     []string{"IAB1"}, // IAB1 = Arts & Entertainment
		ADomain: []string{"example-" + m.name + ".com"},
		W:       w,
		H:       h,
	}, true
}

// Disable 关闭 bidder（用于 A/B 测试或多 bidder 管理）。
func (m *MockBidder) Disable() { m.enabled = false }

// Enable 启用 bidder。
func (m *MockBidder) Enable() { m.enabled = true }