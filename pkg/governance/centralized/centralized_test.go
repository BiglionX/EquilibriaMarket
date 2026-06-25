package centralized

import (
	"context"
	"strings"
	"testing"

	"market-engine/pkg/governance"
)

func TestPlugin_Name(t *testing.T) {
	p := New(DefaultConfig())
	if p.Name() != "centralized" {
		t.Errorf("Name() = %s, want centralized", p.Name())
	}
}

func TestPlugin_MintCurrency(t *testing.T) {
	p := New(Config{
		AdminAddress:  "0xAdmin",
		InflationRate: 0.5, // 50% 通胀率便于测试
	})
	ctx := context.Background()

	tests := []struct {
		name       string
		req        governance.MintRequest
		wantAccept bool
		wantMin    int64
	}{
		{
			name:       "正常发行",
			req:        governance.MintRequest{UserID: "alice", Amount: 100, Reason: "daily_quota"},
			wantAccept: true,
			wantMin:    1,
		},
		{
			name:       "小额发行/至少 1 单位",
			req:        governance.MintRequest{UserID: "bob", Amount: 1, Reason: "reward"},
			wantAccept: true,
			wantMin:    1,
		},
		{
			name:       "零金额/拒绝",
			req:        governance.MintRequest{UserID: "carol", Amount: 0, Reason: "test"},
			wantAccept: false,
		},
		{
			name:       "负金额/拒绝",
			req:        governance.MintRequest{UserID: "dave", Amount: -10, Reason: "test"},
			wantAccept: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := p.MintCurrency(ctx, tt.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Accepted != tt.wantAccept {
				t.Errorf("Accepted = %v, want %v", resp.Accepted, tt.wantAccept)
			}
			if tt.wantAccept {
				if !strings.HasPrefix(resp.TxID, "centralized-mint-") {
					t.Errorf("TxID should start with 'centralized-mint-', got %s", resp.TxID)
				}
				if resp.NewBalance < tt.wantMin {
					t.Errorf("NewBalance = %d, want >= %d", resp.NewBalance, tt.wantMin)
				}
			}
		})
	}
}

func TestPlugin_MintCurrency_UserIDRequired(t *testing.T) {
	p := New(DefaultConfig())
	_, err := p.MintCurrency(context.Background(), governance.MintRequest{Amount: 100})
	if err == nil {
		t.Fatal("expected error when user_id is empty")
	}
}

func TestPlugin_ValidateContent(t *testing.T) {
	p := New(DefaultConfig())
	ctx := context.Background()

	t.Run("正常内容/自动通过", func(t *testing.T) {
		content := governance.Content{
			ContentID: "c-001",
			CreatorID: "alice",
			Hash:      "0xabc",
		}
		decision, err := p.ValidateContent(ctx, content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !decision.Approved {
			t.Errorf("expected approved=true, got false (reason: %s)", decision.Reason)
		}
		if decision.ReviewerID == "" {
			t.Error("ReviewerID should not be empty")
		}
	})

	t.Run("缺少 content_id/拒绝", func(t *testing.T) {
		decision, err := p.ValidateContent(ctx, governance.Content{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Approved {
			t.Error("expected approved=false for empty content_id")
		}
	})
}

func TestPlugin_ResolveDispute(t *testing.T) {
	p := New(DefaultConfig())
	ctx := context.Background()

	t.Run("正常仲裁", func(t *testing.T) {
		dispute := governance.Dispute{
			DisputeID: "d-001",
			Plaintiff: "alice",
			Defendant: "bob",
			Evidence:  "transaction hash 0x...",
			Amount:    100,
		}
		result, err := p.ResolveDispute(ctx, dispute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Winner != "alice" {
			t.Errorf("Winner = %s, want alice", result.Winner)
		}
		if result.Loser != "bob" {
			t.Errorf("Loser = %s, want bob", result.Loser)
		}
		if result.Arbitrator == "" {
			t.Error("Arbitrator should not be empty")
		}
	})

	t.Run("缺少 dispute_id/错误", func(t *testing.T) {
		_, err := p.ResolveDispute(ctx, governance.Dispute{Plaintiff: "a", Defendant: "b"})
		if err == nil {
			t.Error("expected error when dispute_id is empty")
		}
	})
}

func TestPlugin_GetRandomSeed(t *testing.T) {
	p := New(DefaultConfig())
	ctx := context.Background()

	t.Run("正常范围", func(t *testing.T) {
		req := governance.RandomRequest{Seed: "test", RangeMin: 1, RangeMax: 100}
		resp, err := p.GetRandomSeed(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Value < 1 || resp.Value > 100 {
			t.Errorf("Value = %d, want in [1, 100]", resp.Value)
		}
		if resp.Proof == "" {
			t.Error("Proof should not be empty")
		}
	})

	t.Run("范围反转/错误", func(t *testing.T) {
		req := governance.RandomRequest{Seed: "test", RangeMin: 100, RangeMax: 1}
		_, err := p.GetRandomSeed(ctx, req)
		if err == nil {
			t.Error("expected error for invalid range")
		}
	})

	t.Run("单点范围", func(t *testing.T) {
		req := governance.RandomRequest{Seed: "test", RangeMin: 42, RangeMax: 42}
		resp, err := p.GetRandomSeed(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Value != 42 {
			t.Errorf("Value = %d, want 42", resp.Value)
		}
	})
}

func TestPlugin_BalanceAccumulation(t *testing.T) {
	p := New(Config{InflationRate: 1.0}) // 100% 通胀便于断言
	ctx := context.Background()

	// 多次发行应累积
	for i := 0; i < 5; i++ {
		_, _ = p.MintCurrency(ctx, governance.MintRequest{UserID: "alice", Amount: 10})
	}

	want := int64(50)
	if got := p.GetBalance("alice"); got != want {
		t.Errorf("balance = %d, want %d", got, want)
	}
}

func TestRegistry_AutoRegistration(t *testing.T) {
	// 导入本包后，PluginName 应已注册到全局注册表
	plugins := governance.List()
	found := false
	for _, name := range plugins {
		if name == PluginName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("plugin %q not found in registry: %v", PluginName, plugins)
	}
}

func TestRegistry_Load(t *testing.T) {
	plugin, err := governance.Load(PluginName, map[string]any{
		"admin_address":  "0xTestAdmin",
		"inflation_rate": 0.1,
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if plugin.Name() != PluginName {
		t.Errorf("Name() = %s, want %s", plugin.Name(), PluginName)
	}
}

func TestRegistry_LoadNotFound(t *testing.T) {
	_, err := governance.Load("nonexistent-plugin", nil)
	if err == nil {
		t.Error("expected error for nonexistent plugin")
	}
}

// ===== Transfer / Burn 单元测试 =====

// seedBalance 直接注入余额（绕过 MintCurrency 的通胀率），便于测试。
func seedBalance(t *testing.T, p *Plugin, userID string, amount int64) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.balance[userID] = amount
}

func TestPlugin_Transfer(t *testing.T) {
	p := New(DefaultConfig())
	ctx := context.Background()

	seedBalance(t, p, "alice", 1000)
	seedBalance(t, p, "bob", 100)

	tests := []struct {
		name        string
		req         governance.TransferRequest
		wantAccept  bool
		wantAlice   int64
		wantBob     int64
	}{
		{
			name:       "正常转账",
			req:        governance.TransferRequest{From: "alice", To: "bob", Amount: 300, Reason: "payment", TxRef: "tx-1"},
			wantAccept: true,
			wantAlice:  700,
			wantBob:    400,
		},
		{
			name:       "余额不足/拒绝",
			req:        governance.TransferRequest{From: "bob", To: "alice", Amount: 99999, Reason: "test"},
			wantAccept: false,
			wantAlice:  700, // 不变
			wantBob:    400, // 不变
		},
		{
			name:       "金额为0/拒绝",
			req:        governance.TransferRequest{From: "alice", To: "bob", Amount: 0},
			wantAccept: false,
		},
		{
			name:       "负金额/拒绝",
			req:        governance.TransferRequest{From: "alice", To: "bob", Amount: -50},
			wantAccept: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := p.Transfer(ctx, tt.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Accepted != tt.wantAccept {
				t.Errorf("Accepted = %v, want %v (msg=%s)", resp.Accepted, tt.wantAccept, resp.Message)
			}
			if tt.wantAccept {
				if resp.TxID == "" {
					t.Error("TxID should not be empty on success")
				}
				if resp.FromBalance != tt.wantAlice {
					t.Errorf("alice balance = %d, want %d", resp.FromBalance, tt.wantAlice)
				}
				if resp.ToBalance != tt.wantBob {
					t.Errorf("bob balance = %d, want %d", resp.ToBalance, tt.wantBob)
				}
			}
		})
	}
}

func TestPlugin_Transfer_ValidationErrors(t *testing.T) {
	p := New(DefaultConfig())
	ctx := context.Background()

	t.Run("缺少 From", func(t *testing.T) {
		_, err := p.Transfer(ctx, governance.TransferRequest{To: "bob", Amount: 10})
		if err == nil {
			t.Error("expected error")
		}
	})
	t.Run("缺少 To", func(t *testing.T) {
		_, err := p.Transfer(ctx, governance.TransferRequest{From: "alice", Amount: 10})
		if err == nil {
			t.Error("expected error")
		}
	})
	t.Run("自转账", func(t *testing.T) {
		_, err := p.Transfer(ctx, governance.TransferRequest{From: "alice", To: "alice", Amount: 10})
		if err == nil {
			t.Error("expected error for self-transfer")
		}
	})
}

func TestPlugin_Burn(t *testing.T) {
	p := New(DefaultConfig())
	ctx := context.Background()

	seedBalance(t, p, "alice", 500)

	tests := []struct {
		name       string
		req        governance.BurnRequest
		wantAccept bool
		wantBurned int64
		wantLeft   int64
	}{
		{
			name:       "正常销毁",
			req:        governance.BurnRequest{From: "alice", Amount: 100, Reason: "platform_tax"},
			wantAccept: true,
			wantBurned: 100,
			wantLeft:   400,
		},
		{
			name:       "销毁量大于余额/按余额清扫",
			req:        governance.BurnRequest{From: "alice", Amount: 99999, Reason: "sweep"},
			wantAccept: true,
			wantBurned: 400,
			wantLeft:   0,
		},
		{
			name:       "金额为0/拒绝",
			req:        governance.BurnRequest{From: "alice", Amount: 0},
			wantAccept: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := p.Burn(ctx, tt.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Accepted != tt.wantAccept {
				t.Errorf("Accepted = %v, want %v", resp.Accepted, tt.wantAccept)
			}
			if tt.wantAccept {
				if resp.Burned != tt.wantBurned {
					t.Errorf("Burned = %d, want %d", resp.Burned, tt.wantBurned)
				}
				if resp.FromBalance != tt.wantLeft {
					t.Errorf("FromBalance = %d, want %d", resp.FromBalance, tt.wantLeft)
				}
			}
		})
	}
}

func TestPlugin_Burn_ValidationErrors(t *testing.T) {
	p := New(DefaultConfig())
	_, err := p.Burn(context.Background(), governance.BurnRequest{Amount: 100})
	if err == nil {
		t.Error("expected error when from is empty")
	}
}