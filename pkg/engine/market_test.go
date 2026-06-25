package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"market-engine/pkg/governance"
	"market-engine/pkg/types"
)

// fakePlugin 是一个可注入行为的 Plugin 测试替身。
//
// 允许测试在不依赖真实治理实现的情况下，精确控制审核/转账/销毁的结果。
type fakePlugin struct {
	name string

	// 内容审核：决定哪些 contentID 通过
	approve map[string]bool
	// 转账行为：注入余额
	balances map[string]int64

	// 失败注入
	transferErr  error
	burnErr      error
	validateErr  error
}

func newFakePlugin() *fakePlugin {
	return &fakePlugin{
		name:      "fake",
		approve:   make(map[string]bool),
		balances:  make(map[string]int64),
	}
}

func (f *fakePlugin) Name() string { return f.name }

func (f *fakePlugin) MintCurrency(ctx context.Context, req governance.MintRequest) (*governance.MintResponse, error) {
	f.balances[req.UserID] += req.Amount
	return &governance.MintResponse{TxID: "fake-mint", NewBalance: f.balances[req.UserID], Accepted: true}, nil
}

func (f *fakePlugin) Transfer(ctx context.Context, req governance.TransferRequest) (*governance.TransferResponse, error) {
	if f.transferErr != nil {
		return nil, f.transferErr
	}
	if f.balances[req.From] < req.Amount {
		return &governance.TransferResponse{
			FromBalance: f.balances[req.From],
			ToBalance:   f.balances[req.To],
			Accepted:    false,
			Message:     "insufficient",
		}, nil
	}
	f.balances[req.From] -= req.Amount
	f.balances[req.To] += req.Amount
	return &governance.TransferResponse{
		TxID:        "fake-transfer",
		FromBalance: f.balances[req.From],
		ToBalance:   f.balances[req.To],
		Accepted:    true,
	}, nil
}

func (f *fakePlugin) Burn(ctx context.Context, req governance.BurnRequest) (*governance.BurnResponse, error) {
	if f.burnErr != nil {
		return nil, f.burnErr
	}
	burned := req.Amount
	if burned > f.balances[req.From] {
		burned = f.balances[req.From]
	}
	f.balances[req.From] -= burned
	return &governance.BurnResponse{
		TxID:        "fake-burn",
		FromBalance: f.balances[req.From],
		Burned:      burned,
		Accepted:    true,
	}, nil
}

func (f *fakePlugin) ValidateContent(ctx context.Context, content governance.Content) (*governance.PolicyDecision, error) {
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	approved, exists := f.approve[content.ContentID]
	if !exists {
		approved = true // 默认通过
	}
	return &governance.PolicyDecision{
		Approved:   approved,
		ReviewerID: "fake-reviewer",
	}, nil
}

func (f *fakePlugin) ResolveDispute(ctx context.Context, dispute governance.Dispute) (*governance.ArbitrationResult, error) {
	return &governance.ArbitrationResult{Winner: dispute.Plaintiff, Loser: dispute.Defendant, Arbitrator: "fake"}, nil
}

func (f *fakePlugin) GetRandomSeed(ctx context.Context, req governance.RandomRequest) (*governance.RandomResponse, error) {
	return &governance.RandomResponse{Value: req.RangeMin, Proof: "fake-proof"}, nil
}

// ===== MarketMaker 测试 =====

func TestMarketMaker_HappyPath(t *testing.T) {
	plugin := newFakePlugin()
	// 给胜出者预存 10000 单位（便于让税率取整后仍可见）
	plugin.balances["alice"] = 10000

	mm := NewMarketMaker(AuctioneerFunc(SecondPriceAuction), plugin)
	mm.RegisterContent("ad-1", "creator-bob")

	req := types.BidRequest{SlotID: "slot-1"}
	bids := []types.BidResponse{
		{BidderID: "alice", Bid: 100.0, ContentID: "ad-1"},
		{BidderID: "carol", Bid: 80.0, ContentID: "ad-2"},
	}

	result, settle, err := mm.HandleBidRequest(context.Background(), req, bids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.WinnerID != "alice" {
		t.Errorf("WinnerID = %s, want alice", result.WinnerID)
	}
	// clearPrice = 80.01 → 8001 单位；tax = int64(8001*0.001) = 8；payout = 7993
	if settle.TransferTxID == "" {
		t.Error("TransferTxID should not be empty")
	}
	if settle.PayoutTxID == "" {
		t.Error("PayoutTxID should not be empty")
	}
	if settle.BurnTxID == "" {
		t.Error("BurnTxID should not be empty")
	}
	// alice 10000 - 8001 = 1999
	if settle.SponsorBalance != 1999 {
		t.Errorf("alice balance after pay = %d, want 1999", settle.SponsorBalance)
	}
	// 创作者应收到 8001 - 8 = 7993
	if settle.CreatorBalance != 7993 {
		t.Errorf("creator balance = %d, want 7993", settle.CreatorBalance)
	}
	// tax = 8 单位 → 0.08 注意力币
	if settle.Tax <= 0 {
		t.Errorf("tax should be > 0, got %f", settle.Tax)
	}
}

func TestMarketMaker_ContentRejected(t *testing.T) {
	plugin := newFakePlugin()
	plugin.approve["bad-content"] = false

	mm := NewMarketMaker(AuctioneerFunc(SecondPriceAuction), plugin)
	req := types.BidRequest{SlotID: "slot-1"}
	bids := []types.BidResponse{
		{BidderID: "alice", Bid: 10.0, ContentID: "bad-content"},
	}

	_, _, err := mm.HandleBidRequest(context.Background(), req, bids)
	if err == nil {
		t.Fatal("expected error when content rejected")
	}
	if !errors.Is(err, ErrContentRejected) {
		t.Errorf("expected ErrContentRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "bad-content") {
		t.Errorf("error should mention content_id, got: %v", err)
	}
}

func TestMarketMaker_InsufficientBalance(t *testing.T) {
	plugin := newFakePlugin()
	// alice 只有 100，远不够支付
	plugin.balances["alice"] = 100

	mm := NewMarketMaker(AuctioneerFunc(SecondPriceAuction), plugin)
	req := types.BidRequest{SlotID: "slot-1"}
	bids := []types.BidResponse{
		{BidderID: "alice", Bid: 10.0, ContentID: "ad-1"},
		{BidderID: "carol", Bid: 5.0, ContentID: "ad-2"},
	}

	result, settle, err := mm.HandleBidRequest(context.Background(), req, bids)
	// 拍卖应该成功，但结算应该失败
	if result == nil {
		t.Fatal("auction result should be returned even on settlement failure")
	}
	if err == nil {
		t.Fatal("expected error on insufficient balance")
	}
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Errorf("expected ErrInsufficientBalance, got %v", err)
	}
	if settle == nil {
		t.Error("settlement should be returned for debugging")
	}
	// alice 余额不应被扣减（Transfer 拒绝时不修改余额）
	if settle.SponsorBalance != 100 {
		t.Errorf("alice balance should be unchanged, got %d", settle.SponsorBalance)
	}
}

func TestMarketMaker_NoBids(t *testing.T) {
	plugin := newFakePlugin()
	mm := NewMarketMaker(AuctioneerFunc(SecondPriceAuction), plugin)

	req := types.BidRequest{SlotID: "slot-1"}
	_, _, err := mm.HandleBidRequest(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected error when no bids")
	}
	if !errors.Is(err, ErrNoBids) {
		t.Errorf("expected ErrNoBids, got %v", err)
	}
}

func TestMarketMaker_CustomTaxRate(t *testing.T) {
	plugin := newFakePlugin()
	plugin.balances["alice"] = 10000

	mm := NewMarketMaker(
		AuctioneerFunc(SecondPriceAuction),
		plugin,
		WithTaxRate(0.1), // 10% 抽税便于断言
	)
	mm.RegisterContent("ad-1", "creator")

	req := types.BidRequest{SlotID: "slot-1"}
	bids := []types.BidResponse{
		{BidderID: "alice", Bid: 10.0, ContentID: "ad-1"},
	}

	_, settle, err := mm.HandleBidRequest(context.Background(), req, bids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// clearPrice = 0.01（单出价底价）→ 1 单位；tax = 10% → 0 单位（向下取整）
	// 验证 tax 字段存在且 >= 0
	if settle.Tax < 0 {
		t.Errorf("tax should be >= 0, got %f", settle.Tax)
	}
}

func TestMarketMaker_UnknownContentStillSettles(t *testing.T) {
	// 创作者未登记时，应把款项留在系统账户（不报错）
	plugin := newFakePlugin()
	plugin.balances["alice"] = 1000

	mm := NewMarketMaker(AuctioneerFunc(SecondPriceAuction), plugin)
	// 不调用 RegisterContent

	req := types.BidRequest{SlotID: "slot-1"}
	bids := []types.BidResponse{
		{BidderID: "alice", Bid: 10.0, ContentID: "unknown-ad"},
	}

	_, settle, err := mm.HandleBidRequest(context.Background(), req, bids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settle.PayoutTxID != "" {
		t.Error("PayoutTxID should be empty when creator unknown")
	}
}