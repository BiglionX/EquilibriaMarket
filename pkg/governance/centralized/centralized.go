// Package centralized 实现"中心化管理员"治理插件。
//
// 适用场景：政策严格、需要强力风控的环境。
// 技术要点：
//   - 管理员拥有最高权限。
//   - 货币发行由管理员按固定通胀率控制。
//   - 内容审核由 AI + 人工结合（Phase 1 简化为直接通过）。
//   - 争议仲裁由管理员裁决。
//
// 对应 PRD 治理插件示例一（centralized）。
package centralized

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"
	"time"

	"market-engine/pkg/governance"
)

// PluginName 是插件在注册表中的唯一标识。
const PluginName = "centralized"

// Config 中心化插件的配置。
//
// 与 PRD 中的 YAML 示例一一对应。
type Config struct {
	AdminAddress  string  `yaml:"admin_address"`  // 管理员地址（"0xAdmin..."）
	InflationRate float64 `yaml:"inflation_rate"` // 通胀率（默认 0.05 = 5%）
	TaxRate       float64 `yaml:"tax_rate"`       // 系统抽水税率（默认 0.001 = 0.1%）
}

// DefaultConfig 返回默认配置，对应 PRD 中的示例值。
func DefaultConfig() Config {
	return Config{
		AdminAddress:  "0xAdmin0000000000000000000000000000000000",
		InflationRate: 0.05,
		TaxRate:       0.001,
	}
}

// Plugin 中心化管理员插件的具体实现。
//
// 实现了 governance.Plugin 接口的所有方法。
type Plugin struct {
	adminAddress  string
	inflationRate float64
	taxRate       float64

	// 用户余额的内存缓存（Phase 1 用，Phase 2 替换为持久化数据库）
	mu      sync.RWMutex
	balance map[string]int64
}

// New 创建中心化插件实例。
//
// 通常通过工厂函数 NewFactory() 在引擎启动时调用。
func New(cfg Config) *Plugin {
	if cfg.InflationRate <= 0 {
		cfg.InflationRate = 0.05
	}
	if cfg.AdminAddress == "" {
		cfg.AdminAddress = DefaultConfig().AdminAddress
	}
	return &Plugin{
		adminAddress:  cfg.AdminAddress,
		inflationRate: cfg.InflationRate,
		taxRate:       cfg.TaxRate,
		balance:       make(map[string]int64),
	}
}

// NewFactory 返回 governance.Factory，用于注册到全局注册表。
//
// 这是 governance 包与具体实现之间的标准桥梁。
func NewFactory() governance.Factory {
	return func(config map[string]any) (governance.Plugin, error) {
		cfg := DefaultConfig()
		if v, ok := config["admin_address"].(string); ok {
			cfg.AdminAddress = v
		}
		if v, ok := config["inflation_rate"].(float64); ok && v > 0 {
			cfg.InflationRate = v
		}
		if v, ok := config["tax_rate"].(float64); ok && v > 0 {
			cfg.TaxRate = v
		}
		return New(cfg), nil
	}
}

// Name 实现 governance.Plugin。
func (p *Plugin) Name() string { return PluginName }

// MintCurrency 实现 governance.Plugin。
//
// 规则：按通胀率发行。
//   - 实际发行量 = 请求量 × 通胀率。
//   - 最小发行量 1 单位（确保激励可感知）。
//   - 返回的交易ID以 "centralized-mint-" 开头，便于审计识别。
func (p *Plugin) MintCurrency(ctx context.Context, req governance.MintRequest) (*governance.MintResponse, error) {
	if req.UserID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if req.Amount <= 0 {
		return &governance.MintResponse{
			Accepted: false,
			Message:  "amount must be positive",
		}, nil
	}

	// 按通胀率计算实际发行量
	actual := int64(float64(req.Amount) * p.inflationRate)
	if actual < 1 {
		actual = 1
	}

	// 更新余额（线程安全）
	p.mu.Lock()
	p.balance[req.UserID] += actual
	newBalance := p.balance[req.UserID]
	p.mu.Unlock()

	return &governance.MintResponse{
		TxID:       fmt.Sprintf("centralized-mint-%d-%s", time.Now().UnixNano(), req.UserID),
		NewBalance: newBalance,
		Accepted:   true,
		Message:    fmt.Sprintf("minted %d (reason: %s)", actual, req.Reason),
	}, nil
}

// ValidateContent 实现 governance.Plugin。
//
// Phase 1 策略：直接通过。
// Phase 2 计划：调用 AI 模型 + 人工抽审。
//
// 返回的 ReviewerID 是管理员地址，便于审计追溯。
func (p *Plugin) ValidateContent(ctx context.Context, content governance.Content) (*governance.PolicyDecision, error) {
	if content.ContentID == "" {
		return &governance.PolicyDecision{
			Approved:   false,
			Reason:     "content_id is required",
			ReviewerID: p.adminAddress,
		}, nil
	}

	// Phase 1: 简单通过；Phase 2 将引入 AI 审核 + 人工抽审
	return &governance.PolicyDecision{
		Approved:   true,
		Reason:     "centralized auto-approve (Phase 1 MVP)",
		ReviewerID: p.adminAddress,
	}, nil
}

// Transfer 实现 governance.Plugin。
//
// 在两个账户之间转移货币，使用写锁保证原子性。
//
// 错误处理：
//   - From 或 To 为空 → 拒绝
//   - Amount <= 0 → 拒绝
//   - From 余额不足 → 拒绝（不部分执行，避免复杂对账）
func (p *Plugin) Transfer(ctx context.Context, req governance.TransferRequest) (*governance.TransferResponse, error) {
	if req.From == "" || req.To == "" {
		return nil, fmt.Errorf("from and to are required")
	}
	if req.From == req.To {
		return nil, fmt.Errorf("from and to cannot be the same")
	}
	if req.Amount <= 0 {
		return &governance.TransferResponse{
			Accepted: false,
			Message:  "amount must be positive",
		}, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	fromBalance := p.balance[req.From]
	if fromBalance < req.Amount {
		return &governance.TransferResponse{
			TxID:        "",
			FromBalance: fromBalance,
			ToBalance:   p.balance[req.To],
			Accepted:    false,
			Message:     fmt.Sprintf("insufficient balance: have %d, need %d", fromBalance, req.Amount),
		}, nil
	}

	// 原子扣减 + 增加
	p.balance[req.From] -= req.Amount
	p.balance[req.To] += req.Amount

	txID := fmt.Sprintf("centralized-transfer-%d-%s-%s", time.Now().UnixNano(), req.From, req.To)
	return &governance.TransferResponse{
		TxID:        txID,
		FromBalance: p.balance[req.From],
		ToBalance:   p.balance[req.To],
		Accepted:    true,
		Message:     fmt.Sprintf("transferred %d (%s)", req.Amount, req.Reason),
	}, nil
}

// Burn 实现 governance.Plugin。
//
// 从指定账户销毁货币（用于系统抽税、惩罚回收）。
//
// 错误处理：
//   - From 为空 → 拒绝
//   - Amount <= 0 → 拒绝
//   - 余额不足 → 实际销毁量 = 当前余额（不报错，便于"清扫"零头账户）
func (p *Plugin) Burn(ctx context.Context, req governance.BurnRequest) (*governance.BurnResponse, error) {
	if req.From == "" {
		return nil, fmt.Errorf("from is required")
	}
	if req.Amount <= 0 {
		return &governance.BurnResponse{
			Accepted: false,
			Message:  "amount must be positive",
		}, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	balance := p.balance[req.From]
	burned := req.Amount
	if burned > balance {
		burned = balance // 清扫零头
	}
	p.balance[req.From] -= burned

	txID := fmt.Sprintf("centralized-burn-%d-%s", time.Now().UnixNano(), req.From)
	return &governance.BurnResponse{
		TxID:        txID,
		FromBalance: p.balance[req.From],
		Burned:      burned,
		Accepted:    true,
		Message:     fmt.Sprintf("burned %d (%s)", burned, req.Reason),
	}, nil
}

// ResolveDispute 实现 governance.Plugin。
//
// Phase 1 策略：管理员裁决（此处简化为支持原告）。
// Phase 2 计划：人工审核 + 多重签名（联盟模式）。
func (p *Plugin) ResolveDispute(ctx context.Context, dispute governance.Dispute) (*governance.ArbitrationResult, error) {
	if dispute.DisputeID == "" {
		return nil, fmt.Errorf("dispute_id is required")
	}
	if dispute.Plaintiff == "" || dispute.Defendant == "" {
		return nil, fmt.Errorf("plaintiff and defendant are required")
	}

	return &governance.ArbitrationResult{
		Winner:     dispute.Plaintiff, // Phase 1 简化：默认支持原告
		Loser:      dispute.Defendant,
		Ruling:     fmt.Sprintf("centralized admin ruling by %s on dispute %s", p.adminAddress, dispute.DisputeID),
		Arbitrator: p.adminAddress,
	}, nil
}

// GetRandomSeed 实现 governance.Plugin。
//
// 使用 crypto/rand 提供密码学安全的伪随机数。
// Phase 1 在中心化模式下由管理员"见证"，未来 VRF 模式可改为可验证证明。
func (p *Plugin) GetRandomSeed(ctx context.Context, req governance.RandomRequest) (*governance.RandomResponse, error) {
	if req.RangeMax < req.RangeMin {
		return nil, fmt.Errorf("invalid range: [%d, %d]", req.RangeMin, req.RangeMax)
	}

	// 在 [RangeMin, RangeMax] 区间内生成随机数（含两端）
	span := big.NewInt(req.RangeMax - req.RangeMin + 1)
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random: %w", err)
	}
	value := n.Int64() + req.RangeMin

	// 简化的"证明"：管理员签名占位（Phase 1 中心化模式即可）
	proof := fmt.Sprintf("centralized-sig-%s-%d", p.adminAddress, time.Now().UnixNano())

	return &governance.RandomResponse{
		Value: value,
		Proof: proof,
	}, nil
}

// GetBalance 查询用户余额（仅供测试/调试使用，不属于 Plugin 接口）。
func (p *Plugin) GetBalance(userID string) int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.balance[userID]
}

// Snapshot 返回所有用户余额的拷贝快照（线程安全）。
//
// 主要用于 admin console / 监控面板读取整体分布：
//   - 调用方应将其视为只读副本，不要在外部修改。
//   - 实时反映最新余额（含 Mint/Transfer/Burn 的结果）。
func (p *Plugin) Snapshot() map[string]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]int64, len(p.balance))
	for k, v := range p.balance {
		out[k] = v
	}
	return out
}

// Compile-time interface check
var _ governance.Plugin = (*Plugin)(nil)

// init 自动注册到治理注册表。
//
// 这样引擎只需 `_ import "market-engine/pkg/governance/centralized"` 即可激活本插件。
func init() {
	governance.MustRegister(PluginName, NewFactory())
}

// randHex 辅助函数：生成指定字节数的随机十六进制字符串（供测试使用）。
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}