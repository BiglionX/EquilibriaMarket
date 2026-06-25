// Package governance 定义可插拔治理层。
//
// 设计哲学（来自 PRD）：
//   - 治理（货币发行/内容审核/争议仲裁）与经济引擎解耦。
//   - 不同地区、不同合规要求 → 不同的治理插件。
//   - 引擎本身不关心"如何治理"，只关心"按规则办"。
//
// 本文件对应 PRD 中的 Protobuf 接口定义（service GovernancePlugin），
// 用 Go 接口的方式落地面向 Phase 1（中心化模式）。
package governance

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ===== 错误定义 =====

// ErrPluginNotFound 注册表中找不到指定插件。
var ErrPluginNotFound = errors.New("governance plugin not found")

// ErrPluginExists 重复注册同名插件。
var ErrPluginExists = errors.New("governance plugin already registered")

// ===== 请求/响应数据结构 =====
//
// 与 PRD 的 Protobuf 定义一一对应，便于未来切换到 gRPC 实现。

// MintRequest 货币发行请求。
type MintRequest struct {
	UserID    string `json:"user_id"`   // 接收方用户ID
	Amount    int64  `json:"amount"`    // 请求发行量
	Reason    string `json:"reason"`    // 发行原因（基础配额、行为奖励等）
	Timestamp int64  `json:"timestamp"` // 发起时间（防重放）
}

// MintResponse 货币发行响应。
type MintResponse struct {
	TxID       string `json:"tx_id"`       // 交易ID（链上哈希或中心化流水号）
	NewBalance int64  `json:"new_balance"` // 发行后余额
	Accepted   bool   `json:"accepted"`    // 是否成功
	Message    string `json:"message"`     // 附加说明
}

// Content 待审核内容。
type Content struct {
	ContentID string            `json:"content_id"` // 内容唯一ID
	CreatorID string            `json:"creator_id"` // 创建者ID
	Hash      string            `json:"hash"`       // 内容哈希（防篡改）
	Metadata  map[string]string `json:"metadata"`   // 附加元数据（标签、分类等）
}

// PolicyDecision 审核决策。
type PolicyDecision struct {
	Approved   bool   `json:"approved"`    // 是否通过
	Reason     string `json:"reason"`      // 决策理由
	ReviewerID string `json:"reviewer_id"` // 决策者ID（管理员/合约地址/社区）
}

// Dispute 争议。
type Dispute struct {
	DisputeID string `json:"dispute_id"` // 争议ID
	Plaintiff string `json:"plaintiff"`  // 原告
	Defendant string `json:"defendant"`  // 被告
	Evidence  string `json:"evidence"`   // 证据描述或链接
	Amount    int64  `json:"amount"`     // 争议涉及金额
}

// ArbitrationResult 仲裁结果。
type ArbitrationResult struct {
	Winner     string `json:"winner"`     // 胜诉方
	Loser      string `json:"loser"`      // 败诉方
	Ruling     string `json:"ruling"`     // 裁决书
	Arbitrator string `json:"arbitrator"` // 仲裁者
}

// RandomRequest 随机数请求。
type RandomRequest struct {
	Seed     string `json:"seed"`      // 种子（通常为请求哈希）
	RangeMin int64  `json:"range_min"` // 最小值（含）
	RangeMax int64  `json:"range_max"` // 最大值（含）
}

// RandomResponse 随机数响应。
type RandomResponse struct {
	Value int64  `json:"value"` // 随机数结果
	Proof string `json:"proof"` // 可验证证明（VRF proof 或中心化签名）
}

// TransferRequest 货币转移请求（做市商结算流程中的核心操作）。
//
// 用于"胜出者扣款 → 系统账户 → 创作者/平台分账"等场景。
// 治理插件可施加合规约束（如 KYC、白名单、限速）。
type TransferRequest struct {
	From   string `json:"from"`   // 转出方用户ID
	To     string `json:"to"`     // 接收方用户ID
	Amount int64  `json:"amount"` // 金额（最小单位：分）
	Reason string `json:"reason"` // 转移原因（如 "auction_settlement"）
	TxRef  string `json:"tx_ref"` // 关联交易引用（如拍卖清算ID），便于审计追溯
}

// TransferResponse 货币转移响应。
type TransferResponse struct {
	TxID        string `json:"tx_id"`        // 本次转移的交易ID
	FromBalance int64  `json:"from_balance"` // 转出方最新余额
	ToBalance   int64  `json:"to_balance"`   // 接收方最新余额
	Accepted    bool   `json:"accepted"`     // 是否成功
	Message     string `json:"message"`      // 附加说明（拒绝原因等）
}

// BurnRequest 货币销毁请求（系统抽税、惩罚回收等场景）。
type BurnRequest struct {
	From   string `json:"from"`   // 被销毁方（通常是系统账户或惩罚对象）
	Amount int64  `json:"amount"` // 销毁金额
	Reason string `json:"reason"` // 销毁原因（如 "platform_tax"、"penalty"）
	TxRef  string `json:"tx_ref"` // 关联交易引用
}

// BurnResponse 货币销毁响应。
type BurnResponse struct {
	TxID        string `json:"tx_id"`        // 销毁交易ID
	FromBalance int64  `json:"from_balance"` // 被销毁方最新余额
	Burned      int64  `json:"burned"`       // 实际销毁数量（可能因余额不足而小于请求量）
	Accepted    bool   `json:"accepted"`     // 是否成功
	Message     string `json:"message"`      // 附加说明
}

// ===== 核心接口 =====

// Plugin 治理插件接口。
//
// 实现该接口的类型可以独立治理整个市场——引擎通过该接口与治理层交互。
// PRD 中的 Protobuf 定义（service GovernancePlugin）即对应此接口。
type Plugin interface {
	// Name 返回插件唯一名称（如 "centralized"、"tokenized_compliance"）。
	Name() string

	// MintCurrency 按规则向用户发行货币。
	MintCurrency(ctx context.Context, req MintRequest) (*MintResponse, error)

	// Transfer 在两个账户之间转移货币（做市商结算的核心操作）。
	Transfer(ctx context.Context, req TransferRequest) (*TransferResponse, error)

	// Burn 从指定账户销毁货币（系统抽税、惩罚回收）。
	Burn(ctx context.Context, req BurnRequest) (*BurnResponse, error)

	// ValidateContent 判断内容或交易是否合规。
	ValidateContent(ctx context.Context, content Content) (*PolicyDecision, error)

	// ResolveDispute 提供争议仲裁。
	ResolveDispute(ctx context.Context, dispute Dispute) (*ArbitrationResult, error)

	// GetRandomSeed 提供防篡改的随机数。
	GetRandomSeed(ctx context.Context, req RandomRequest) (*RandomResponse, error)
}

// ===== 插件注册器 =====

// Factory 插件工厂函数：从配置创建插件实例。
//
// config 的具体 schema 由每个插件自行定义（通常对应 YAML/JSON 配置）。
type Factory func(config map[string]any) (Plugin, error)

// registry 全局插件注册表（线程安全）。
//
// 设计要点：
//   - 使用 init() 自注册，简化部署：导入即注册。
//   - sync.RWMutex 保护并发读写：注册是低频操作，运行时是只读。
type registry struct {
	mu    sync.RWMutex
	items map[string]Factory
}

var globalRegistry = &registry{
	items: make(map[string]Factory),
}

// Register 注册一个插件工厂。
//
// 通常在插件包的 init() 中调用，例如：
//
//	func init() {
//	    governance.Register("centralized", NewCentralizedPlugin)
//	}
func Register(name string, factory Factory) error {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	if _, exists := globalRegistry.items[name]; exists {
		return fmt.Errorf("%w: %s", ErrPluginExists, name)
	}
	globalRegistry.items[name] = factory
	return nil
}

// MustRegister 是 Register 的"失败即 panic"版本，用于 init() 中。
//
// 如果插件名重复，说明代码中存在严重 bug，应当立即暴露而非静默忽略。
func MustRegister(name string, factory Factory) {
	if err := Register(name, factory); err != nil {
		panic(err)
	}
}

// Load 通过名称和配置加载插件。
//
// 引擎启动时通过配置（如 YAML）调用此函数，动态激活治理插件。
func Load(name string, config map[string]any) (Plugin, error) {
	globalRegistry.mu.RLock()
	factory, ok := globalRegistry.items[name]
	globalRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPluginNotFound, name)
	}
	return factory(config)
}

// List 返回所有已注册插件的名称。
//
// 主要用于运维/调试：如 `equilibria-engine governance list`。
func List() []string {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	names := make([]string, 0, len(globalRegistry.items))
	for name := range globalRegistry.items {
		names = append(names, name)
	}
	return names
}

// Unregister 注销一个插件（主要用于测试）。
func Unregister(name string) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	delete(globalRegistry.items, name)
}