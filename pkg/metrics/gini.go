// Package metrics 提供市场健康度指标计算。
//
// 现阶段实现：
//   - 基尼系数（Gini coefficient）：衡量货币分布的不平等程度。
//   - Lorenz 曲线：基尼系数的可视化前置数据。
//   - 基础统计：总账户数、零余额账户数、总发行量、均值、中位数、Top-K。
//
// 设计原则：
//   - 纯函数：输入 map，输出结构化结果，便于测试。
//   - 不依赖 governance 包：通过传入快照数据解耦。
//
// 参考：
//   - 基尼系数定义：https://en.wikipedia.org/wiki/Gini_coefficient
package metrics

import (
	"math"
	"sort"
	"time"
)

// Snapshot 是系统状态的瞬时快照（由调用方提供）。
//
// 设计目的：让 metrics 包保持纯粹（无外部依赖），调用方从治理插件拉取数据。
type Snapshot struct {
	// Balances 全部账户余额（包含零余额账户；调用方负责过滤）。
	Balances map[string]int64

	// Timestamp 快照时间。
	Timestamp time.Time
}

// GiniReport 基尼系数计算结果。
type GiniReport struct {
	// Gini 基尼系数 [0, 1]。
	//   - 0 表示完全平等（所有账户余额相同）。
	//   - 1 表示完全不平等（一个账户拿走所有）。
	Gini float64 `json:"gini"`

	// TotalAccounts 总账户数。
	TotalAccounts int `json:"total_accounts"`

	// ActiveAccounts 余额 > 0 的账户数。
	ActiveAccounts int `json:"active_accounts"`

	// ZeroBalanceAccounts 余额 = 0 的账户数。
	ZeroBalanceAccounts int `json:"zero_balance_accounts"`

	// TotalSupply 总发行量（所有非负余额之和）。
	TotalSupply int64 `json:"total_supply"`

	// Mean 平均余额（仅算 Active 账户）。
	Mean float64 `json:"mean"`

	// Median 中位数余额（仅算 Active 账户）。
	Median float64 `json:"median"`

	// TopConcentrationTopN 头部 N 个账户占总发行量的比例。
	TopConcentrationTopN float64 `json:"top_concentration_top_n"`

	// LorenzCurve 洛伦兹曲线采样点（X, Y）配对；前端可绘图。
	//   - X 轴：累计人口比例 [0, 1]
	//   - Y 轴：累计财富比例 [0, 1]
	// 完美平等时为对角线 y=x。
	// 为了避免点数过多，采样 ~20 个点。
	LorenzCurve []LorenzPoint `json:"lorenz_curve"`

	// Timestamp 指标生成时间。
	Timestamp time.Time `json:"timestamp"`
}

// LorenzPoint 洛伦兹曲线上的一个采样点。
type LorenzPoint struct {
	X float64 `json:"x"` // 累计人口比例
	Y float64 `json:"y"` // 累计财富比例
}

// Compute 计算完整基尼系数报告。
//
// 算法说明：
//  1. 提取所有非负余额，排序（升序）。
//  2. 用标准公式 G = (Σ(2i - n - 1) * x_i) / (n * Σx_i)，i 从 1 开始编号。
//     当所有余额为 0 时返回 G=0（避免除以 0）。
//  3. 同步计算均值、中位数、Top-N 集中度、洛伦兹曲线。
//
// 时间复杂度：O(n log n) 由排序决定。
func Compute(snap Snapshot) GiniReport {
	report := GiniReport{
		Timestamp: time.Now(),
	}

	report.TotalAccounts = len(snap.Balances)

	// 提取并排序
	values := make([]int64, 0, len(snap.Balances))
	for _, v := range snap.Balances {
		if v < 0 {
			// 负余额（透支）在 Phase 1 中心化模式下不应该出现；跳过。
			// 真实系统里会做风控。
			continue
		}
		values = append(values, v)
	}

	if len(values) == 0 {
		// 没有任何账户
		report.LorenzCurve = []LorenzPoint{{X: 0, Y: 0}, {X: 1, Y: 0}}
		return report
	}

	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })

	// 基础统计
	var totalSupply int64
	activeCount := 0
	for _, v := range values {
		totalSupply += v
		if v > 0 {
			activeCount++
		}
	}
	report.TotalSupply = totalSupply
	report.ActiveAccounts = activeCount
	report.ZeroBalanceAccounts = report.TotalAccounts - activeCount

	if activeCount > 0 {
		report.Mean = float64(totalSupply) / float64(activeCount)
		// 中位数：奇数取中间，偶数取平均
		// 注意：values 包含 0，所以需要从 activeCount 反推位置
		// 简化处理：values 已升序，零余额在最前
		// 实际有 0 也有 > 0，排序后 [0,0,0,...,1,2,3]
		// activeCount 起始位置 = report.TotalAccounts - activeCount
		activeStart := report.TotalAccounts - activeCount
		if activeCount%2 == 1 {
			report.Median = float64(values[activeStart+activeCount/2])
		} else {
			mid := activeStart + activeCount/2
			report.Median = float64(values[mid-1]+values[mid]) / 2.0
		}
	}

	// 基尼系数（用所有 values，包括 0）
	n := len(values)
	if n == 0 || totalSupply == 0 {
		report.Gini = 0
	} else {
		var weightedSum float64
		for i, v := range values {
			// 公式：G = (2*Σ(i*x_i) - (n+1)*Σx_i) / (n*Σx_i)
			// i 是 1-indexed
			weightedSum += float64(2*(i+1)-n-1) * float64(v)
		}
		report.Gini = weightedSum / float64(n*int(totalSupply))
		// 数值稳定性：clip 到 [0, 1]
		if report.Gini < 0 {
			report.Gini = 0
		}
		if report.Gini > 1 {
			report.Gini = 1
		}
	}

	// Top-N 集中度（N=10 或者账户总数的 10%，取较小者）
	topN := 10
	if n/10 < topN {
		topN = n / 10
	}
	if topN < 1 {
		topN = 1
	}
	var topSum int64
	for i := n - topN; i < n; i++ {
		if i >= 0 {
			topSum += values[i]
		}
	}
	if totalSupply > 0 {
		report.TopConcentrationTopN = float64(topSum) / float64(totalSupply)
	}

	// 洛伦兹曲线：等距采样 ~20 个点
	const samples = 20
	report.LorenzCurve = make([]LorenzPoint, 0, samples+1)
	report.LorenzCurve = append(report.LorenzCurve, LorenzPoint{X: 0, Y: 0})
	if totalSupply > 0 {
		var cumWealth float64
		step := math.Max(1, float64(n)/float64(samples))
		next := step
		for i, v := range values {
			cumWealth += float64(v)
			if float64(i+1) >= next {
				report.LorenzCurve = append(report.LorenzCurve, LorenzPoint{
					X: float64(i+1) / float64(n),
					Y: cumWealth / float64(totalSupply),
				})
				next += step
			}
		}
		// 强制加上 (1, 1) 端点
		if report.LorenzCurve[len(report.LorenzCurve)-1].X < 1 {
			report.LorenzCurve = append(report.LorenzCurve, LorenzPoint{X: 1, Y: 1})
		}
	}

	return report
}

// TopBalances 提取余额最高的 N 个账户（用于前端展示）。
type TopHolder struct {
	UserID  string `json:"user_id"`
	Balance int64  `json:"balance"`
}

// TopHolders 返回 Top-K 持有者（按余额降序）。
func TopHolders(snap Snapshot, k int) []TopHolder {
	type kv struct {
		k string
		v int64
	}
	pairs := make([]kv, 0, len(snap.Balances))
	for k, v := range snap.Balances {
		if v <= 0 {
			continue
		}
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })

	if k <= 0 || k > len(pairs) {
		k = len(pairs)
	}
	out := make([]TopHolder, k)
	for i := 0; i < k; i++ {
		out[i] = TopHolder{UserID: pairs[i].k, Balance: pairs[i].v}
	}
	return out
}
