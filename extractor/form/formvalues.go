package form

import (
	"strings"

	"github.com/winezer0/basecrawler/extractor/base"

	"golang.org/x/net/html"
)

// 表单参数的「值」在多数情况下是客户端生成或交互得到的，静态拿不到。
// 但有一批是例外：hidden 的 value 就是确定默认值，select/radio/checkbox
// 的候选集合本身写在 HTML 里。这些不需要发任何请求就能得到，
// 对「这个接口能带什么参数、取什么值」很有用。
//
// 取舍：只输出候选值，不合成 URL。合成 URL 意味着额外请求，
// 而 hidden 值常带 csrf token / 会话态，拼出来的地址必然失败。

const (
	// maxFormParams 限制单个表单产出的参数个数。
	maxFormParams = 32
	// maxParamValues 限制单个参数的候选值个数。
	maxParamValues = 20
	// maxParamValueLen 限制单个取值的长度，超长的多半是 base64 或序列化数据。
	maxParamValueLen = 256
)

// paramValueKinds 是允许收集取值的控件类型 → 取值来源标签。
var paramValueKinds = map[string]string{
	"hidden":   "hidden",
	"radio":    "radio",
	"checkbox": "checkbox",
}

// textInputTypes 是文本类 input 的 type 集合（P-2）。它们没有静态
// 候选，但 name + type 就是填充信息：testfill 按 type 生成测试值
// （M 批次「自动填充输入框」此前只覆盖有静态候选的控件，是缺口）。
// 提交类控件（submit/button/reset/image）不是数据参数，不收。
var textInputTypes = map[string]struct{}{
	"text": {}, "email": {}, "password": {}, "number": {},
	"tel": {}, "url": {}, "search": {}, "date": {},
	"datetime-local": {}, "month": {}, "week": {}, "time": {},
}

// formParamValues 收集表单里可静态确定的参数取值。
//
// 返回值按出现顺序排列且已去重；只含「至少有一个可用值」的参数，
// 单纯的文本框（没有 value 或 value 为空）不会出现在结果里。
func FormParamValues(form *html.Node, r *base.LimitReport) []base.ParamValue {
	var out []base.ParamValue
	idx := map[string]int{}
	candidates := map[string]struct{}{}
	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		if n == nil || depth > 12 {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			tag := strings.ToLower(c.Data)
			if tag == "form" && c != form {
				continue // 嵌套表单不属于当前表单
			}
			switch tag {
			case "input":
				addInputValue(c, idx, &out, candidates)
			case "select":
				addSelectValues(c, idx, &out, candidates)
			case "textarea":
				// 文本框的初始内容不是参数值（可编辑），不收集
			}
			walk(c, depth+1)
		}
	}
	walk(form, 0)
	r.Record(base.NewLimitEvent(base.LimitFormParams, len(candidates), len(out)))
	return out
}

func addInputValue(n *html.Node, idx map[string]int, out *[]base.ParamValue, candidates map[string]struct{}) {
	if _, disabled := base.AttrVal(n, "disabled"); disabled {
		return
	}
	typ, _ := base.AttrVal(n, "type")
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" {
		typ = "text" // HTML 默认行为
	}
	// 提交类控件不是数据参数
	switch typ {
	case "submit", "button", "reset", "image":
		return
	}
	name, ok := base.AttrVal(n, "name")
	if !ok || !validParamName(name) {
		return
	}
	name = strings.TrimSpace(name)
	if kind, ok := paramValueKinds[typ]; ok {
		// 静态候选来源：要求有非空 value（老口径不变）
		value, ok := base.AttrVal(n, "value")
		if !ok {
			return
		}
		appendParamValue(idx, out, name, kind, value, candidates)
		return
	}
	if _, ok := textInputTypes[typ]; !ok {
		return
	}
	// 文本类（P-2）：type 记入 Type 字段；有非空 value 时它是
	// 确定默认值，作静态候选；没有则 Values 为空（待填充）。
	var vals []string
	if v, ok := base.AttrVal(n, "value"); ok {
		if v = strings.TrimSpace(v); v != "" && len(v) <= maxParamValueLen {
			vals = []string{v}
		}
	}
	appendTextParam(idx, out, name, typ, vals, candidates)
}

// appendTextParam 收集文本类控件（P-2）：无静态候选也要出现，
// 供 testfill 按 type 填充。同 name 先到先得（首个控件的 type）。
func appendTextParam(idx map[string]int, out *[]base.ParamValue, name, typ string, values []string, candidates map[string]struct{}) {
	candidates[name] = struct{}{}
	i, exists := idx[name]
	if !exists {
		if len(*out) >= maxFormParams {
			return
		}
		*out = append(*out, base.ParamValue{
			Name: name, Kind: "input:" + typ, Type: typ, Values: values,
		})
		idx[name] = len(*out) - 1
		return
	}
	p := &(*out)[i]
	if !strings.HasPrefix(p.Kind, "input:") {
		return // 静态候选来源优先，不覆盖
	}
	for _, v := range values {
		if len(p.Values) >= maxParamValues {
			return
		}
		dup := false
		for _, existing := range p.Values {
			if existing == v {
				dup = true
				break
			}
		}
		if !dup {
			p.Values = append(p.Values, v)
		}
	}
}

func addSelectValues(n *html.Node, idx map[string]int, out *[]base.ParamValue, candidates map[string]struct{}) {
	name, ok := base.AttrVal(n, "name")
	if !ok || !validParamName(name) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || strings.ToLower(c.Data) != "option" {
			continue
		}
		if _, disabled := base.AttrVal(c, "disabled"); disabled {
			continue
		}
		// 没有 value 属性时，浏览器提交的是选项文本
		v, ok := base.AttrVal(c, "value")
		if !ok {
			continue
		}
		appendParamValue(idx, out, strings.TrimSpace(name), "select", v, candidates)
	}
}

// appendParamValue 把取值并入对应参数，同名参数聚合（radio 组、多选）。
func appendParamValue(idx map[string]int, out *[]base.ParamValue, name, kind, raw string, candidates map[string]struct{}) {
	v := strings.TrimSpace(raw)
	if v == "" || len(v) > maxParamValueLen {
		return
	}
	if v == "on" {
		// checkbox 未显式给 value 时的浏览器默认值，不携带信息
		return
	}
	// 先记候选再判上限，否则被截断的参数名永远不会出现在统计里。
	candidates[name] = struct{}{}
	i, exists := idx[name]
	if !exists {
		if len(*out) >= maxFormParams {
			return
		}
		*out = append(*out, base.ParamValue{Name: name, Kind: kind, Values: []string{v}})
		idx[name] = len(*out) - 1
		return
	}
	p := &(*out)[i]
	if len(p.Values) >= maxParamValues {
		return
	}
	for _, existing := range p.Values {
		if existing == v {
			return
		}
	}
	p.Values = append(p.Values, v)
}

// validParamName 与参数名收集保持同一口径：不含分隔符与控制字符。
func validParamName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && !strings.ContainsAny(name, " \t\n\r&=")
}
