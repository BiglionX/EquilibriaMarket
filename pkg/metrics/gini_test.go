package metrics

import (
	"math"
	"testing"
	"time"
)

// TestGini_Empty 空快照应该返回零值。
func TestGini_Empty(t *testing.T) {
	rep := Compute(Snapshot{Balances: map[string]int64{}})

	if rep.Gini != 0 {
		t.Errorf("expected Gini=0 for empty snapshot, got %v", rep.Gini)
	}
	if rep.TotalAccounts != 0 {
		t.Errorf("expected TotalAccounts=0, got %d", rep.TotalAccounts)
	}
	if rep.TotalSupply != 0 {
		t.Errorf("expected TotalSupply=0, got %d", rep.TotalSupply)
	}
}

// TestGini_PerfectEquality 完全平等时 Gini=0。
//
// 例如 5 个账户都持有 100。
func TestGini_PerfectEquality(t *testing.T) {
	balances := map[string]int64{
		"a": 100, "b": 100, "c": 100, "d": 100, "e": 100,
	}
	rep := Compute(Snapshot{Balances: balances})

	if math.Abs(rep.Gini) > 1e-9 {
		t.Errorf("expected Gini≈0 for perfect equality, got %v", rep.Gini)
	}
	if rep.Mean != 100 {
		t.Errorf("expected Mean=100, got %v", rep.Mean)
	}
	if rep.Median != 100 {
		t.Errorf("expected Median=100, got %v", rep.Median)
	}
	if rep.TotalSupply != 500 {
		t.Errorf("expected TotalSupply=500, got %d", rep.TotalSupply)
	}
	if rep.ActiveAccounts != 5 {
		t.Errorf("expected 5 active accounts, got %d", rep.ActiveAccounts)
	}
}

// TestGini_PerfectInequality 最不平等：一个账户拿走全部。
//
// 例如 1 个账户 100，4 个账户 0。
func TestGini_PerfectInequality(t *testing.T) {
	balances := map[string]int64{
		"rich": 100,
		"a":    0, "b": 0, "c": 0, "d": 0,
	}
	rep := Compute(Snapshot{Balances: balances})

	// n=5, values=[0,0,0,0,100]
	// weightedSum = Σ(2i - n - 1) * x_i, i 从 1
	// i=1: (2-6)*0 = 0
	// i=2: (4-6)*0 = 0
	// i=3: (6-6)*0 = 0
	// i=4: (8-6)*0 = 0
	// i=5: (10-6)*100 = 400
	// G = 400 / (5*100) = 0.8
	expected := 0.8
	if math.Abs(rep.Gini-expected) > 1e-9 {
		t.Errorf("expected Gini=%v, got %v", expected, rep.Gini)
	}
}

// TestGini_TypicalDistribution 典型 Pareto 分布。
func TestGini_TypicalDistribution(t *testing.T) {
	// 模拟一个 1% 的人持 50% 财富的场景
	balances := make(map[string]int64)
	// 1 个富户
	balances["rich"] = 50
	// 99 个普通人各持 50/99
	for i := 0; i < 99; i++ {
		balances[string(rune('a'+i%26))+string(rune('A'+i/26))] = 0 // 部分为 0，下面覆盖
	}
	// 简化：10 个中产各 1，90 个穷光蛋
	balances = make(map[string]int64)
	balances["rich"] = 50
	for i := 0; i < 10; i++ {
		balances[string(rune('a'+i))] = 1
	}
	for i := 0; i < 89; i++ {
		balances[string(rune('A'+i%26))+string(rune('0'+i/26))] = 0
	}

	rep := Compute(Snapshot{Balances: balances})

	// 理论 Gini 应该在 0.85-1.0 范围内（极端不平等）
	if rep.Gini < 0.85 || rep.Gini > 1.0 {
		t.Errorf("expected Gini in [0.85, 1.0] for skewed distribution, got %v", rep.Gini)
	}
	if rep.ActiveAccounts != 11 {
		t.Errorf("expected 11 active accounts, got %d", rep.ActiveAccounts)
	}
	if rep.TotalSupply != 60 {
		t.Errorf("expected TotalSupply=60, got %d", rep.TotalSupply)
	}
}

// TestGini_BoundaryValues 验证 Gini 在 [0, 1] 范围内。
func TestGini_BoundaryValues(t *testing.T) {
	balances := map[string]int64{
		"a": 1, "b": 2, "c": 3, "d": 4, "e": 5,
	}
	rep := Compute(Snapshot{Balances: balances})

	if rep.Gini < 0 || rep.Gini > 1 {
		t.Errorf("Gini out of [0,1]: %v", rep.Gini)
	}

	// 验证理论值
	// 排序后 [1,2,3,4,5], n=5, sum=15
	// i=1: (2-6)*1 = -4
	// i=2: (4-6)*2 = -4
	// i=3: (6-6)*3 = 0
	// i=4: (8-6)*4 = 8
	// i=5: (10-6)*5 = 20
	// weighted = -4-4+0+8+20 = 20
	// G = 20 / (5*15) = 20/75 ≈ 0.2667
	expected := 20.0 / 75.0
	if math.Abs(rep.Gini-expected) > 1e-9 {
		t.Errorf("expected Gini=%v, got %v", expected, rep.Gini)
	}
}

// TestLorenzCurve 验证洛伦兹曲线单调递增且终点为 (1, 1)。
func TestLorenzCurve(t *testing.T) {
	balances := map[string]int64{
		"a": 10, "b": 20, "c": 30, "d": 40, "e": 50,
	}
	rep := Compute(Snapshot{Balances: balances})

	curve := rep.LorenzCurve
	if len(curve) < 2 {
		t.Fatalf("Lorenz curve should have at least 2 points, got %d", len(curve))
	}

	// 起点必须是 (0, 0)
	if curve[0].X != 0 || curve[0].Y != 0 {
		t.Errorf("expected start (0, 0), got (%v, %v)", curve[0].X, curve[0].Y)
	}
	// 终点必须是 (1, 1)
	last := curve[len(curve)-1]
	if last.X != 1 || last.Y != 1 {
		t.Errorf("expected end (1, 1), got (%v, %v)", last.X, last.Y)
	}
	// 必须单调递增
	for i := 1; i < len(curve); i++ {
		if curve[i].Y < curve[i-1].Y-1e-9 {
			t.Errorf("Lorenz curve should be non-decreasing, but Y[%d]=%v < Y[%d]=%v",
				i, curve[i].Y, i-1, curve[i-1].Y)
		}
	}
}

// TestTopHolders 验证 Top-K 排序正确。
func TestTopHolders(t *testing.T) {
	balances := map[string]int64{
		"alice": 100,
		"bob":   500,
		"carol": 200,
		"dave":  0, // 应该被排除
		"eve":   300,
	}
	tops := TopHolders(Snapshot{Balances: balances}, 3)

	if len(tops) != 3 {
		t.Fatalf("expected 3 top holders, got %d", len(tops))
	}
	expected := []string{"bob", "eve", "carol"}
	for i, h := range tops {
		if h.UserID != expected[i] {
			t.Errorf("top[%d]: expected %s, got %s", i, expected[i], h.UserID)
		}
	}
	// 0 余额的不应出现
	for _, h := range tops {
		if h.UserID == "dave" {
			t.Error("dave (zero balance) should be excluded from top holders")
		}
	}
}

// TestTopHolders_LessThanK 当账户数少于 K 时返回全部。
func TestTopHolders_LessThanK(t *testing.T) {
	balances := map[string]int64{"a": 10, "b": 20}
	tops := TopHolders(Snapshot{Balances: balances}, 10)
	if len(tops) != 2 {
		t.Errorf("expected 2 holders, got %d", len(tops))
	}
}

// TestTimestamp 验证时间戳被设置。
func TestTimestamp(t *testing.T) {
	before := time.Now()
	rep := Compute(Snapshot{Balances: map[string]int64{"a": 1}})
	after := time.Now()

	if rep.Timestamp.Before(before) || rep.Timestamp.After(after) {
		t.Errorf("timestamp out of range: %v (expected between %v and %v)",
			rep.Timestamp, before, after)
	}
}
