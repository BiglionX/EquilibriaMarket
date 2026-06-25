// Package prebid 实现 EquilibriaMarket 与 Prebid Server 之间的 adapter。
//
// 角色定位：
//   - 让外部 Prebid Server（或任何 OpenRTB 兼容的 SSP/Exchange）能够调用我们的引擎。
//   - 我们的引擎在 OpenRTB 世界里扮演一个 "seat"（出价方）。
//
// 工作流程：
//  1. Prebid Server 发送 OpenRTB BidRequest。
//  2. Adapter 把 OpenRTB 请求转换为引擎的 BidRequest + BidResponse[]。
//  3. 调用 MarketMaker.HandleBidRequest（审核 → 拍卖 → 结算）。
//  4. 把 AuctionResult 映射回 OpenRTB SeatBid（Bid[]）。
//
// 关键设计：
//   - 每个 imp 独立拍卖 → 可能产生多个 SeatBid（Phase 1 简化为合并成一个）。
//   - Bidder 列表通过配置注入；未配置时使用 mock bidder（开发模式）。
//   - OpenRTB at=2 映射为维克瑞拍卖，at=1 映射为第一价拍卖（Phase 2）。
package prebid

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"market-engine/pkg/engine"
	"market-engine/pkg/openrtb"
	"market-engine/pkg/types"
)

const (
	// AdapterName adapter 在 Prebid 中的注册名（用于日志/监控）。
	AdapterName = "equilibria"

	// SeatName 我们作为出价方的标识。
	SeatName = "equilibria"
)

// Bidder 抽象一个"出价方"（商家/达人），负责根据 imp 生成报价。
//
// 在真实 Prebid 中，每个 bidder 是一个远程 HTTP 服务（如 Rubicon、AppNexus）。
// 这里我们抽象为接口，让 adapter 可以挂载本地或远程 bidder。
type Bidder interface {
	// Name 返回 bidder 标识（用于 SeatBid.seat）。
	Name() string

	// Bid 根据 OpenRTB imp 生成一个出价。
	// 返回 (Bid, true) 表示有出价；(nil, false) 表示放弃。
	Bid(ctx context.Context, req *openrtb.BidRequest, imp *openrtb.Imp) (*Bid, bool)
}

// Bid adapter 层的出价（介于 OpenRTB Bid 和引擎 BidResponse 之间）。
//
// 用本地结构让 Bidder 接口不直接依赖引擎类型，降低耦合。
type Bid struct {
	BidderID  string  // 出价方 ID
	Price     float64 // 出价金额
	ContentID string  // 待展示的广告/内容 ID
	AdM       string  // 广告素材（HTML/VAST）
	Cat       []string
	ADomain   []string
	W, H      int
}

// Adapter 把 OpenRTB 流量转换为引擎请求，再把引擎结果转换为 OpenRTB 响应。
type Adapter struct {
	market  *engine.MarketMaker
	bidders []Bidder
	logger  *slog.Logger

	// 拍卖类型映射：openrtb.at → 引擎拍卖器
	// Phase 1 仅支持 at=2（维克瑞）。
}

// AdapterOption 适配器配置选项。
type AdapterOption func(*Adapter)

// WithBidders 注册一组 bidder。
func WithBidders(bidders ...Bidder) AdapterOption {
	return func(a *Adapter) {
		a.bidders = append(a.bidders, bidders...)
	}
}

// WithLogger 自定义日志器。
func WithLogger(logger *slog.Logger) AdapterOption {
	return func(a *Adapter) {
		if logger != nil {
			a.logger = logger
		}
	}
}

// NewAdapter 构造 Prebid adapter。
func NewAdapter(market *engine.MarketMaker, opts ...AdapterOption) *Adapter {
	a := &Adapter{
		market:  market,
		bidders: nil,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(a)
	}

	// 如果没有配置 bidder，注册默认 mock bidder（开发/演示模式）
	if len(a.bidders) == 0 {
		a.bidders = append(a.bidders,
			NewMockBidder("merchant-premium", 12.0),
			NewMockBidder("merchant-standard", 7.0),
			NewMockBidder("creator-livestream", 4.5),
		)
	}

	return a
}

// HandleOpenRTB 处理一个 OpenRTB 请求，返回 OpenRTB 响应。
//
// 这是 adapter 的主入口，可被 Prebid Server 通过 HTTP/gRPC 调用。
func (a *Adapter) HandleOpenRTB(ctx context.Context, req *openrtb.BidRequest) (*openrtb.BidResponse, error) {
	if req == nil {
		return nil, errors.New("nil bid request")
	}
	if len(req.Imp) == 0 {
		return nil, errors.New("bid request has no impressions")
	}

	start := time.Now()
	defer func() {
		a.logger.Info("openrtb handled",
			"request_id", req.ID,
			"imps", len(req.Imp),
			"elapsed_ms", time.Since(start).Milliseconds(),
		)
	}()

	resp := &openrtb.BidResponse{
		ID:  req.ID,
		Cur: "USD", // OpenRTB 默认货币；Phase 2 接入注意力币转换
	}

	seatBids := make([]openrtb.SeatBid, 0, len(req.Imp))

	for i := range req.Imp {
		imp := &req.Imp[i]
		seatBid, err := a.handleImp(ctx, req, imp)
		if err != nil {
			a.logger.Warn("imp handling failed",
				"imp_id", imp.ID,
				"err", err,
			)
			continue
		}
		if seatBid != nil && len(seatBid.Bid) > 0 {
			seatBids = append(seatBids, *seatBid)
		}
	}

	if len(seatBids) == 0 {
		// OpenRTB 规范：NoBid=1 表示"无出价"
		resp.NoBid = 1
		return resp, nil
	}

	resp.SeatBid = seatBids
	return resp, nil
}

// handleImp 处理单个 imp：收集所有 bidder 报价 → 引擎拍卖 → 映射回 OpenRTB。
func (a *Adapter) handleImp(ctx context.Context, req *openrtb.BidRequest, imp *openrtb.Imp) (*openrtb.SeatBid, error) {
	// 1. 收集所有 bidder 报价
	internalBids := make([]types.BidResponse, 0, len(a.bidders))
	bidsByID := make(map[string]*Bid, len(a.bidders)) // bidderID → 原始 Bid（用于映射响应）

	for _, bidder := range a.bidders {
		bid, ok := bidder.Bid(ctx, req, imp)
		if !ok || bid == nil {
			continue
		}
		internalBids = append(internalBids, types.BidResponse{
			BidderID:  bid.BidderID,
			Bid:       bid.Price,
			ContentID: bid.ContentID,
		})
		bidsByID[bid.BidderID] = bid
	}

	if len(internalBids) == 0 {
		return nil, nil // 无出价
	}

	// 2. OpenRTB imp → 引擎 BidRequest
	internalReq := a.toInternalBidRequest(req, imp)

	// 3. 调用引擎
	result, settle, err := a.market.HandleBidRequest(ctx, internalReq, internalBids)
	if err != nil {
		// 即便有错误，result 也可能不为空（部分流程失败）
		if result == nil {
			return nil, fmt.Errorf("market maker failed: %w", err)
		}
		a.logger.Warn("settlement failed but auction succeeded",
			"imp_id", imp.ID,
			"err", err,
		)
	}

	// 4. AuctionResult → OpenRTB Bid
	openRTBBid := a.toOpenRTBBid(result, bidsByID, imp, settle)
	if openRTBBid == nil {
		return nil, nil
	}

	return &openrtb.SeatBid{
		Bid:   []openrtb.Bid{*openRTBBid},
		Seat:  SeatName,
		Group: 0,
	}, nil
}

// ===== 转换函数 =====

// toInternalBidRequest 把 OpenRTB BidRequest + 单个 Imp 转换为引擎 BidRequest。
//
// 提取字段：
//   - SlotID  ← imp.ID
//   - UserTags ← user.id/user.keywords/device.ua/device.geo 等
//   - FloorPrice ← imp.BidFloor
//   - Timestamp ← time.Now()
func (a *Adapter) toInternalBidRequest(req *openrtb.BidRequest, imp *openrtb.Imp) types.BidRequest {
	tags := extractUserTags(req, imp)
	return types.BidRequest{
		SlotID:     imp.ID,
		UserTags:   tags,
		FloorPrice: imp.BidFloor,
		Timestamp:  time.Now(),
	}
}

// extractUserTags 从 OpenRTB 请求提取用户画像标签（用于引擎匹配）。
//
// 标签格式：key=value;key=value
// 例如：device_type=mobile;os=ios;country=CN;interest=tech
func extractUserTags(req *openrtb.BidRequest, imp *openrtb.Imp) map[string]string {
	tags := make(map[string]string)

	if req.User != nil {
		if req.User.ID != "" {
			tags["user_id"] = req.User.ID
		}
		if req.User.Gender != "" {
			tags["gender"] = req.User.Gender
		}
		if req.User.Keywords != "" {
			tags["keywords"] = req.User.Keywords
		}
		if req.User.Geo != nil {
			if req.User.Geo.Country != "" {
				tags["country"] = req.User.Geo.Country
			}
			if req.User.Geo.Region != "" {
				tags["region"] = req.User.Geo.Region
			}
			if req.User.Geo.City != "" {
				tags["city"] = req.User.Geo.City
			}
		}
		// 数据段（来自第三方数据提供商）
		for _, d := range req.User.Data {
			for _, seg := range d.Segment {
				key := "seg_" + sanitizeKey(d.Name)
				tags[key] = sanitizeKey(seg.Name)
			}
		}
	}

	if req.Device != nil {
		if req.Device.OS != "" {
			tags["device_os"] = req.Device.OS
		}
		if req.Device.DeviceType > 0 {
			tags["device_type"] = fmt.Sprintf("%d", req.Device.DeviceType)
		}
		if req.Device.UA != "" {
			tags["ua"] = req.Device.UA
		}
	}

	if req.Site != nil {
		tags["site_domain"] = req.Site.Domain
		tags["site_page"] = req.Site.Page
	}
	if req.App != nil {
		tags["app_bundle"] = req.App.Bundle
	}
	if imp.TagID != "" {
		tags["tag_id"] = imp.TagID
	}

	return tags
}

// sanitizeKey 把字符串转换为标签安全的 key（去掉空格/特殊字符）。
func sanitizeKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// 替换非法字符
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			b = append(b, c)
		case c >= '0' && c <= '9':
			b = append(b, c)
		case c == '_' || c == '-':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}

// toOpenRTBBid 把引擎 AuctionResult 转换为 OpenRTB Bid。
//
// 映射关系：
//   - Bid.ID       ← 自动生成 UUID-like
//   - Bid.ImpID    ← imp.ID
//   - Bid.Price    ← result.ClearPrice
//   - Bid.AdM      ← 来自胜出 bidder 的素材
//   - Bid.CRID/CID ← ContentID / BidderID
func (a *Adapter) toOpenRTBBid(result *types.AuctionResult, bidsByID map[string]*Bid, imp *openrtb.Imp, settle *engine.Settlement) *openrtb.Bid {
	if result == nil {
		return nil
	}

	// 查找胜出 bidder 的原始出价（用于填充素材信息）
	originalBid := bidsByID[result.WinnerID]

	bid := &openrtb.Bid{
		ID:    result.SlotID + "-" + result.WinnerID, // Phase 2 替换为 UUID
		ImpID: result.SlotID,
		Price: result.ClearPrice,
		CRID:  result.ContentID,
		CID:   result.WinnerID,
	}

	if originalBid != nil {
		bid.AdM = originalBid.AdM
		bid.Cat = originalBid.Cat
		bid.ADomain = originalBid.ADomain
		bid.W = originalBid.W
		bid.H = originalBid.H
	}

	// Banner/Video 规格继承自 imp
	if imp.Banner != nil {
		if bid.W == 0 {
			bid.W = imp.Banner.W
		}
		if bid.H == 0 {
			bid.H = imp.Banner.H
		}
	}

	// NURL（胜出通知 URL）—— Phase 2 替换为真实结算回调
	if settle != nil && settle.TransferTxID != "" {
		bid.NURL = "/v1/notify?tx=" + settle.TransferTxID
	}

	return bid
}