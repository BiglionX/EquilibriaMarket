// prebid-mock 启动一个模拟 Prebid Server，集成 EquilibriaMarket 引擎。
//
// 用途：
//   - 演示完整的 OpenRTB 接入链路。
//   - 让前端/客户端开发可以本地调用一个"看起来像 Prebid"的端点。
//   - 集成测试：与外部 Prebid Server 互通前的本地验证。
//
// 启动：
//   go run ./cmd/prebid-mock -addr :8090
//
// 测试：
//   curl -X POST http://localhost:8090/openrtb2/auction -H "Content-Type: application/json" -d @req.json
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"market-engine/pkg/engine"
	"market-engine/pkg/governance"
	_ "market-engine/pkg/governance/centralized"
	"market-engine/pkg/openrtb"
	"market-engine/adapters/prebid"
)

func main() {
	var (
		addr          = flag.String("addr", ":8090", "Prebid mock server 监听地址")
		inflationRate = flag.Float64("inflation", 1.0, "货币通胀率（演示用，建议 1.0）")
		taxRate       = flag.Float64("tax", 0.001, "平台抽税比例")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// 加载治理插件
	plugin, err := governance.Load("centralized", map[string]any{
		"admin_address":  "0xMockAdmin0000000000000000000000000000",
		"inflation_rate": *inflationRate,
		"tax_rate":       *taxRate,
	})
	if err != nil {
		logger.Error("load plugin failed", "err", err)
		os.Exit(1)
	}

	// 构造做市商
	mm := engine.NewMarketMaker(
		engine.AuctioneerFunc(engine.SecondPriceAuction),
		plugin,
		engine.WithTaxRate(*taxRate),
		engine.WithLogger(logger),
	)

	// 构造 adapter（带 3 个 mock bidder）
	adapter := prebid.NewAdapter(mm,
		prebid.WithBidders(
			prebid.NewMockBidder("merchant-premium", 12.0),
			prebid.NewMockBidder("merchant-standard", 7.0),
			prebid.NewMockBidder("creator-livestream", 4.5),
		),
		prebid.WithLogger(logger),
	)

	// 预存一些创作者余额，方便测试
	seedBalances(plugin, mm, logger)

	// 启动 mock server
	srv := prebid.NewMockPrebidServer(adapter, *addr)
	logger.Info("prebid mock starting", "addr", *addr, "openrtb_endpoint", "POST "+*addr+"/openrtb2/auction")

	idleClosed := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("shutdown error", "err", err)
		}
		close(idleClosed)
	}()

	if err := srv.Start(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
	<-idleClosed
}

// seedBalances 通过 MintCurrency 给一些用户注入余额，便于端到端测试。
func seedBalances(plugin governance.Plugin, mm *engine.MarketMaker, logger *slog.Logger) {
	ctx := context.Background()
	users := map[string]int64{
		"merchant-premium":  100000,
		"merchant-standard": 100000,
		"creator-livestream": 100000,
		"creator-premium":    50000,
		"creator-standard":   50000,
	}
	for user, amount := range users {
		resp, err := plugin.MintCurrency(ctx, governance.MintRequest{
			UserID:    user,
			Amount:    amount,
			Reason:    "seed",
			Timestamp: time.Now().Unix(),
		})
		if err != nil {
			logger.Warn("seed mint failed", "user", user, "err", err)
			continue
		}
		if resp.Accepted {
			logger.Info("seed minted", "user", user, "amount", resp.NewBalance)
		}
	}

	// 注册内容-创作者映射（基于 mock bidder 的命名规则）
	// 注意：mock bidder 生成的 ContentID 是 "creative-{bidder}-{imp}"
	contentMap := map[string]string{
		"creative-merchant-premium-imp-banner-home-1":  "creator-premium",
		"creative-merchant-standard-imp-banner-home-1": "creator-standard",
		"creative-creator-livestream-imp-banner-home-1": "creator-livestream",
	}
	for content, creator := range contentMap {
		mm.RegisterContent(content, creator)
	}
}

// 参考 openrtb 包（保持 import）。
var _ = openrtb.BidRequest{}