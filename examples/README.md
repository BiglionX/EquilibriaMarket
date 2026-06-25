# EquilibriaMarket 集成示例

## 🎯 run-auction — 5 分钟集成

**最简示例**：不写一行 HTTP / API，纯 Go 库方式调用引擎跑一次 Vickrey 第二价格拍卖 + 三段式结算 + 治理插件记账 + 基尼系数监控。

### 跑起来

```bash
go run ./examples/run-auction
```

### 预期输出

```
============================================================
  5 分钟集成示例：Vickrey 第二价格拍卖
============================================================

[A] 撮合结果
    胜出者     : DSP-Alpha
    清算价     : 4.21 (第二高价 + 0.01，激励相容)
    广告位     : slot-homepage-banner

[B] 结算明细
    胜出者实付  : 4.21
    创作者分账  : 4.21 → creator-zoe
    系统抽税    : 0.00 (燃烧)
    胜出者余额  : 4579
    创作者余额  : 421

[C] 治理插件账本快照
    账户数     : 6

[D] 基尼系数（贫富差距监控）
    Gini       = 0.326
    总账户     = 6
    总发行量   = 20000
    Top-1 占比 = 25.0%
============================================================
  ✅ 完成！整个拍卖 + 结算 + 指标 ~50 行 Go 代码
  📦 真实部署再加 cmd/marketd（HTTP 端点已实现）即可服务化
============================================================
```

### 关键代码片段

```go
// 1) 选治理插件（已有 centralized；想换自己的实现 Plugin 接口即可）
plugin := centralized.New(centralized.DefaultConfig())

// 2) 创建做市商（用一行 AuctioneerFunc 适配器包住 SecondPriceAuction）
mm := engine.NewMarketMaker(
    engine.AuctioneerFunc(engine.SecondPriceAuction), // 第二价格密封拍卖
    plugin,
)

// 3) 登记内容 → 创作者映射（结算时按此分账）
mm.RegisterContent("ad-001", "creator-zoe")

// 4) 准备一次广告竞价
req := types.BidRequest{SlotID: "slot-homepage-banner", FloorPrice: 1.0}
bids := []types.BidResponse{
    {BidderID: "DSP-Alpha", ContentID: "ad-001", Bid: 5.00},
    {BidderID: "DSP-Beta", ContentID: "ad-001", Bid: 3.00},
    {BidderID: "DSP-Gamma", ContentID: "ad-001", Bid: 4.20},
}

// 5) 跑！Validate → Auction → Settle 一气呵成
result, settle, err := mm.HandleBidRequest(ctx, req, bids)
```

### 我能直接用什么？我要写什么？

| 直接可用（零代码） | 需要适配（业务相关） |
|---|---|
| `pkg/types` — BidRequest/BidResponse/AuctionResult | 真实流量源对接（改 cmd/marketd 路由） |
| `pkg/engine` — Vickrey 拍卖 + MarketMaker | 自定义治理规则（实现 Plugin 接口） |
| `pkg/governance/centralized` — 中心化记账 | 自定义"注意力币"（改 AttentionCurrency） |
| `pkg/metrics` — 基尼系数/洛伦兹曲线 | 持久层（现默认 in-memory） |
| `cmd/marketd` — OpenRTB 风格 HTTP 端点 | 认证 / 鉴权（加 middleware） |
| `cmd/admin-console` — Web UI | UI 定制（修改 index.html / app.js） |
| `adapters/prebid` — Prebid Server 集成 | — |

### 集成步骤

1. `go get github.com/BiglionX/EquilibriaMarket/pkg/...`
2. import `pkg/engine` + `pkg/governance/centralized` + `pkg/types`
3. 构造 `MarketMaker` 实例
4. 调用 `mm.HandleBidRequest(ctx, req, bids)`
5. （可选）挂到自己的 HTTP handler / gRPC service

**完整代码**：见 [main.go](./run-auction/main.go)
