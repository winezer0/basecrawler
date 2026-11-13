// Package testfill 把采集结果填充成「可跑的测试请求」。
//
// 定位：basecrawler 负责「找到接口」，本包负责「给接口填上像样的测试值」，
// 产物是请求清单/脚本，由调用方决定是否执行——本包自己不发送任何请求。
//
// 设计约束（与 extractor 一致）：
//   - 纯函数：无 IO、无全局状态、不引入 math/rand 全局源
//   - 确定性优先：ValueSource 可插拔，默认实现 seeded，同输入同输出
//   - 凭证只占位不编造：Authorization 等由调用方提供模板
package testfill

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/winezer0/basecrawler/extractor"
)

// 取值形态。表驱动的规则集落在 valueKind，取值落在 SeededSource。
const (
	kindInt      = "int"
	kindUUID     = "uuid"
	kindEmail    = "email"
	kindName     = "name"
	kindPassword = "password"
	kindToken    = "token"
	kindURL      = "url"
	kindPhone    = "phone"
	kindDate     = "date"
	kindWord     = "word"
)

// kindExact 精确名 → 形态。key 是归一化后的名字。
var kindExact = map[string]string{
	"id": kindInt, "page": kindInt, "limit": kindInt, "size": kindInt,
	"count": kindInt, "perpage": kindInt, "pagesize": kindInt,
	"offset": kindInt, "skip": kindInt, "start": kindInt, "from": kindInt,
	"uuid": kindUUID, "guid": kindUUID,
	"email": kindEmail, "mail": kindEmail,
	"password": kindPassword, "pwd": kindPassword,
	"token": kindToken, "apikey": kindToken, "key": kindToken,
	"secret": kindToken, "auth": kindToken,
	"username": kindName, "user": kindName, "name": kindName,
	"phone": kindPhone, "tel": kindPhone, "mobile": kindPhone,
	"url": kindURL, "uri": kindURL, "link": kindURL,
	"callback": kindURL, "redirect": kindURL, "return": kindURL,
	"q": kindWord, "query": kindWord, "search": kindWord, "keyword": kindWord,
	"date": kindDate, "time": kindDate,
	"homepage": kindURL, "website": kindURL,
}

// uppercasePlaceholders 是全大写占位符段表（O-1 实测 GitHub REST
// 文档风格：/users/USERNAME/repos、/repos/OWNER/REPO）。
// 用名单而非长度启发：API/V1/HTTP 这类缩写目录不填，ORG/USERNAME 才填。
var uppercasePlaceholders = map[string]string{
	"USERNAME": kindName, "USER": kindName, "OWNER": kindName,
	"ORG": kindName, "NAME": kindName, "REPO": kindWord,
	"TEMPLATE_OWNER": kindName, "TEMPLATE_REPO": kindWord,
	"ID": kindInt, "UUID": kindUUID, "EMAIL": kindEmail,
	"TOKEN": kindToken, "KEY": kindToken, "PASSWORD": kindPassword,
	"PAGE": kindInt, "LIMIT": kindInt, "OFFSET": kindInt,
	"QUERY": kindWord, "SEARCH": kindWord, "URL": kindURL,
	"CALLBACK": kindURL, "PHONE": kindPhone, "DATE": kindDate,
}

// kindContains 包含规则，按顺序首个命中生效（归一化后）。
var kindContains = []struct {
	sub  string
	kind string
}{
	{"email", kindEmail},
	{"password", kindPassword},
	{"pwd", kindPassword},
	{"token", kindToken},
	{"apikey", kindToken},
	{"secret", kindToken},
	{"uuid", kindUUID},
	{"phone", kindPhone},
	{"tel", kindPhone},
	{"callback", kindURL},
	{"redirect", kindURL},
	{"name", kindName},
	{"url", kindURL},
	{"id", kindInt},
}

// normalizeName 归一化参数名：小写、去掉连字符/下划线/空格/点。
func normalizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch r {
		case '-', '_', ' ', '.', '[', ']':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// valueKind 推断参数名的取值形态。纯函数，表驱动。
func valueKind(name string) string {
	if k, ok := uppercasePlaceholders[name]; ok {
		return k
	}
	n := normalizeName(name)
	if n == "" {
		return kindWord
	}
	if k, ok := kindExact[n]; ok {
		return k
	}
	for _, c := range kindContains {
		if strings.Contains(n, c.sub) {
			return c.kind
		}
	}
	return kindWord
}

// ValueSource 生成指定形态的测试值。可替换（测试用固定源；
// 将来要「真实感数据」时可换 gofakeit 实现，上层不动）。
type ValueSource interface {
	Next(kind string) string
}

// SeededSource 确定性取值源：同 seed 同序列，无全局状态。
// 每个 kind 独立计数——填充结果不受无关调用影响（同 seed 必然同输出）。
type SeededSource struct {
	seed   int
	counts map[string]int
}

// NewSeededSource 创建确定性取值源。
func NewSeededSource(seed int) *SeededSource {
	return &SeededSource{seed: seed, counts: map[string]int{}}
}

// Next 按形态返回下一个值（同 kind 内独立轮换）。
func (s *SeededSource) Next(kind string) string {
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[kind]++
	n := s.counts[kind]
	switch kind {
	case kindInt:
		return strconv.Itoa((n-1)%9 + 1)
	case kindUUID:
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", s.seed*7919+n, n)
	case kindEmail:
		return fmt.Sprintf("user%d@example.com", n)
	case kindName:
		return fmt.Sprintf("user%d", n)
	case kindPassword:
		return fmt.Sprintf("Test@%06d", n)
	case kindToken:
		return fmt.Sprintf("%032x", s.seed*104729+n)
	case kindURL:
		return "https://example.com/callback"
	case kindPhone:
		return fmt.Sprintf("+8613%09d", n)
	case kindDate:
		return "2026-01-0" + strconv.Itoa((n-1)%9+1)
	default: // kindWord
		return fmt.Sprintf("sample-%d", n)
	}
}

var braceParamRe = regexp.MustCompile(`\$\{(\w+)\}|\{(\w+)\}`)

// FillTemplate 填充 URL 模板里的占位符：`:id` 路径段、`{id}`、`${id}`。
// 纯函数。无法解析的占位符（如 `${map[e]}`）原样保留。
func FillTemplate(raw string, src ValueSource) string {
	// 路径段：:name（跳过带正则约束的 :name(...)，name 仍用于选值）
	segs := strings.Split(raw, "/")
	for i, seg := range segs {
		// :name 路径参数（跳过带正则约束的 :name(...)，name 仍用于选值）
		if strings.HasPrefix(seg, ":") && len(seg) >= 2 {
			name := seg[1:]
			if j := strings.IndexByte(name, '('); j >= 0 {
				name = name[:j]
			}
			segs[i] = src.Next(valueKind(name))
			continue
		}
		// 全大写占位符段（USERNAME/OWNER/REPO…，O-1 实测的文档风格）
		if k, ok := uppercasePlaceholders[seg]; ok {
			segs[i] = src.Next(k)
		}
	}
	out := strings.Join(segs, "/")
	// {name} / ${name}：任意位置
	out = braceParamRe.ReplaceAllStringFunc(out, func(m string) string {
		g := braceParamRe.FindStringSubmatch(m)
		name := g[1]
		if name == "" {
			name = g[2]
		}
		return src.Next(valueKind(name))
	})
	return out
}

// FillParams 把「参数名 + 静态候选」填充成 name→value 映射。
// 有静态候选的（select/radio/hidden）取第一个；其余按名字推断形态。
// 纯函数。
// inputTypeKinds 是 input type → 取值形态映射（P-2）。空值表示
// 「该 type 不携带形态信息，回退参数名推断」（text/search）。
// type 是控件契约，比参数名猜测可靠——优先用它。
var inputTypeKinds = map[string]string{
	"email":          kindEmail,
	"password":       kindPassword,
	"number":         kindInt,
	"range":          kindInt,
	"tel":            kindPhone,
	"url":            kindURL,
	"date":           kindDate,
	"datetime-local": kindDate,
	"month":          kindDate,
	"week":           kindDate,
	"time":           kindDate,
}

// valueKindFromInputType 按 input type 选取值形态；空字符串表示
// 回退参数名推断。纯函数。
func valueKindFromInputType(typ string) string {
	return inputTypeKinds[strings.ToLower(strings.TrimSpace(typ))]
}

// kindForParam type 优先于名字（控件契约比名字猜测可靠）。纯函数。
func kindForParam(typ, name string) string {
	if k := valueKindFromInputType(typ); k != "" {
		return k
	}
	return valueKind(name)
}

// FillParams 填充参数：静态候选（hidden/select/radio/checkbox）取原值。
// 注意 hidden 的静态值常是 csrf/session 等真实凭证，只有
// --testfill-use-hidden 显式授权时才走这里；默认请用 FillParamsSafe。
func FillParams(names []string, values []extractor.ParamValue, src ValueSource) map[string]string {
	return fillParams(names, values, src, true)
}

// FillParamsSafe 与 FillParams 形态一致，但把静态候选值替换为占位测试值，
// 不把采集到的真实凭证原样发回目标站。CLI 默认走这条路径。
func FillParamsSafe(names []string, values []extractor.ParamValue, src ValueSource) map[string]string {
	return fillParams(names, values, src, false)
}

func fillParams(names []string, values []extractor.ParamValue, src ValueSource, useHidden bool) map[string]string {
	out := make(map[string]string, len(names)+len(values))
	// 1. 静态候选（hidden/select/radio/checkbox/有默认值的控件）
	for _, pv := range values {
		if len(pv.Values) > 0 {
			if useHidden {
				out[pv.Name] = pv.Values[0]
			} else {
				out[pv.Name] = src.Next(kindForParam(pv.Type, pv.Name))
			}
		}
	}
	// 2. 文本类控件（P-2）：无静态候选，按 input type 选形态，
	// type 无信息（text/search）时回退参数名推断
	for _, pv := range values {
		if len(pv.Values) > 0 || !strings.HasPrefix(pv.Kind, "input:") {
			continue
		}
		if _, ok := out[pv.Name]; ok {
			continue
		}
		out[pv.Name] = src.Next(kindForParam(pv.Type, pv.Name))
	}
	// 3. 裸参数名（JS/HTML 链接里带的名字，无控件上下文）
	for _, n := range names {
		if _, ok := out[n]; ok {
			continue
		}
		out[n] = src.Next(valueKind(n))
	}
	return out
}

// FillJSON 按 key 的取值形态填充 JSON body 的字符串值。
// 数字/布尔/null/数组元素原样保留；非 JSON body 原样返回。
// 纯函数。
func FillJSON(body string, src ValueSource) (string, error) {
	if strings.TrimSpace(body) == "" {
		return body, nil
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body, nil // 表单编码等非 JSON body：不动
	}
	filled := fillJSONValue(v, src)
	out, err := json.Marshal(filled)
	if err != nil {
		return body, err
	}
	return string(out), nil
}

func fillJSONValue(v any, src ValueSource) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		// 按 key 排序遍历：Go map 迭代顺序随机，不排序则同 seed
		// 下「哪个 key 拿到哪个值」不稳定，破坏确定性承诺。
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := t[k]
			if _, ok := val.(string); ok {
				out[k] = src.Next(valueKind(k))
				continue
			}
			out[k] = fillJSONValue(val, src)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = fillJSONValue(val, src)
		}
		return out
	default:
		return v
	}
}
