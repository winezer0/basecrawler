// compat.go —— 拆包后的兼容层。
//
// extractor 原是单包；现在每类解析器独立成 extractor/<类型> 子包，
// 共享类型在 extractor/base。但外部调用方（crawler / testfill / jsext /
// cmd）此前 import 的是根包，为保持 `extractor.XXX` 的引用面零改动，
// 这里用类型别名 + 函数值转发把旧符号全部接住。
//
// 规则：
//   - 类型用 alias（Request = base.Request），两侧是同一个类型，可互赋值；
//   - 纯函数用 var 转发（FromSourceMap = sourcemap.FromSourceMap），
//     调用语法与拆包前完全一致；
//   - 新代码建议直接 import 对应子包，compat 层只为存量调用方保留。
package extractor

import (
	"github.com/winezer0/basecrawler/extractor/base"
	"github.com/winezer0/basecrawler/extractor/curl"
	"github.com/winezer0/basecrawler/extractor/inlinejson"
	"github.com/winezer0/basecrawler/extractor/json"
	"github.com/winezer0/basecrawler/extractor/modulemap"
	"github.com/winezer0/basecrawler/extractor/nextjs"
	"github.com/winezer0/basecrawler/extractor/openapi"
	"github.com/winezer0/basecrawler/extractor/sourcemap"
)

// ---- 共享类型（原定义：extractor.go / formvalues.go / limits.go） ----

type (
	// Request 一个 URL 候选，Raw 可能是相对路径。
	Request = base.Request
	// ParamValue 值可静态确定的表单参数。
	ParamValue = base.ParamValue
	// LimitKind 截断统计的来源标识。
	LimitKind = base.LimitKind
	// LimitEvent 一次截断统计。
	LimitEvent = base.LimitEvent
	// LimitReport 按 kind 聚合的截断统计器，零值可用、nil 安全。
	LimitReport = base.LimitReport
)

// CurlCommand 是解析出的一条 curl 命令。
type CurlCommand = curl.CurlCommand

// 截断统计的来源常量。
const (
	LimitInlineJSON      = base.LimitInlineJSON
	LimitNextFlight      = base.LimitNextFlight
	LimitSourceMap       = base.LimitSourceMap
	LimitFormParams      = base.LimitFormParams
	LimitRobotsWildcards = base.LimitRobotsWildcards
)

// ---- sourcemap：.map 文件识别与 sources 归一化 ----

var (
	FromSourceMap           = sourcemap.FromSourceMap
	FromSourceMapWithLimits = sourcemap.FromSourceMapWithLimits
	LooksLikeSourceMap      = sourcemap.LooksLikeSourceMap
)

// ---- modulemap：importmap 与 sourceMappingURL 声明 ----

var SourceMapRequests = modulemap.SourceMapRequests

// ---- openapi：OpenAPI/Swagger 文档探测与解析 ----

var (
	FromOpenAPI       = openapi.FromOpenAPI
	LooksLikeOpenAPI  = openapi.LooksLikeOpenAPI
	OpenAPIProbePaths = openapi.OpenAPIProbePaths
)

// ---- nextjs：__NEXT_FLIGHT_DATA__ 内联数据 ----

var (
	FromNextFlight      = nextjs.FromNextFlight
	LooksLikeNextFlight = nextjs.LooksLikeNextFlight
)

// ---- inlinejson：script 标签里的内联 JSON（state/config） ----

var FromInlineJSON = inlinejson.FromInlineJSON

// ---- json：JSON 响应体与分页 ----

var (
	FromJSON           = json.FromJSON
	FromJSONPagination = json.FromJSONPagination
)

// ---- curl：curl 命令解析 ----

var (
	ParseCurl     = curl.ParseCurl
	CurlCommands  = curl.CurlCommands
	LooksLikeCurl = curl.LooksLikeCurl
)
