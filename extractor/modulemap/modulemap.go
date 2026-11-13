package modulemap

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/winezer0/basecrawler/extractor/base"

	"golang.org/x/net/html"
)

// 本文件收集「ESM 部署形态」带来的两类地址，二者都是浏览器/打包器
// 实际会去取的文件，但在 HTML 标签与 JS 字符串里都看不见。
//
//  1. <script type="importmap"> —— 浏览器加载模块时按此表解析裸模块名。
//     表的右值（imports / scopes）就是真实资源地址。R-0 实测
//     docs.remix.run 单页 54 条 imports、remix.run/jam/2026 93 条，
//     其中 91 条 live 且**完全不在**现有结果里（现有规则只认标签与 JS 字面量）。
//
//  2. `//# sourceMappingURL=...` —— 每个 JS/CSS 尾部一行，指向源码映射。
//     现有实现只在响应**本身**是 .map 时解析它的 sources，从不去取那个
//     声明的 .map 文件。R-0 实测 petstore3 的 swagger-ui.css.map 200 且漏检。
//
// H-0 曾以「八站独有 URL 全 0」裁定 importmap 不做；R-0 用两个含 importmap
// 的真实站复测，增量 91 条 —— 该结论按 Q 批次判据（以拿到 URL 资源为准）
// 作废，见 ISSUES 十六/十七。

// importMapShaped 判断一个 importmap 右值是否是资源地址。
//
// 裸模块名（`react`、`@remix-run/component`）不是地址，按规范它就是靠
// 这张表去解析的「输入」而不是「输出」，收进来只会制造 404。
func importMapShaped(s string) bool {
	s = strings.TrimSpace(s)
	if !base.PathShaped(s) && !strings.HasPrefix(s, "./") {
		return false
	}
	return !strings.ContainsAny(s, " \t\r\n")
}

// importMapValues 从一个 importmap 对象的 imports / scopes 里取出全部右值。
//
// 兼容三种右值形态：
//   - 字符串：普通映射；
//   - 数组：规范里的 fallback 列表（取全部，浏览器会依次尝试）；
//   - 对象（scopes）：scope 前缀 → 子映射，值递归同样的规则。
//
// 纯函数。
func importMapValues(node any, out *[]string) {
	switch v := node.(type) {
	case string:
		if importMapShaped(v) {
			*out = append(*out, strings.TrimSpace(v))
		}
	case []any:
		for _, item := range v {
			importMapValues(item, out)
		}
	case map[string]any:
		for _, item := range v {
			importMapValues(item, out)
		}
	}
}

// parseImportMap 解析一个 importmap 的 JSON 文本，返回其中的资源地址。
//
// 解析失败返回 nil：真实站点上残缺的 importmap 不少（注释、尾逗号），
// 报错没有价值，跳过即可。去重保序。纯函数。
func parseImportMap(text string) []string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	var raw []string
	if v, ok := doc["imports"]; ok {
		importMapValues(v, &raw)
	}
	if v, ok := doc["scopes"]; ok {
		importMapValues(v, &raw)
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, u := range raw {
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// importMapRequests 从 <script type="importmap"> 提取资源地址。
//
// type 的比较按 HTML 规范是 ASCII 大小写不敏感，且允许带参数
// （`type="importmap"` / `type="IMPORTMAP"`）。纯函数。
func ImportMapRequests(n *html.Node) []base.Request {
	if !strings.EqualFold(strings.TrimSpace(n.Data), "script") {
		return nil
	}
	typ, ok := base.AttrVal(n, "type")
	if !ok || !strings.EqualFold(strings.TrimSpace(typ), "importmap") {
		return nil
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
		}
	}
	if sb.Len() == 0 {
		return nil
	}
	urls := parseImportMap(sb.String())
	out := make([]base.Request, 0, len(urls))
	for _, u := range urls {
		out = append(out, base.Request{
			Raw: u, Method: http.MethodGet, Source: "importmap", Attribute: "imports", Tag: "script",
		})
	}
	return out
}

// sourceMappingURLs 从 JS/CSS 尾部注释里取出 sourceMappingURL 声明。
//
// 位置限定在尾部 8KB：注释可能出现在任意位置（字符串里也会有假命中），
// 而浏览器/DevTools 只认文件末尾那一行。base64 内联（data:）不是外部资源，
// 直接跳过。去重保序。纯函数。
func sourceMappingURLs(body []byte) []string {
	tail := body
	if len(tail) > 8192 {
		tail = tail[len(tail)-8192:]
	}
	text := string(tail)
	var out []string
	seen := make(map[string]struct{}, 2)
	add := func(v string) {
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if v == "" || strings.HasPrefix(v, "data:") {
			return
		}
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// 两种写法：//# sourceMappingURL=x.js.map 与 /*# sourceMappingURL=x.js.map */
		for _, marker := range []string{"//# sourceMappingURL=", "//@ sourceMappingURL=", "/*# sourceMappingURL=", "/*@ sourceMappingURL="} {
			idx := strings.Index(line, marker)
			if idx < 0 {
				continue
			}
			val := line[idx+len(marker):]
			if end := strings.Index(val, "*/"); end >= 0 {
				val = val[:end]
			}
			if end := strings.IndexAny(val, " \t"); end >= 0 {
				val = val[:end]
			}
			add(val)
		}
	}
	return out
}

// SourceMapRequests 从一个 JS/CSS 响应体里取出 sourceMappingURL 声明的地址。
//
// 只返回声明本身，不判断目标是否存在——是否 live 由抓取决定
// （Q 批次判据：能拿到 URL 资源就去做）。纯函数。
func SourceMapRequests(body []byte) []base.Request {
	urls := sourceMappingURLs(body)
	out := make([]base.Request, 0, len(urls))
	for _, u := range urls {
		out = append(out, base.Request{
			Raw: u, Method: http.MethodGet, Source: "sourcemap", Attribute: "sourceMappingURL",
		})
	}
	return out
}
