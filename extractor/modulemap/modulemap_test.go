package modulemap

import (
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// importmapText 用真实页面的形态构造一个 importmap：裸模块名做键、
// 站点绝对路径与子路径做值，外加 scopes 与数组形式的 fallback。
const importmapText = `{` +
	`"imports":{` +
	`"\/_assets\/run.js":"/_assets/run.@ndeAFp.js",` +
	`"remix-docs-shared\/hmr\/browser":"\/_assets\/docs-shared\/hmr\/public\/server-update.@BnlDLw.js",` +
	`"bare-only":"react",` +
	`"fallback":["/a.js","/b.js"]` +
	`},` +
	`"scopes":{` +
	`"/_assets/app/":{"remix-docs-shared\/search\/browser":"_assets/search.@ypcKyc.js"},` +
	`"/other/":{"k":"/other/k.js"}` +
	`}}`

// parseDoc 解析 HTML 并返回其中的 script 节点：
// ImportMapRequests 的入参约定是 script 元素本身（根包在 walk 时传入）。
func parseDoc(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var script *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if script != nil {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "script") {
			script = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if script == nil {
		t.Fatalf("no script node in %q", body)
	}
	return script
}

// TestImportMapValues 锁定 importmap 右值的三种形态与裸模块名的排除。
// importMapShaped 接受有明确起点的写法（`/`、`./`、`../`、`//`、`http(s)://`）。
// 裸相对路径 `_assets/x.js` 不在其中——规范里它是相对于 importmap 所在页面
// 自身的，而 `assets/x.js` 这类不带前缀的值在 data-* 上多为配置键，
// 收进来会与 dataAttrRequests 一样制造假路径。这里如实断言该取舍。
func TestImportMapValues(t *testing.T) {
	reqs := ImportMapRequests(parseDoc(t, `<html><head><script type="importmap">`+importmapText+`</script></head></html>`))

	var got []string
	for _, r := range reqs {
		if r.Source == "importmap" {
			got = append(got, r.Raw)
		}
	}
	// 不能依赖顺序：importMapValues 递归的是 map，而 Go 的 map 迭代顺序是
	// 随机的（本地跑过、CI 上就挂了）。排序后再断言，只锁定「提取到什么」。
	sort.Strings(got)
	want := []string{
		"/_assets/docs-shared/hmr/public/server-update.@BnlDLw.js",
		"/_assets/run.@ndeAFp.js",
		"/a.js",
		"/b.js",
		"/other/k.js",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d importmap requests %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q (全部: %v)", i, got[i], want[i], got)
		}
	}
}

// TestImportMapScopesRecursed scopes 的层级是「前缀 → 子映射」，值要递归取。
func TestImportMapScopesRecursed(t *testing.T) {
	doc := parseDoc(t, `<script type="importmap">{"imports":{},"scopes":{"/app/":{"a":"/a.js","b":["/b1.js","/b2.js"]}}}</script>`)
	var got []string
	for _, r := range ImportMapRequests(doc) {
		got = append(got, r.Raw)
	}
	// 同样不能依赖 scopes 内的顺序（map 迭代随机）。
	sort.Strings(got)
	want := []string{"/a.js", "/b1.js", "/b2.js"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

// TestImportMapBareModuleSkipped 裸模块名不是地址：它是被解析的「输入」。
func TestImportMapBareModuleSkipped(t *testing.T) {
	reqs := ImportMapRequests(parseDoc(t, `<script type="IMPORTMAP">{"imports":{"react":"/react.js","vue":"vue"}}</script>`))
	var got []string
	for _, r := range reqs {
		got = append(got, r.Raw)
	}
	if len(got) != 1 || got[0] != "/react.js" {
		t.Fatalf("got %v, want only /react.js（type 比较应大小写不敏感）", got)
	}
}

// TestSourceMappingURLForms 锁定 4 种注释写法与 data: 内联的排除。
func TestSourceMappingURLForms(t *testing.T) {
	body := []byte("console.log(1)\n" +
		"//# sourceMappingURL=app.js.map\n" +
		"//@ sourceMappingURL=other.js.map\n" +
		"/*# sourceMappingURL=block.css.map */\n" +
		"//# sourceMappingURL=data:application/json;base64,AAAA\n")
	reqs := SourceMapRequests(body)
	want := []string{"app.js.map", "other.js.map", "block.css.map"}
	if len(reqs) != len(want) {
		t.Fatalf("got %d %v, want %d", len(reqs), reqs, len(want))
	}
	for i := range want {
		if reqs[i].Raw != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, reqs[i].Raw, want[i])
		}
		if reqs[i].Source != "sourcemap" || reqs[i].Attribute != "sourceMappingURL" {
			t.Fatalf("来源标记不对: %+v", reqs[i])
		}
	}
}

// TestSourceMappingURLOnlyTail 只认文件尾部：字符串常量里同形的文本不是声明。
//
// 真实打包产物里 `"//# sourceMappingURL="` 会作为字符串出现（工具链自身的
// 代码），扫全文会把工具链里的示例路径当成这个文件的映射。
func TestSourceMappingURLOnlyTail(t *testing.T) {
	filler := strings.Repeat("var x=1;\n", 2000) // > 8KB
	body := []byte(`var tpl="//# sourceMappingURL=from-string.map";` + "\n" + filler + "//# sourceMappingURL=real.map\n")
	reqs := SourceMapRequests(body)
	if len(reqs) != 1 || reqs[0].Raw != "real.map" {
		t.Fatalf("got %+v, want only real.map", reqs)
	}
}

// TestSourceMappingURLDedupe 同一个文件重复声明（构建工具会加两次）只产出一次。
func TestSourceMappingURLDedupe(t *testing.T) {
	body := []byte("a\n//# sourceMappingURL=x.js.map\n//# sourceMappingURL=x.js.map\n")
	if reqs := SourceMapRequests(body); len(reqs) != 1 {
		t.Fatalf("got %d %+v, want 1", len(reqs), reqs)
	}
}
