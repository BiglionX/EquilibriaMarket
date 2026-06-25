// Package engine 实现核心调度引擎。
//
// 本文件定义拍卖器接口与维克瑞拍卖（第二价格密封拍卖）的标准实现。
//
// 设计要点：
//   - Auctioneer 是接口，便于 Phase 2 替换为第一价格、加价拍卖等多种机制。
//   - SecondPriceAuction 是纯函数：无副作用、易测试、天然并发安全。
//   - 维克瑞拍卖的核心经济学性质：诚实报价是占优策略（Dominant Strategy）。
package engine

import (
	"context"
	"errors"
	"sort"

	"market-engine/pkg/types"
)

// ErrNoBids 没有有效出价时返回。
var ErrNoBids = errors.New("no bids provided")

// ErrInvalidBid 出价非正数时返回（防止恶意低价或负价攻击）。
var ErrInvalidBid = errors.New("invalid bid amount")

// Auctioneer 拍卖器接口。
//
// 任何实现该接口的类型都可以被引擎用于处理流量位的"价格发现"。
// Phase 1 默认实现：SecondPriceAuction（维克瑞拍卖）。
// 未来可扩展：第一价格拍卖、加价拍卖（Ascending）、组合拍卖等。
type Auctioneer interface {
	// RunAuction 在给定的流量位请求上对一组出价进行清算。
	// 返回胜出者与成交价格。
	RunAuction(ctx context.Context, req types.BidRequest, bids []types.BidResponse) (*types.AuctionResult, error)
}

// SecondPriceAuction 实现维克瑞拍卖（第二价格密封拍卖）。
//
// 规则：
//   - 出价最高者获胜。
//   - 胜出者支付"第二高价 + 0.01"（增量 bid increment 为 0.01）。
//   - 若仅一人出价，按底价 0.01 成交（保护胜出者、避免独占低价）。
//
// 经济学性质：诚实报价是占优策略，因为胜出者只需支付第二高的报价，
// 不会因为报低价而吃亏，也不会因为报高价而多付 —— 这是 Jordan 教授
// 强调的"激励机制设计"的经典案例。
func SecondPriceAuction(ctx context.Context, req types.BidRequest, bids []types.BidResponse) (*types.AuctionResult, error) {
	if len(bids) == 0 {
		return nil, ErrNoBids
	}

	// 输入校验：过滤非正数出价
	validBids := make([]types.BidResponse, 0, len(bids))
	for _, b := range bids {
		if b.Bid <= 0 {
			continue
		}
		validBids = append(validBids, b)
	}
	if len(validBids) == 0 {
		return nil, ErrNoBids
	}

	// 复制并按出价降序排序（稳定排序保留同价时的输入顺序）
	sorted := make([]types.BidResponse, len(validBids))
	copy(sorted, validBids)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Bid > sorted[j].Bid
	})

	winner := sorted[0]

	// 计算成交价
	var clearPrice float64
	if len(sorted) == 1 {
		// 单人出价：底价成交（保护新进入者、避免报价过低损害市场）
		clearPrice = 0.01
	} else {
		// 第二高价 + 0.01（最小加价单位，保证价格离散性）
		clearPrice = sorted[1].Bid + 0.01
	}

	return &types.AuctionResult{
		WinnerID:   winner.BidderID,
		ContentID:  winner.ContentID,
		ClearPrice: clearPrice,
		SlotID:     req.SlotID,
	}, nil
}

// Compile-time 接口契约检查：确保 SecondPriceAuction 满足 Auctioneer 接口。
//
// 如果未来有人改动接口签名，此行会在编译期报错。
var _ Auctioneer = AuctioneerFunc(SecondPriceAuction)

// AuctioneerFunc 适配器，允许普通函数实现 Auctioneer 接口。
type AuctioneerFunc func(ctx context.Context, req types.BidRequest, bids []types.BidResponse) (*types.AuctionResult, error)

// RunAuction 实现 Auctioneer 接口。
func (f AuctioneerFunc) RunAuction(ctx context.Context, req types.BidRequest, bids []types.BidResponse) (*types.AuctionResult, error) {
	return f(ctx, req, bids)
}