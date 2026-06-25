package engine

import (
	"context"
	"errors"
	"testing"

	"market-engine/pkg/types"
)

// TestSecondPriceAuction 使用 table-driven 模式覆盖 SecondPriceAuction 的关键场景。
//
// 用例覆盖：
//   - 正常情况：三人不同出价，最高者按"第二高价+0.01"成交。
//   - 两人出价相同：稳定排序保证第一个出价者获胜（与现实一致）。
//   - 只有一人出价：按底价 0.01 成交。
//   - 边界：无人出价、全部无效出价 → 返回错误。
//   - 经济性质：诚实报价占优（报 10 不会比报 8 多付）。
func TestSecondPriceAuction(t *testing.T) {
	tests := []struct {
		name       string
		bids       []types.BidResponse
		wantErr    error
		wantWinner string
		wantPrice  float64
	}{
		{
			name: "正常情况/三人不同出价",
			bids: []types.BidResponse{
				{BidderID: "A", Bid: 10.0, ContentID: "ad-A"},
				{BidderID: "B", Bid: 8.0, ContentID: "ad-B"},
				{BidderID: "C", Bid: 5.0, ContentID: "ad-C"},
			},
			wantWinner: "A",
			wantPrice:  8.01,
		},
		{
			name: "两人出价相同/稳定排序",
			bids: []types.BidResponse{
				{BidderID: "A", Bid: 10.0, ContentID: "ad-A"},
				{BidderID: "B", Bid: 10.0, ContentID: "ad-B"},
			},
			wantWinner: "A", // 稳定排序：先到者胜
			wantPrice:  10.01,
		},
		{
			name: "只有一人出价/底价成交",
			bids: []types.BidResponse{
				{BidderID: "A", Bid: 100.0, ContentID: "ad-A"},
			},
			wantWinner: "A",
			wantPrice:  0.01,
		},
		{
			name: "四人出价/验证维克瑞规则",
			bids: []types.BidResponse{
				{BidderID: "A", Bid: 100.0, ContentID: "ad-A"},
				{BidderID: "B", Bid: 80.0, ContentID: "ad-B"},
				{BidderID: "C", Bid: 60.0, ContentID: "ad-C"},
				{BidderID: "D", Bid: 40.0, ContentID: "ad-D"},
			},
			wantWinner: "A",
			wantPrice:  80.01,
		},
		{
			name:    "无人出价",
			bids:    []types.BidResponse{},
			wantErr: ErrNoBids,
		},
		{
			name: "全部无效出价/零或负数",
			bids: []types.BidResponse{
				{BidderID: "A", Bid: 0, ContentID: "ad-A"},
				{BidderID: "B", Bid: -5.0, ContentID: "ad-B"},
			},
			wantErr: ErrNoBids,
		},
		{
			name: "混合有效与无效出价",
			bids: []types.BidResponse{
				{BidderID: "A", Bid: 0, ContentID: "ad-A"},     // 无效
				{BidderID: "B", Bid: 10.0, ContentID: "ad-B"},  // 有效
				{BidderID: "C", Bid: 5.0, ContentID: "ad-C"},   // 有效
			},
			wantWinner: "B",
			wantPrice:  5.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := types.BidRequest{SlotID: "slot-test-1"}
			got, err := SecondPriceAuction(context.Background(), req, tt.bids)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.WinnerID != tt.wantWinner {
				t.Errorf("WinnerID = %s, want %s", got.WinnerID, tt.wantWinner)
			}
			// 浮点比较使用 epsilon，避免二进制浮点误差
			const epsilon = 1e-9
			if abs(got.ClearPrice-tt.wantPrice) > epsilon {
				t.Errorf("ClearPrice = %f, want %f", got.ClearPrice, tt.wantPrice)
			}
			if got.SlotID != req.SlotID {
				t.Errorf("SlotID = %s, want %s", got.SlotID, req.SlotID)
			}
		})
	}
}

// TestVickreyDominantStrategy 验证维克瑞拍卖的核心经济学性质：
//
// 在第二价格密封拍卖中，每个理性参与者的占优策略是"诚实报价"。
// 即：报出自己的真实估值不会比任何其他策略更差。
//
// 此测试通过对比"诚实报价"和"虚假报价"的期望收益来演示这一性质。
func TestVickreyDominantStrategy(t *testing.T) {
	// 场景：你的真实估值是 10。其他出价方报 6。
	yourTrueValue := 10.0
	competitorBid := 6.0
	competitor := types.BidResponse{BidderID: "Rival", Bid: competitorBid, ContentID: "rival"}

	// 关键经济学性质：只要报价不低于第二高价，支付价就等于"第二高价+0.01"，
	// 与你报多少无关——这正是"诚实报价是占优策略"的根源。
	tests := []struct {
		name        string
		yourBid     float64
		wantWin     bool
		wantPayment float64 // 期望的清算价（与胜出方无关）
	}{
		{name: "诚实报价10/获胜支付6.01", yourBid: 10.0, wantWin: true, wantPayment: 6.01},
		{name: "刚好压过对手报7/获胜支付6.01", yourBid: 7.0, wantWin: true, wantPayment: 6.01},
		{name: "超额报价15/获胜支付不变", yourBid: 15.0, wantWin: true, wantPayment: 6.01},
		{name: "低估报价5/输给竞争对手", yourBid: 5.0, wantWin: false, wantPayment: 5.01}, // 对手赢，按你的报价5 + 0.01 清算
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			you := types.BidResponse{BidderID: "You", Bid: tt.yourBid, ContentID: "you"}
			req := types.BidRequest{SlotID: "slot-truthful"}

			got, err := SecondPriceAuction(context.Background(), req, []types.BidResponse{you, competitor})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			won := got.WinnerID == "You"
			if won != tt.wantWin {
				t.Errorf("win = %v, want %v", won, tt.wantWin)
			}
			if got.ClearPrice != tt.wantPayment {
				t.Errorf("payment = %f, want %f", got.ClearPrice, tt.wantPayment)
			}

			// 经济学性质：你的"盈余"= 真实估值 - 支付价
			if won {
				surplus := yourTrueValue - got.ClearPrice
				if surplus < 0 {
					t.Errorf("报诚实价应保证非负盈余，实际盈余=%.4f", surplus)
				}
			}
		})
	}
}

// abs 辅助函数：浮点绝对值
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// BenchmarkSecondPriceAuction 基准测试：衡量维克瑞拍卖的性能。
// Phase 1 目标：P99 < 50ms、QPS > 10,000。
func BenchmarkSecondPriceAuction(b *testing.B) {
	req := types.BidRequest{SlotID: "slot-bench"}
	bids := make([]types.BidResponse, 20)
	for i := range bids {
		bids[i] = types.BidResponse{
			BidderID:  string(rune('A' + i)),
			Bid:       float64(100 - i),
			ContentID: "ad-" + string(rune('A'+i)),
		}
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SecondPriceAuction(ctx, req, bids)
	}
}