// Command run-auction 是 EquilibriaMarket 的 5 分钟集成示例。
//
// 它演示了：如何不写一行 HTTP / API，纯 Go 库方式调用引擎跑一次
// Vickrey 第二价格拍卖 + 三段式结算 + 治理插件记账。
//
// 跑这个例子：
//
//	go run ./examples/run-auction
//
// 你会看到：
//   1. 治理插件初始化（centralized）
//   2. 一次广告拍卖（A/B/C 三个 DSP 出价，胜出者付第二高价）
//   3. 治理插件的余额变化（中标者扣款、创作者到账、系统抽税）
//   4. 跑 3 轮后基尼系数下降（再分配效果可视化）
//
// 零 API / 零 HTTP / 零 JSON — 这就是 Phase 1 MVP 的核心能力。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"market-engine/pkg/engine"
	"market-engine/pkg/governance"
	"market-engine/pkg/governance/centralized"
	"market-engine/pkg/metrics"
	"market-engine/pkg/types"
)

func main() {
	logger := slog.Default()
	ctx := context.Background()

	// ============================================================
	// 1) 选治理插件（已有 centralized；想换自己的实现 Plugin 接口即可）
	// ============================================================
	plugin := centralized.New(centralized.DefaultConfig())
	logger.Info("plugin ready", "name", centralized.PluginName, "inflation", 0.05)

	// ============================================================
	// 2) 创建做市商（用一行 AuctioneerFunc 适配器包住 SecondPriceAuction）
	// ============================================================
	mm := engine.NewMarketMaker(
		engine.AuctioneerFunc(engine.SecondPriceAuction), // 第二价格密封拍卖
		plugin,
	)

	// ============================================================
	// 3) 登记内容 → 创作者映射（结算时按此分账）
	// ============================================================
	mm.RegisterContent("ad-001", "creator-zoe")

	// ============================================================
	// 4) 给中标方预铸一些币（让它有钱付清算价）
	//    注：centralized 插件按 InflationRate 实际到账（5%），所以需要
	//    请求 100000 才能拿到 5000 余额，覆盖 421 单位的清算价。
	// ============================================================
	for _, id := range []string{"DSP-Alpha", "DSP-Beta", "DSP-Gamma", "small-user"} {
		plugin.MintCurrency(ctx, governance.MintRequest{
			UserID: id,
			Amount: 100_000, // 实际到账 = 100_000 * 0.05 = 5_000
			Reason: "demo grant",
		})
	}

	// ============================================================
	// 5) 准备一次广告竞价（3 个 DSP 出价）
	// ============================================================
	req := types.BidRequest{
		SlotID:     "slot-homepage-banner",
		FloorPrice: 1.0,
		Timestamp:  time.Now(),
	}
	// HandleBidRequest 第二个参数是 []BidResponse 数组
	bids := []types.BidResponse{
		{BidderID: "DSP-Alpha", ContentID: "ad-001", Bid: 5.00},
		{BidderID: "DSP-Beta", ContentID: "ad-001", Bid: 3.00},
		{BidderID: "DSP-Gamma", ContentID: "ad-001", Bid: 4.20},
	}

	// ============================================================
	// 6) 跑！Validate → Auction → Settle 一气呵成
	// ============================================================
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("  5 分钟集成示例：Vickrey 第二价格拍卖")
	fmt.Println(strings.Repeat("=", 60))

	result, settle, err := mm.HandleBidRequest(ctx, req, bids)
	if err != nil {
		logger.Error("auction failed", "err", err)
		return
	}

	fmt.Printf("\n[A] 撮合结果\n")
	fmt.Printf("    胜出者     : %s\n", result.WinnerID)
	fmt.Printf("    清算价     : %.2f (第二高价 + 0.01，激励相容)\n", result.ClearPrice)
	fmt.Printf("    广告位     : %s\n", result.SlotID)

	if settle != nil {
		fmt.Printf("\n[B] 结算明细\n")
		fmt.Printf("    胜出者实付  : %.2f\n", settle.SponsorPaid)
		fmt.Printf("    创作者分账  : %.2f → creator-zoe\n", settle.CreatorPayout)
		fmt.Printf("    系统抽税    : %.2f (燃烧)\n", settle.Tax)
		fmt.Printf("    胜出者余额  : %d\n", settle.SponsorBalance)
		fmt.Printf("    创作者余额  : %d\n", settle.CreatorBalance)
	}

	// ============================================================
	// 7) 治理插件内部账本快照
	// ============================================================
	fmt.Printf("\n[C] 治理插件账本快照\n")
	snapshot := plugin.Snapshot()
	fmt.Printf("    账户数     : %d\n", len(snapshot))
	for _, id := range []string{"DSP-Alpha", "DSP-Beta", "DSP-Gamma", "creator-zoe", "small-user"} {
		if v, ok := snapshot[id]; ok {
			fmt.Printf("      %-15s = %d\n", id, v)
		}
	}

	// ============================================================
	// 8) 基尼系数（衡量贫富差距；越低越平等）
	// ============================================================
	fmt.Printf("\n[D] 基尼系数（贫富差距监控）\n")
	report := metrics.Compute(metrics.Snapshot{Balances: snapshot, Timestamp: time.Now()})
	fmt.Printf("    Gini       = %.3f  (0=绝对平等 / 1=极端不平等)\n", report.Gini)
	fmt.Printf("    总账户     = %d\n", report.TotalAccounts)
	fmt.Printf("    总发行量   = %d\n", report.TotalSupply)
	fmt.Printf("    Top-1 占比 = %.1f%%\n", report.TopConcentrationTopN*100)

	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("  ✅ 完成！整个拍卖 + 结算 + 指标 ~50 行 Go 代码")
	fmt.Println("  📦 真实部署再加 cmd/marketd（HTTP 端点已实现）即可服务化")
	fmt.Println(strings.Repeat("=", 60))
}
