package inlinejson

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/winezer0/basecrawler/extractor/base"

	"golang.org/x/net/html"
)

// 内联 hydration 载体：Nuxt 3 / Next.js Pages Router / Vue SSR 会把整棵
// 页面数据序列化进 <script type="application/json">，里面的导航路径、
// 接口地址在 HTML 标签与外链 JS 里都找不到。
//
// 实测 nuxt.com 首页：__NUXT_DATA__ 约 200 KB，含 460 条站内绝对路径，
// 现有提取只覆盖 50 条。而且 Nuxt 把斜杠写成 \u002F（`\u002Fdocs\u002F4.x`），
// 按普通 JS 或正则扫字面量都拿不到——必须按 JSON 解码后再取字符串值。
//
// 这里刻意不复用 FromJSON：响应体 JSON 与 hydration payload 的噪音形态不同
// （后者混着大量文案、ID、枚举值），需要更严的形状判定和独立的条数上限。

// inlineJSONScripts 是表驱动的识别规则：script[id] → source 后缀。
// 后缀进 Source 字段，排查时能一眼看出是哪个框架的数据。
var inlineJSONScripts = map[string]string{
	"__NUXT_DATA__":     "nuxt-data",
	"__NEXT_DATA__":     "next-data",
	"__INITIAL_STATE__": "initial-state",
}

// maxInlineJSONURLs 限制单段 payload 的产出，避免大型导航树灌满队列。
const maxInlineJSONURLs = 200

// inlineJSONStringRe 是「payload 不是合法 JSON」时的保守兜底，
// 只取带引号的字面量，不扫裸字符串。
var inlineJSONStringRe = regexp.MustCompile(`"((?:[^"\\]|\\.){2,300})"`)

// inlineJSONKind 判断 <script> 是否是 hydration JSON 载体。
//
// 只认 id 白名单，或 type=application/json 且带 data-nuxt-data 标记；
// 刻意不认 application/ld+json（schema.org 结构化数据），那是另一类内容。
func InlineJSONKind(n *html.Node) (string, bool) {
	if n == nil || !strings.EqualFold(n.Data, "script") {
		return "", false
	}
	var id, typ string
	var nuxtData bool
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "id":
			id = a.Val
		case "type":
			typ = strings.ToLower(strings.TrimSpace(a.Val))
		case "data-nuxt-data":
			nuxtData = true
		}
	}
	if kind, ok := inlineJSONScripts[id]; ok {
		return kind, true
	}
	if typ == "application/json" && nuxtData {
		return "nuxt-data", true
	}
	return "", false
}

// FromInlineJSON 从一段 hydration payload 里提取站内地址。
//
// 只收解码后以 / 或 http(s) 开头的字面量，不合成 URL、不猜测相对名。
func FromInlineJSON(payload, kind string) []base.Request {
	return FromInlineJSONWithLimits(payload, kind, nil)
}

// fromInlineJSON 是带截断统计的内部实现；公共入口保持原有签名不变。
func FromInlineJSONWithLimits(payload, kind string, r *base.LimitReport) []base.Request {
	source := "inline-json:" + kind
	var v any
	if err := json.Unmarshal([]byte(payload), &v); err == nil {
		reqs, ev := walkInlineJSON(v, source)
		r.Record(ev)
		return reqs
	}
	reqs, ev := inlineJSONFallback(payload, source)
	r.Record(ev)
	return reqs
}

// walkInlineJSON 遍历完整棵树后再按上限截断：只有这样才知道「本该产出多少」。
// 遍历成本远小于一次网络请求，换来的是截断可观测。
func walkInlineJSON(v any, source string) ([]base.Request, base.LimitEvent) {
	out := make([]base.Request, 0, 16)
	seen := make(map[string]struct{}, 32)
	candidates := 0
	// nextPages 先行：结构化抽 __NEXT_DATA__ 的 page/pages 字段。
	// Next.js Pages Router 把整站路由挂在 page 键下，这些值全部以 /
	// 开头、语义干净，比通用 walk 的形状判定更可靠；且能兜住通用
	// walk 因转义残留/超长被拒的路由值。
	for _, p := range nextDataPages(v, 0) {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		candidates++
		if len(out) < maxInlineJSONURLs {
			out = append(out, base.Request{Raw: p, Method: http.MethodGet,
				Source: source + ":page"})
		}
	}
	var walk func(any, int)
	walk = func(node any, depth int) {
		if depth > 24 {
			return
		}
		switch t := node.(type) {
		case map[string]any:
			for _, val := range t {
				walk(val, depth+1)
			}
		case []any:
			for _, val := range t {
				walk(val, depth+1)
			}
		case string:
			if !inlinePathUsable(t) {
				return
			}
			if _, dup := seen[t]; dup {
				return
			}
			seen[t] = struct{}{}
			candidates++
			if len(out) < maxInlineJSONURLs {
				out = append(out, base.Request{Raw: t, Method: http.MethodGet, Source: source})
			}
		}
	}
	walk(v, 0)
	return out, base.NewLimitEvent(base.LimitInlineJSON, candidates, len(out))
}

// nextDataPages 从 __NEXT_DATA__ 树里结构化抽出路由：
//   - map 键 "page" 的字符串值（每层 pageProps 都可能带）
//   - 键 "pages" / "pageList" 的数组元素（旧版 Next 把全站路由清单挂这里）
//
// 只认以 / 开头的值（路由必然如此），不做形状判定——结构键本身就是
// 语义保证；静态资源仍排除，防止个别站点把图片路径也塞进 page。
//
// 产出按发现深度降序、同深度按字典序排列：map 遍历顺序在 Go 里是
// 随机的，同一棵树里嵌套 pageProps.page（具体页）与顶层 page（[slug]
// 模板）谁先出现本来就不定；具体页藏得更深、语义更专，让深者优先
// 既修掉不确定性，也符合「结构化优先于模板」的直觉。
func nextDataPages(v any, depth int) []string {
	type hit struct {
		path  string
		depth int
	}
	var hits []hit
	var collect func(any, int)
	collect = func(node any, d int) {
		if d > 24 {
			return
		}
		switch t := node.(type) {
		case map[string]any:
			for k, val := range t {
				switch k {
				case "page":
					if s, ok := val.(string); ok && strings.HasPrefix(s, "/") &&
						len(s) > 1 && !base.IsStaticAsset(s) {
						hits = append(hits, hit{s, d})
					}
				case "pages", "pageList":
					if arr, ok := val.([]any); ok {
						for _, e := range arr {
							if s, ok := e.(string); ok && strings.HasPrefix(s, "/") &&
								len(s) > 1 && !base.IsStaticAsset(s) {
								hits = append(hits, hit{s, d})
							}
						}
					}
				default:
					collect(val, d+1)
				}
			}
		case []any:
			for _, e := range t {
				collect(e, d+1)
			}
		}
	}
	collect(v, depth)
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].depth != hits[j].depth {
			return hits[i].depth > hits[j].depth
		}
		return hits[i].path < hits[j].path
	})
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.path)
	}
	return out
}

// inlineJSONFallback 处理带赋值前缀（不是纯 JSON）的初始化脚本。
func inlineJSONFallback(payload, source string) ([]base.Request, base.LimitEvent) {
	var out []base.Request
	seen := make(map[string]struct{}, 32)
	candidates := 0
	for _, m := range inlineJSONStringRe.FindAllStringSubmatch(payload, -1) {
		s, ok := base.UnescapeJSString(m[1])
		if !ok || !inlinePathUsable(s) {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		candidates++
		if len(out) < maxInlineJSONURLs {
			out = append(out, base.Request{Raw: s, Method: http.MethodGet, Source: source})
		}
	}
	return out, base.NewLimitEvent(base.LimitInlineJSON, candidates, len(out))
}

// inlinePathUsable 是比 jsonURL 更严的一层：payload 里混着大量文案与 ID。
func inlinePathUsable(s string) bool {
	if !base.JSONURL(s) || len(s) > 300 {
		return false
	}
	// 模板占位、JS 插值、转义残留都不该当地址
	if strings.Contains(s, "EXPR") || strings.ContainsAny(s, "${}\\") {
		return false
	}
	// 图片/字体等静态资源由标签属性提取，不在导航数据里重复收
	if base.IsStaticAsset(s) {
		return false
	}
	return true
}
