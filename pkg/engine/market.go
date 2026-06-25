package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"market-engine/pkg/governance"
	"market-engine/pkg/types"
)

// 错误定义
var (
	// ErrContentRejected 内容审核未通过
	ErrContentRejected = errors.New("content rejected by governance")
	// ErrInsufficientBalance 胜出者余额不足
	ErrInsufficientBalance = errors.New("winner has insufficient balance")
	// ErrNoCreator 没有创作者信息（无法分账）
	ErrNoCreator = errors.New("content has no creator; cannot settle")
)

// SystemAccountID 系统账户 ID（在 Phase 1 中心化模式下作为中介账户）。
// 所有拍卖款项先进入系统账户，再按比例分账给创作者 / 抽税。
const SystemAccountID = "system"

// DefaultPlatformTaxRate 默认平台抽税比例（与 centralized.Config.TaxRate 一致）。
const DefaultPlatformTaxRate = 0.001

// Settlement 结算明细：记录一次拍卖清算的完整资金流向。
//
// 设计目的：让结算流程透明、可审计（PRD 中"可审计日志"的核心数据）。
type Settlement struct {
	ClearPrice     float64 `json:"clear_price"`     // 拍卖清算价（注意力币）
	Tax            float64 `json:"tax"`             // 系统抽税
	CreatorPayout  float64 `json:"creator_payout"`  // 创作者分账
	SponsorPaid    float64 `json:"sponsor_paid"`    // 胜出者实际支付
	SponsorBalance int64   `json:"sponsor_balance"` // 胜出者支付后余额
	CreatorBalance int64   `json:"creator_balance"` // 创作者收到后余额
	TransferTxID   string  `json:"transfer_tx_id"`  // 胜出者→系统账户的转账ID
	PayoutTxID     string  `json:"payout_tx_id"`    // 系统账户→创作者的转账ID
	BurnTxID       string  `json:"burn_tx_id"`      // 抽税销毁ID
}

// MarketMaker 做市商：组合 Auctioneer 和 Plugin，实现完整交易流程。
//
// 核心职责（来自 PRD）：
//   1. 内容审核（治理插件 ValidateContent）
//   2. 拍卖撮合（Auctioneer.RunAuction）
//   3. 资金结算（治理插件 Transfer + Burn：扣款 → 转移 → 抽税）
//
// 设计哲学：
//   - "做市商本身不以盈利为首要目的"——只抽取固定税率维持运转。
//   - "市场均衡而非中心规划"——所有规则通过 Plugin 注入，可被不同治理模式替换。
type MarketMaker struct {
	auctioneer Auctioneer
	plugin     governance.Plugin

	// 内容→创作者 的映射（Phase 1 内存版；Phase 2 应改为内容索引服务）。
	// 使用 sync.RWMutex 保护并发读写。
	mu              sync.RWMutex
	contentCreators map[string]string

	// 平台抽税比例（默认 0.001 = 0.1%）
	taxRate float64

	// 结构化日志
	logger *slog.Logger
}

// MarketMakerOption 配置选项（函数式选项模式）。
type MarketMakerOption func(*MarketMaker)

// WithTaxRate 自定义平台抽税比例。
func WithTaxRate(rate float64) MarketMakerOption {
	return func(m *MarketMaker) {
		if rate >= 0 && rate < 1 {
			m.taxRate = rate
		}
	}
}

// WithLogger 自定义日志器。
func WithLogger(logger *slog.Logger) MarketMakerOption {
	return func(m *MarketMaker) {
		if logger != nil {
			m.logger = logger
		}
	}
}

// WithContentCreator 预设内容-创作者映射（主要用于测试和种子数据）。
func WithContentCreator(contentID, creatorID string) MarketMakerOption {
	return func(m *MarketMaker) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.contentCreators[contentID] = creatorID
	}
}

// NewMarketMaker 构造一个做市商实例。
func NewMarketMaker(auctioneer Auctioneer, plugin governance.Plugin, opts ...MarketMakerOption) *MarketMaker {
	m := &MarketMaker{
		auctioneer:      auctioneer,
		plugin:          plugin,
		contentCreators: make(map[string]string),
		taxRate:         DefaultPlatformTaxRate,
		logger:          slog.Default(),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// RegisterContent 登记内容-创作者映射。
//
// 由业务服务在内容发布成功后调用，引擎据此在结算时分账。
func (m *MarketMaker) RegisterContent(contentID, creatorID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contentCreators[contentID] = creatorID
	m.logger.Info("content registered", "content_id", contentID, "creator_id", creatorID)
}

// EffectiveTaxRate 返回当前生效的抽税比例（仅用于调试/监控）。
func (m *MarketMaker) EffectiveTaxRate() float64 {
	return m.taxRate
}

// lookupCreator 查询内容的创作者（内部方法）。
func (m *MarketMaker) lookupCreator(contentID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	creator, ok := m.contentCreators[contentID]
	return creator, ok
}

// HandleBidRequest 处理一次竞价请求的完整生命周期。
//
// 流程（对应 PRD 中的"做市商"职责）：
//  1. 审核阶段：对每个 BidResponse 关联的 Content 调用 plugin.ValidateContent。
//  2. 撮合阶段：调用 auctioneer.RunAuction 得到胜出者和清算价。
//  3. 结算阶段：
//     a) 胜出者 → 系统账户（Transfer，扣款）
//     b) 系统账户 → 创作者（Transfer，分账）
//     c) 系统账户 → 销毁（Burn，抽税）
//
// 返回值：包含拍卖结果 + 结算明细，调用方可序列化用于审计日志。
func (m *MarketMaker) HandleBidRequest(ctx context.Context, req types.BidRequest, bids []types.BidResponse) (*types.AuctionResult, *Settlement, error) {
	start := time.Now()
	defer func() {
		m.logger.Info("bid request handled",
			"slot_id", req.SlotID,
			"bids", len(bids),
			"elapsed_ms", time.Since(start).Milliseconds(),
		)
	}()

	// ===== 阶段 1：内容审核 =====
	if err := m.validateBids(ctx, bids); err != nil {
		return nil, nil, err
	}

	// ===== 阶段 2：拍卖撮合 =====
	result, err := m.auctioneer.RunAuction(ctx, req, bids)
	if err != nil {
		return nil, nil, fmt.Errorf("auction failed: %w", err)
	}

	// ===== 阶段 3：资金结算 =====
	settle, err := m.settle(ctx, result)
	if err != nil {
		// 结算失败时仍返回拍卖结果（让上游知道"谁应该付钱但没付成"）
		return result, settle, fmt.Errorf("settlement failed: %w", err)
	}

	return result, settle, nil
}

// validateBids 对所有出价关联的内容做合规审核。
func (m *MarketMaker) validateBids(ctx context.Context, bids []types.BidResponse) error {
	for _, b := range bids {
		// 推断创作者：先查映射表，没有则用出价方（兜底）。
		creator, ok := m.lookupCreator(b.ContentID)
		if !ok {
			creator = b.BidderID // Phase 1 简化：未登记内容视为出价方自有
		}

		content := governance.Content{
			ContentID: b.ContentID,
			CreatorID: creator,
			Metadata: map[string]string{
				"bidder_id": b.BidderID,
				"bid":       fmt.Sprintf("%f", b.Bid),
			},
		}

		decision, err := m.plugin.ValidateContent(ctx, content)
		if err != nil {
			return fmt.Errorf("validate content %s: %w", b.ContentID, err)
		}
		if !decision.Approved {
			return fmt.Errorf("%w: %s (reason: %s)", ErrContentRejected, b.ContentID, decision.Reason)
		}
	}
	return nil
}

// settle 执行资金结算（扣款 → 转移 → 抽税）。
//
// 资金流向：
//
//	胜出者 ──(clearPrice)──► 系统账户 ──(creator_payout)──► 创作者
//	                                    └─(tax)──────────► 销毁
func (m *MarketMaker) settle(ctx context.Context, result *types.AuctionResult) (*Settlement, error) {
	if result == nil {
		return nil, errors.New("nil auction result")
	}

	// 金额单位：拍卖结果是 float64（注意力币），账本是 int64（最小单位：分）
	// 转换系数：1 注意力币 = 100 分
	const scale = int64(100)
	clearPriceUnits := int64(result.ClearPrice * float64(scale))
	if clearPriceUnits <= 0 {
		return &Settlement{ClearPrice: result.ClearPrice}, nil
	}

	// 计算分账：先抽税，剩余给创作者
	taxUnits := int64(float64(clearPriceUnits) * m.taxRate)
	creatorPayoutUnits := clearPriceUnits - taxUnits

	txRef := fmt.Sprintf("auction-%s-%s", result.SlotID, result.WinnerID)

	settle := &Settlement{
		ClearPrice:    result.ClearPrice,
		Tax:           float64(taxUnits) / float64(scale),
		CreatorPayout: float64(creatorPayoutUnits) / float64(scale),
		SponsorPaid:   result.ClearPrice,
	}

	// 步骤 a：胜出者 → 系统账户（扣款）
	transferResp, err := m.plugin.Transfer(ctx, governance.TransferRequest{
		From:   result.WinnerID,
		To:     SystemAccountID,
		Amount: clearPriceUnits,
		Reason: "auction_settlement",
		TxRef:  txRef,
	})
	if err != nil {
		return settle, fmt.Errorf("transfer winner→system: %w", err)
	}
	if !transferResp.Accepted {
		settle.SponsorBalance = transferResp.FromBalance
		return settle, fmt.Errorf("%w: %s", ErrInsufficientBalance, transferResp.Message)
	}
	settle.TransferTxID = transferResp.TxID
	settle.SponsorBalance = transferResp.FromBalance

	// 步骤 b：系统账户 → 创作者（分账）
	creator, ok := m.lookupCreator(result.ContentID)
	if !ok {
		// 未登记内容：全部留给系统账户（不向创作者分账，但也不报错——保证拍卖结果一致）
		m.logger.Warn("content creator unknown, skipping payout",
			"content_id", result.ContentID,
			"amount", creatorPayoutUnits,
		)
		settle.CreatorBalance = transferResp.ToBalance // 系统账户当前余额（含待分账）
	} else {
		payoutResp, err := m.plugin.Transfer(ctx, governance.TransferRequest{
			From:   SystemAccountID,
			To:     creator,
			Amount: creatorPayoutUnits,
			Reason: "creator_payout",
			TxRef:  txRef,
		})
		if err != nil {
			return settle, fmt.Errorf("transfer system→creator: %w", err)
		}
		if !payoutResp.Accepted {
			m.logger.Warn("creator payout failed",
				"creator", creator,
				"reason", payoutResp.Message,
			)
			settle.CreatorBalance = payoutResp.FromBalance
		} else {
			settle.PayoutTxID = payoutResp.TxID
			settle.CreatorBalance = payoutResp.ToBalance
		}
	}

	// 步骤 c：系统账户 → 销毁（抽税）
	burnResp, err := m.plugin.Burn(ctx, governance.BurnRequest{
		From:   SystemAccountID,
		Amount: taxUnits,
		Reason: "platform_tax",
		TxRef:  txRef,
	})
	if err != nil {
		m.logger.Warn("tax burn failed", "err", err)
	} else {
		settle.BurnTxID = burnResp.TxID
	}

	return settle, nil
}