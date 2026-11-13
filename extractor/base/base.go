// Package base 是 extractor 各子解析器共用的最小类型集。
//
// 拆包背景：extractor 原是单包 16 类解析器，共用 Request / ParamValue /
// LimitReport 三个类型与截断统计机制。为了「每类额外解析一个子包」，
// 共享类型独立成 base，避免子包互相依赖或反向依赖根包。
//
// 设计约束不变：
//   - 纯类型与纯统计，无 IO、无并发解析；
//   - Request 只承载「原始字符串」，不做归一化、不做去重（归一化属 frontier）。
package base

import "net/http"

// Request 是一个提取到的 URL 候选。Raw 可能是相对路径，需调用方结合上下文解析。
type Request struct {
	Raw       string
	Method    string
	Source    string // 来源分类：标签名 / header:xxx / js / style 等
	Attribute string
	Tag       string
	// Confidence 为空表示地址可确定；"template" 表示含 EXPR 占位符，不可直接访问。
	Confidence string
	// Kind 标记来源类别，目前用于区分 "chunk"（打包产物分片）与 "route"（前端路由）。
	// 为空表示普通提取结果。
	Kind string
	// Params 是从上下文推断出的参数名（表单控件、JS 调用参数对象）。
	// 只有名字没有取值——取不到真实值，编造一个反而会污染结果。
	Params []string
	// ParamValues 是「值可静态确定」的那部分参数：hidden 的默认值、
	// select/radio/checkbox 的候选集合。只报告候选，不合成 URL。
	ParamValues []ParamValue
}

// NewRequest 是 Request 的语法糖：多数解析器只填 Raw / Method / Source 三件套。
func NewRequest(raw, source string) Request {
	return Request{Raw: raw, Method: http.MethodGet, Source: source}
}

// ParamValue 是「值可静态确定」的表单参数：名字 + 候选取值 + 取值来源。
type ParamValue struct {
	// Name 参数名。
	Name string `json:"name"`
	// Values 候选取值，按 HTML 中出现顺序、已去重。
	Values []string `json:"values"`
	// Kind 取值来源：hidden / select / radio / checkbox /
	// input:<type>（文本类控件，P-2 新增，无静态候选时 Values 为空）。
	Kind string `json:"kind,omitempty"`
	// Type 是 input 控件的 type 属性归一化值（P-2 新增：
	// text/email/password/number/date/tel/url/search 等）。
	// testfill 按它选值形态；静态候选来源（hidden/select 等）留空。
	Type string `json:"type,omitempty"`
}
