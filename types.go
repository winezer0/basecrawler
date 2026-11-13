// Package assetcrawler 是一个零浏览器依赖的 Web URL 资产采集引擎。
//
// 本包只做编排，具体能力由三个子包提供，三者互不依赖：
//
//	fetcher    HTTP 抓取（连接池/限速/重试/重定向链/body 截断）
//	extractor  URL 提取，纯函数，只输出原始字符串，不碰 URL 解析
//	frontier   URL 归一化、范围判定、去重
//
// 数据流是单向的：
//
//	seed → frontier.Push → fetcher.Get → extractor.From* → frontier.Push → ...
//
// 这样拆的意义在于：想单独要「HTML 里所有 URL」的人可以只 import extractor，
// 完全不牵扯 HTTP 栈；想复用去重规则的人也只需要 frontier。
//
// 非目标（明确不做，避免范围蔓延）：
//   - 不执行 JavaScript、不渲染 DOM
//   - 不做漏洞检测、不做指纹识别
//   - 不做敏感信息提取（身份证/手机/邮箱/密钥等），这块交给 URLFinder 之类专用工具
//   - 不做目录重组 fuzz，不主动猜路径
//   - 不自动提交表单，只提取 action 与 method
package basecrawler

import (
	"net/http"
	"regexp"
	"time"

	"github.com/winezer0/basecrawler/extractor"
	"github.com/winezer0/basecrawler/fetcher"
	"github.com/winezer0/basecrawler/frontier"
)

// Scope 是采集范围，等价于 frontier.Scope。
type Scope = frontier.Scope

const (
	// ScopeSameHost 仅同 host（含端口），最严格。
	ScopeSameHost = frontier.SameHost
	// ScopeSubdomain 同根域（含任意子域），默认。
	ScopeSubdomain = frontier.Subdomain
	// ScopeAny 不限制范围（仍会去重，但可能失控，慎用）。CLI: -scope all。
	ScopeAny = frontier.Any
	// ScopeDN 域名关键字：host 含任一种子的根域字符串即命中。CLI: -scope dn。
	ScopeDN = frontier.DN
	// ScopeRegex 自定义正则：完整 URL 命中 Options.ScopeRegex 即命中。
	// CLI: -scope regex -scope-regex '...'。
	ScopeRegex = frontier.URLRegex
)

// Result 是一条采集结果。
//
// Source 标记该 URL 从何处被发现，取值如：
// "seed" / "a" / "script" / "form" / "header:location" / "js" / "robots" / "sitemap" / "redirect"。
// 这个字段对排查误报、评估提取质量很有用，不建议丢弃。
type Result struct {
	URL         string `json:"url"`
	Method      string `json:"method,omitempty"`
	Source      string `json:"source,omitempty"`
	Attribute   string `json:"attribute,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Depth       int    `json:"depth"`
	StatusCode  int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Referer     string `json:"referer,omitempty"`
	// Confidence 为空表示地址可直接访问；"template" 表示含 EXPR 占位符，
	// "interface" 表示非 GET 的接口形状（表单 action 等），"clue" 表示
	// 只报告不请求的情报线索（CSP 允许域、robots Disallow/Allow 等）。
	// template/interface 会生成测试请求；clue 与路由形状不会。
	Confidence string `json:"confidence,omitempty"`
	// Kind 标记资产类别："chunk"（打包分片）、"route"（前端路由），空为普通 URL。
	Kind string `json:"kind,omitempty"`
	// Params 是从上下文推断出的参数名（表单控件、JS 调用参数对象）。
	// 只有名字没有取值：真实取值要么在客户端生成，要么需要交互才能得到。
	Params []string `json:"params,omitempty"`
	// ParamValues 是「值可静态确定」的那部分参数：hidden 的默认值、
	// select/radio/checkbox 的候选集合。只报告候选，不合成 URL——
	// hidden 值常带 csrf/session，拼出来的地址必然失败。
	ParamValues []extractor.ParamValue `json:"param_values,omitempty"`
	// Raw 是 Kind=curl 时的原始命令全文（保真，供测试请求生成使用）。
	Raw string `json:"raw,omitempty"`
}

// Options 控制采集行为。请用 DefaultOptions() 起步，再按需覆盖字段。
type Options struct {
	// MaxDepth 最大爬取深度，种子页为 0。0 表示不限制（谨慎）。
	MaxDepth int
	// MaxURLs 最多抓取的页面数上限，防止失控。0 表示不限制。
	MaxURLs int
	// Concurrency 全局并发抓取数，默认 10。
	Concurrency int
	// PerHostConcurrency 单 host 并发上限，默认 4。对目标更友好，也降低被封概率。
	PerHostConcurrency int
	// RateLimit 全局 QPS 上限，0 表示不限速（仍受 Concurrency 约束）。
	RateLimit int
	// Timeout 单请求超时，默认 10s。
	Timeout time.Duration
	// MaxBodySize 单个响应体最大读取字节数，默认 2MB，超出部分截断（不报错）。
	MaxBodySize int64
	// Scope 爬取范围，默认 ScopeSubdomain（rdn）。
	Scope Scope
	// ScopeRegex 仅在 Scope == ScopeRegex 时生效：完整 URL 命中即入范围。
	ScopeRegex *regexp.Regexp
	// FollowRobots 是否抓取并解析 robots.txt（其中 Sitemap 会被纳入）。
	FollowRobots bool
	// FollowRobotsDisallow 是否把 robots.txt 的 Disallow 路径入队抓取，默认开启。
	// 关闭时退回旧行为：只作为 StatusCode=0 的线索输出，不请求。
	// CLI: --no-robots-disallow。
	FollowRobotsDisallow bool
	// FollowFormActions 是否把非 GET 表单 action 也入队抓一次（GET 语义），
	// 默认关闭：POST action 用 GET 抓通常只得到 404 噪音。授权场景下库调用方
	// 可打开，用于发现 action 页面背后的链接。CLI: --follow-form-actions。
	FollowFormActions bool
	// AllowUnsafeSend 是否允许发送非幂等测试请求（POST/PUT/PATCH/DELETE），
	// 默认开启；置 false 后 testfill 只发 GET/HEAD/OPTIONS。CLI: --no-unsafe-send。
	AllowUnsafeSend bool
	// FollowSitemap 是否抓取并解析 sitemap.xml（递归处理 sitemap index）。
	FollowSitemap bool
	// ExtractJS 是否对 .js / .css 及内联脚本做 endpoint 提取。
	ExtractJS bool
	// JSEngine 选择 JS 提取引擎，见 JSEngine 常量。零值等价于 JSEngineRegex。
	JSEngine JSEngine
	// ExtractFromStyle 是否从 style 属性与 <style> 块中提取 url()。
	ExtractFromStyle bool
	// ExtractJSON 是否从 JSON 响应体里提取地址（next/links 这类「下一跳」）。
	ExtractJSON bool
	// ExtractChunkGraph 是否从打包产物里恢复懒加载 chunk 与前端路由表。
	ExtractChunkGraph bool
	// RouteExec 是否允许「执行 bundle」来兜底恢复路由，默认开启。
	// 只有命中打包产物/路由签名粗筛的脚本才会真正执行，普通 JS 不跑。
	// 关闭用 CLI --no-route-exec，或库层显式置 false。
	RouteExec bool

	// RouteExpand 是否把带 :param 的前端路由展开成具体路径去试抓，默认开启。
	//
	// 每个路由用占位值（:id -> 1，*rest -> index）展开一次，由
	// MaxRouteExpand 限流。SPA 上可能返回同一个 index.html，传统站点上
	// 可能是一串 404；关闭用 CLI --no-route-expand。
	RouteExpand bool
	// MaxRouteExpand 单次采集最多展开多少条路由，默认 50。
	MaxRouteExpand int

	// CSPHints 是否把 CSP 响应头里的允许来源与上报端点作为线索输出，默认开启。
	//
	// 线索只报告不入队：`csp-source` 多为域名根路径，抓取价值低；
	// `csp-report` 常带一次性 token，抓取等于向目标注入数据。
	// 关掉用 `-no-csp`。
	CSPHints bool

	// StableHostQuota 开启同层任务的确定性排序（回归测量模式）。
	// 实测：`-max-urls 300` 饱和时能把连跑差集从 22/20 压到 0/0，
	// 但净少约 15 条站内 URL、多 5 条 404，因此默认关闭。
	// 做基线回归时显式开启，且基线必须用同一开关重采。CLI: -stable-host-order。
	StableHostQuota bool

	// NoiseFilter 丢弃 js-ast 来源的高噪音候选（node_modules/.pnpm 虚拟路径、
	// 源码扩展、:param 路由模板）。依据批次 I-1 实测：三类占 remix.run
	// 404 的 114/118；对 HTML 链接来源不生效，避免误伤。默认开。
	NoiseFilter bool
	// Deobfuscate 检测到混淆时自动做轻量反混淆（packer 解包、eval 字符串
	// 展开）后再提取，默认开启。检测见 deobf.LooksObfuscated。
	Deobfuscate bool
	// DeobfForce 自动签名检测漏报时的兜底：检测未命中也尝试解包一次，
	// 默认开启。Recover 对未混淆代码是 no-op，正常 JS 结果不变。
	// 关闭用 CLI --no-deobf（同时关掉 Deobfuscate）。
	DeobfForce bool
	// DeobfExec 允许在 goja 沙箱里执行 string-array 解码器，默认开启。
	// 只有命中 string-array 专属签名（数组 + 解码函数 + 调用点）才执行，
	// 普通 JS 与 packer/eval 类混淆不跑。关闭用 CLI --no-deobf-exec。
	DeobfExec bool
	// ExtractCurlBlocks 是否从 HTML 代码块提取 curl 命令（Kind=curl 结果，
	// 只报告不入队）。文档站点（OpenAI 参考等）的 endpoint 多以 curl 示例
	// 给出，这是「文档 → 可跑请求」链路的第一环。默认开。
	ExtractCurlBlocks bool

	// ProbeOpenAPI 是否探测目标站的 OpenAPI/Swagger spec，默认开启。
	//
	// 每个种子的 origin 试 14 个标准 spec 路径，命中则把 spec 里的
	// paths + 方法 + 参数名提取为接口形状（Source=openapi）。代价是
	// 14 次便宜请求；P-1 实测暴露率 2.4%（主流站 0%、API 子集 12.5%）。
	// 关闭用 CLI --no-probe-openapi。
	ProbeOpenAPI bool
	// FuzzPaths 开启内置字典路径 fuzz（默认开启）：对每个 origin 把字典
	// 里的路径作为 depth 0 任务探测，命中后纳入正常爬取。关闭用 --no-fuzz。
	FuzzPaths bool
	// FuzzDict 自定义字典路径列表；非空时完全替换内置字典（仍需 FuzzPaths=true）。
	FuzzDict []string

	// MaxSeen 已见集合的条数上限，默认 200000，0 表示不限制。
	//
	// 已见集合常驻内存且不会缩小，是超大站点上唯一没有上限的增长点。
	// 超出后新 URL 被拒绝并计数，采集结束时通过 OnError 报一次，
	// 避免「结果看起来完整、其实被截断」。
	MaxSeen int
	// MaxPagesPerHost 单 host 最多抓取的 HTML 页面数，0 表示不限制。
	// 防爬虫陷阱：大站单域死循环跑飞是真实故障模式，全局 -max-urls
	// 之外再按 host 封顶。只统计成功（2xx）的 text/html GET 页面，
	// 静态资产与线索类结果不计入。CLI: -mdp。
	MaxPagesPerHost int
	// Insecure 跳过 TLS 证书校验。
	Insecure bool
	// Proxy 形如 http://127.0.0.1:8080 或 socks5://127.0.0.1:1080。空表示直连。
	Proxy string
	// UserAgent 自定义 UA，空则用默认值。
	UserAgent string
	// Headers 额外请求头（如 Cookie、Authorization），会被附加到每个请求。
	Headers http.Header
	// Retries 失败重试次数，默认 1。仅对传输层错误与 5xx 重试。
	Retries int
	// MaxRedirects 手动跟随重定向的最大跳数，默认 5。
	// NoRedirects 为 true 时不跟随重定向：首个 3xx 直接返回（Location
	// 仍记录在 Redirects 里，重定向目标本身是资产）。CLI: -dr。
	NoRedirects  bool
	MaxRedirects int
	// InsidePath 只爬种子路径之下（hakrawler -i 语义）：种子是
	// https://example.com/en-US/ 时，/en-US/ 之外的同站路径不入队。
	// 种子在站点根（/）时等于无限制。范围仍先受 Scope 约束。
	// CLI: -i。
	InsidePath bool
	// ExcludeRegex 命中则丢弃该 URL（匹配完整 URL）。
	ExcludeRegex []*regexp.Regexp
	// IncludeRegex 非空时，仅保留命中的 URL。
	IncludeRegex []*regexp.Regexp
	// OnResult 每抓取完一个 URL 回调一次。库内部已串行化，
	// 调用方无需自行加锁即可直接写文件或 channel。
	OnResult func(Result)
	// OnError 错误回调。与 OnResult 一样由库串行化调用。
	OnError func(url string, err error)
}

// DefaultOptions 返回一套对生产环境友好的默认值。
func DefaultOptions() *Options {
	return &Options{
		MaxDepth:             3,
		MaxURLs:              2000,
		Concurrency:          10,
		PerHostConcurrency:   4,
		Timeout:              10 * time.Second,
		MaxBodySize:          2 << 20,
		Scope:                ScopeSubdomain,
		FollowRobots:         true,
		FollowRobotsDisallow: true,
		FollowFormActions:    false,
		AllowUnsafeSend:      true,
		FollowSitemap:        true,
		ExtractJS:            true,
		JSEngine:             JSEngineAuto,
		ExtractFromStyle:     true,
		ExtractJSON:          true,
		ExtractChunkGraph:    true,
		RouteExec:            true,
		RouteExpand:          true,
		MaxRouteExpand:       50,
		MaxSeen:              200000,
		CSPHints:             true,
		StableHostQuota:      false,
		NoiseFilter:          true,
		ExtractCurlBlocks:    true,
		Deobfuscate:          true,
		DeobfForce:           true,
		DeobfExec:            true,
		ProbeOpenAPI:         true,
		FuzzPaths:            true,
		Retries:              1,
		MaxRedirects:         5,
	}
}

// normalize 兜底非法值，避免调用方传 0/负数导致死锁或除零。
func (o *Options) normalize() {
	if o.Concurrency <= 0 {
		o.Concurrency = 10
	}
	if o.PerHostConcurrency <= 0 {
		o.PerHostConcurrency = 4
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.MaxBodySize <= 0 {
		o.MaxBodySize = 2 << 20
	}
	if o.MaxRedirects <= 0 {
		o.MaxRedirects = 5
	}
	if o.MaxDepth < 0 {
		o.MaxDepth = 0
	}
}

// fetcherOptions 把编排层配置映射成抓取层配置。
// 各子包的默认值由子包自己兜底，这里只做字段搬运，不做二次判断。
func (o *Options) fetcherOptions() fetcher.Options {
	return fetcher.Options{
		Timeout:            o.Timeout,
		MaxBodySize:        o.MaxBodySize,
		MaxRedirects:       o.MaxRedirects,
		NoRedirects:        o.NoRedirects,
		Retries:            o.Retries,
		Insecure:           o.Insecure,
		Proxy:              o.Proxy,
		UserAgent:          o.UserAgent,
		Headers:            o.Headers,
		PerHostConcurrency: o.PerHostConcurrency,
		QPS:                o.RateLimit,
	}
}

// frontierOptions 把编排层配置映射成范围/过滤配置。
func (o *Options) frontierOptions() frontier.Options {
	return frontier.Options{
		Scope:      o.Scope,
		ScopeRegex: o.ScopeRegex,
		InsidePath: o.InsidePath,
		Include:    o.IncludeRegex,
		Exclude:    o.ExcludeRegex,
		MaxSeen:    o.MaxSeen,
	}
}

// extractorOptions 把编排层配置映射成提取配置。
func (o *Options) extractorOptions(limits *extractor.LimitReport) extractor.Options {
	return extractor.Options{
		ExtractJS:    o.ExtractJS,
		ExtractStyle: o.ExtractFromStyle,
		JSExtract:    adaptEngine(engineChain(o.JSEngine)),
		Deobfuscate:  o.Deobfuscate,
		DeobfForce:   o.DeobfForce,
		DeobfExec:    o.DeobfExec,
		Limits:       limits,
	}
}
