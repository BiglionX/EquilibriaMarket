// Package openrtb 定义 OpenRTB（Open Real-Time Bidding）2.5 协议的精简数据结构。
//
// OpenRTB 是 IAB 制定的实时竞价标准协议，Prebid Server、头部竞价服务器、SSP/DSP
// 都遵循此规范。本包定义核心数据结构，使 EquilibriaMarket 能与外部广告生态互联互通。
//
// 设计取舍：
//   - 只实现 Phase 1 必需的字段，标注 "省略字段" 的部分按需扩展。
//   - JSON tag 严格对齐 OpenRTB 官方 spec，确保与 Prebid Server 互通。
//   - 数值类型用指针（*float64 等）以区分"未传"与"传 0"。
//
// 参考：https://iabtechlab.com/standards/openrtb/
package openrtb

// ===== 请求侧（BidRequest）=====

// BidRequest OpenRTB 顶层请求。
//
// 一个 BidRequest 可包含多个 imp（广告位），每个 imp 独立拍卖后合并为 SeatBid[] 响应。
type BidRequest struct {
	ID     string   `json:"id"`               // 请求唯一ID
	Imp    []Imp    `json:"imp"`              // 广告位列表（至少 1 个）
	Site   *Site    `json:"site,omitempty"`   // 站点信息（与 App 二选一）
	App    *App     `json:"app,omitempty"`    // 应用信息（与 Site 二选一）
	User   *User    `json:"user,omitempty"`   // 用户信息
	Device *Device  `json:"device,omitempty"` // 设备信息
	TMax   int      `json:"tmax,omitempty"`   // 超时（毫秒）
	At     int      `json:"at,omitempty"`     // 拍卖类型：1=第一价 2=第二价（Vickrey）
	Currency []string `json:"cur,omitempty"`   // 接受的货币列表
	Test   int      `json:"test,omitempty"`   // 1=测试模式

	// 省略字段：regs、bcat、badv、source、wseat、allimps 等
}

// Imp 单个广告位（impression）。
//
// 每个 imp 是一次拍卖机会；adapter 会为每个 imp 生成一个引擎 BidRequest。
type Imp struct {
	ID         string   `json:"id"`                   // 广告位唯一ID
	Banner     *Banner  `json:"banner,omitempty"`     // 横幅广告规格
	Video      *Video   `json:"video,omitempty"`      // 视频广告规格
	Native     *Native  `json:"native,omitempty"`     // 原生广告规格
	BidFloor   float64  `json:"bidfloor,omitempty"`    // 底价（注意力币）
	BidFloorCur string  `json:"bidfloorcur,omitempty"` // 底价货币（默认 USD）
	Secure     int      `json:"secure,omitempty"`      // 1=HTTPS
	Instl      int      `json:"instl,omitempty"`       // 1=全屏/插屏
	TagID      string   `json:"tagid,omitempty"`       // 广告位标签
	Ext        any      `json:"ext,omitempty"`         // 扩展字段（Prebid 常用）
}

// Banner 横幅广告规格。
type Banner struct {
	W        int      `json:"w,omitempty"`        // 宽度（像素）
	H        int      `json:"h,omitempty"`        // 高度（像素）
	Pos      int      `json:"pos,omitempty"`      // 广告位置（0=未知，1=顶部）
	API      []int    `json:"api,omitempty"`     // 支持的 API
	MIMEs    []string `json:"mimes,omitempty"`   // 接受的 MIME 类型
	BAttr    []int    `json:"battr,omitempty"`    // 屏蔽的创意属性
}

// Video 视频广告规格。
type Video struct {
	W           int      `json:"w,omitempty"`
	H           int      `json:"h,omitempty"`
	MIMEs       []string `json:"mimes,omitempty"`
	MinDuration int      `json:"minduration,omitempty"`
	MaxDuration int      `json:"maxduration,omitempty"`
	Protocols   []int    `json:"protocols,omitempty"` // VAST 协议版本
	StartDelay  int      `json:"startdelay,omitempty"`
	Linearity   int      `json:"linearity,omitempty"`  // 1=线性 2=非线性
}

// Native 原生广告规格（精简）。
type Native struct {
	Request string `json:"request,omitempty"` // 原生请求 JSON
	Ver     string `json:"ver,omitempty"`
}

// Site 站点信息（网页广告）。
type Site struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain,omitempty"`
	Cat    []string `json:"cat,omitempty"`
	Page   string `json:"page,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Publisher *Publisher `json:"publisher,omitempty"`
}

// App 应用信息（移动/CTV 广告）。
type App struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Bundle   string `json:"bundle,omitempty"`
	StoreURL string `json:"storeurl,omitempty"`
	Publisher *Publisher `json:"publisher,omitempty"`
}

// Publisher 媒体方（出版商）。
type Publisher struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// User 用户信息（隐私敏感，遵守 GDPR/CCPA）。
type User struct {
	ID         string         `json:"id,omitempty"`         // 平台用户ID
	Yob        int            `json:"yob,omitempty"`        // 出生年
	Gender     string         `json:"gender,omitempty"`     // M/F/O
	Keywords   string         `json:"keywords,omitempty"`   // 关键词（逗号分隔）
	CustomData string         `json:"customdata,omitempty"` // 自定义数据
	Geo        *Geo           `json:"geo,omitempty"`        // 地理位置
	Data       []Data         `json:"data,omitempty"`       // 用户数据段
	Consent    string         `json:"consent,omitempty"`    // GDPR consent 字符串
	Ext        map[string]any `json:"ext,omitempty"`        // 扩展（如 Prebid user IDs）
}

// Geo 地理位置（精度刻意降低以保护隐私）。
type Geo struct {
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	Country string  `json:"country,omitempty"`
	Region  string  `json:"region,omitempty"`
	City    string  `json:"city,omitempty"`
	ZIP     string  `json:"zip,omitempty"`
	Metro   string  `json:"metro,omitempty"`
	Type    int     `json:"type,omitempty"` // 1=GPS 2=IP
	UTCOffset int   `json:"utcoffset,omitempty"`
}

// Data 用户数据段（来自数据提供商）。
type Data struct {
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name,omitempty"`
	Segment []Segment `json:"segment,omitempty"`
}

// Segment 用户分群标签。
type Segment struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// Device 设备信息。
type Device struct {
	UA             string  `json:"ua,omitempty"`             // User-Agent
	Geo            *Geo    `json:"geo,omitempty"`            // 设备地理位置
	DNT            int     `json:"dnt,omitempty"`            // 1=Do Not Track
	Lmt            int     `json:"lmt,omitempty"`            // 1=限制广告追踪
	IP             string  `json:"ip,omitempty"`             // IP 地址
	DeviceType     int     `json:"devicetype,omitempty"`     // 1=手机 2=平板 3=PC 4=TV
	Make           string  `json:"make,omitempty"`
	Model          string  `json:"model,omitempty"`
	OS             string  `json:"os,omitempty"`
	OSV            string  `json:"osv,omitempty"`
	HWV            string  `json:"hwv,omitempty"`
	H              int     `json:"h,omitempty"`               // 屏幕高
	W              int     `json:"w,omitempty"`               // 屏幕宽
	PPI            int     `json:"ppi,omitempty"`
	PxRatio        float64 `json:"pxratio,omitempty"`
	JS             int     `json:"js,omitempty"`              // 1=支持 JS
	Language       string  `json:"language,omitempty"`
	IFA            string  `json:"ifa,omitempty"`             // 移动广告ID (IDFA/GAID)
	Carrier        string  `json:"carrier,omitempty"`
	MCCMNC         string  `json:"mccmnc,omitempty"`
	ConnectionType int     `json:"connectiontype,omitempty"`
}

// ===== 响应侧（BidResponse）=====

// BidResponse OpenRTB 顶层响应。
type BidResponse struct {
	ID      string    `json:"id"`               // 对应请求 ID
	SeatBid []SeatBid `json:"seatbid"`          // 出价方响应列表
	BidID   string    `json:"bidid,omitempty"`   // // 服务端生成的 bid ID
	Cur     string    `json:"cur,omitempty"`    // 货币（默认 USD）
	NoBid   int       `json:"nobid,omitempty"`  // 1=无出价（兜底）

	// 省略字段：ext
}

// SeatBid 单一出价方（seat）的所有出价。
//
// Prebid 中"seat"通常是 bidder 名称（如 "rubicon"、"appnexus"）。
// 我们的引擎是单一 seat，所以通常只返回 1 个 SeatBid。
type SeatBid struct {
	Bid  []Bid `json:"bid"`           // 出价列表（每个对应一个 imp）
	Seat string `json:"seat,omitempty"` // 出价方标识
	Group int   `json:"group,omitempty"` // 1=要求胜出者必须展示同 seat 的所有 bid

	// 省略字段：ext
}

// Bid 单次出价。
type Bid struct {
	ID    string  `json:"id"`              // 唯一 bid ID
	ImpID string  `json:"impid"`           // 对应的 imp ID
	Price float64 `json:"price"`           // 清算价格（CPM）
	AdM   string  `json:"adm,omitempty"`   // 广告素材（HTML/VAST）
	NURL  string  `json:"nurl,omitempty"`   // 胜出通知 URL（用于审计）
	IURL  string  `json:"iurl,omitempty"`   // 图片 URL（用于内容审核）
	ADomain []string `json:"adomain,omitempty"` // 广告主域名
	Cat   []string `json:"cat,omitempty"`   // IAB 内容分类
	Attr  []int    `json:"attr,omitempty"`  // 创意属性
	API   int      `json:"api,omitempty"`   // 框架（1=VPAID 2=MRAID）
	Protocol int   `json:"protocol,omitempty"` // VAST 版本
	QAGMediaRating int `json:"qagmediarating,omitempty"`
	CID   string   `json:"cid,omitempty"`   // 活动 ID
	CRID  string   `json:"crid,omitempty"`  // 创意 ID
	W     int      `json:"w,omitempty"`     // 广告宽
	H     int      `json:"h,omitempty"`     // 广告高
	WMin  int      `json:"wmin,omitempty"`
	HMin  int      `json:"hmin,omitempty"`
	DealID string  `json:"dealid,omitempty"` // 私有交易 ID（PMP）
	Exp    int     `json:"exp,omitempty"`     // 过期时间（秒）
}

// ===== 错误响应（OpenRTB 5.3 节）=====

// NoBidReason 标识无出价原因（用于调试）。
type NoBidReason int

const (
	NoBidUnknown      NoBidReason = 0
	NoBidTechnical    NoBidReason = 1
	NoBidInvalidReq   NoBidReason = 2
	NoBidKnownWebSpider NoBidReason = 3
	NoBidNonHuman     NoBidReason = 4
	NoBidProxyIP      NoBidReason = 5
	NoBidUnsupported  NoBidReason = 6
	NoBidBlocked      NoBidReason = 7
	NoBidUnreachable  NoBidReason = 8
)