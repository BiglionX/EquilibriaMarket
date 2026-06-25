// Package types 定义引擎核心数据结构。
//
// 这些类型是引擎、市场方和治理插件之间通信的"通用语"。
// 设计原则：保持结构体精简、序列化友好、可演进。
package types

import "time"

// AttentionCurrency 注意力货币：用户持有、用于支付内容消费。
//
// "货币"是稀缺性信号的工程实现：通过余额和过期时间来表达"注意力价值的时间衰减"。
type AttentionCurrency struct {
	UserID    string    `json:"user_id"`    // 持有者ID
	Balance   int64     `json:"balance"`    // 余额（最小单位：分；1 单位 = 0.01 注意力币）
	ExpiresAt time.Time `json:"expires_at"` // 过期时间（防囤积、促进流通）
}

// BidRequest 竞价请求：描述一次"流量位机会"。
//
// 引擎收到该请求后，会向所有接入的出价方广播，收集 BidResponse。
type BidRequest struct {
	SlotID    string            `json:"slot_id"`    // 流量位唯一ID
	UserTags  map[string]string `json:"user_tags"`  // 用户画像（兴趣/地域/设备等）
	FloorPrice float64           `json:"floor_price"` // 市场底价（供需价格发现器动态调整）
	Timestamp time.Time         `json:"timestamp"`  // 请求时间
}

// BidResponse 出价响应：来自商家或达人的报价。
//
// 多个 BidResponse 汇聚到一个流量位，由 Auctioneer 决定胜出者。
type BidResponse struct {
	BidderID  string  `json:"bidder_id"`  // 出价方ID（商家/达人）
	Bid       float64 `json:"bid"`        // 出价金额（注意力币）
	ContentID string  `json:"content_id"` // 待展示的广告/内容ID
}

// AuctionResult 拍卖结果：维克瑞拍卖（第二价格密封）的输出。
//
// 胜出者支付"第二高价 + 0.01"，激励诚实报价。
type AuctionResult struct {
	WinnerID   string  `json:"winner_id"`   // 胜出者ID
	ContentID  string  `json:"content_id"`  // 胜出内容ID
	ClearPrice float64 `json:"clear_price"` // 成交价格（第二高价 + 0.01）
	SlotID     string  `json:"slot_id"`     // 对应的流量位
}