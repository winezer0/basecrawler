package inlinejson

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestFromInlineJSONNuxtData 覆盖 Nuxt 3 的 hydration payload。
// 斜杠被写成 \u002F，按普通 JS 或正则扫字面量都拿不到，必须按 JSON 解码。
func TestFromInlineJSONNuxtData(t *testing.T) {
	const payload = `[["ShallowReactive",1],{"path":2},"\u002Fdocs\u002F4.x","\u002Fapi\u002Fitems","Docs","/img/logo.png","/x/EXPR","https://cdn.example.com/a.js"]`

	got := FromInlineJSON(payload, "nuxt-data")
	urls := raws(got)
	for _, want := range []string{"/docs/4.x", "/api/items", "https://cdn.example.com/a.js"} {
		if !contains(urls, want) {
			t.Errorf("缺少 %s，实际 %v", want, urls)
		}
	}
	for _, bad := range []string{"ShallowReactive", "Docs", "/img/logo.png", "/x/EXPR"} {
		if contains(urls, bad) {
			t.Errorf("不该提取 %q，实际 %v", bad, urls)
		}
	}
	for _, r := range got {
		if r.Source != "inline-json:nuxt-data" {
			t.Errorf("source 期望 inline-json:nuxt-data，实际 %q", r.Source)
		}
	}
}

// TestFromInlineJSONNextData 覆盖 Next.js Pages Router 的对象形态。
func TestFromInlineJSONNextData(t *testing.T) {
	const payload = `{"props":{"pageProps":{"href":"/docs/getting-started"}},"page":"/","buildId":"abc"}`

	got := FromInlineJSON(payload, "next-data")
	if len(got) != 1 || got[0].Raw != "/docs/getting-started" {
		t.Fatalf("期望只取出 /docs/getting-started，实际 %v", raws(got))
	}
	if got[0].Source != "inline-json:next-data" {
		t.Errorf("source = %q", got[0].Source)
	}
}

// TestFromInlineJSONFallback 覆盖带赋值前缀、不是合法 JSON 的初始化脚本。
func TestFromInlineJSONFallback(t *testing.T) {
	const payload = `window.__INITIAL_STATE__ = {"links":["\/account\/profile","\/api\/v2\/orders","some text"]};`

	got := FromInlineJSON(payload, "initial-state")
	urls := raws(got)
	for _, want := range []string{"/account/profile", "/api/v2/orders"} {
		if !contains(urls, want) {
			t.Errorf("缺少 %s，实际 %v", want, urls)
		}
	}
}

// TestFromInlineJSONLimit 确认大 payload 不会灌满队列。
func TestFromInlineJSONLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 400; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"/page/`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(string(rune('a' + i/26)))
		b.WriteString(`"`)
	}
	b.WriteString("]")

	if got := len(FromInlineJSON(b.String(), "nuxt-data")); got > maxInlineJSONURLs {
		t.Errorf("产出 %d 条，超过上限 %d", got, maxInlineJSONURLs)
	}
}

// TestInlineJSONKind 确认只认 hydration 载体，不认 ld+json。
func TestInlineJSONKind(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{"nuxt by id", `<script id="__NUXT_DATA__" type="application/json">[]</script>`, "nuxt-data"},
		{"nuxt by marker", `<script type="application/json" data-nuxt-data="nuxt-app">[]</script>`, "nuxt-data"},
		{"next pages", `<script id="__NEXT_DATA__" type="application/json">{}</script>`, "next-data"},
		{"ld+json", `<script type="application/ld+json">{}</script>`, ""},
		{"plain script", `<script>var a=1</script>`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := InlineJSONKind(firstScriptNode(t, c.html))
			if c.want == "" && ok {
				t.Errorf("不该识别，实际 %q", got)
			}
			if c.want != "" && got != c.want {
				t.Errorf("期望 %q，实际 %q", c.want, got)
			}
		})
	}
}

func firstScriptNode(t *testing.T, s string) *html.Node {
	t.Helper()
	doc := parseHTML(t, s)
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "script") {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if found == nil {
		t.Fatal("HTML 里没有 script 节点")
	}
	return found
}

// TestNextDataPages 结构化解析 __NEXT_DATA__：page 键与 pages 清单
// 优先产出，source 带 :page 后缀；含通用 walk 兜不住的值。
func TestNextDataPages(t *testing.T) {
	payload := `{"props":{"pageProps":{"page":"/blog/deep-post","__N_SSG":true}},"page":"/blog/[slug]","runtimeConfig":{}}
	`
	got := FromInlineJSON(payload, "next-data")
	if len(got) == 0 {
		t.Fatal("未产出任何结果")
	}
	first := got[0]
	if first.Raw != "/blog/deep-post" {
		t.Errorf("第一条 = %q，期望 /blog/deep-post（page 键优先）", first.Raw)
	}
	if first.Source != "inline-json:next-data:page" {
		t.Errorf("Source = %q，期望带 :page 后缀", first.Source)
	}

	// pages 清单：整站路由数组
	payload2 := `{"pages":["/","/about","/pricing"],"buildId":"abc"}`
	got2 := FromInlineJSON(payload2, "next-data")
	raws := map[string]bool{}
	for _, r := range got2 {
		raws[r.Raw] = true
	}
	for _, want := range []string{"/about", "/pricing"} {
		if !raws[want] {
			t.Errorf("pages 清单缺 %q（got %v）", want, raws)
		}
	}

	// page 键里混静态资源应被排除
	payload3 := `{"page":"/logo.png"}`
	got3 := FromInlineJSON(payload3, "next-data")
	if len(got3) != 0 {
		t.Errorf("静态资源不应作为 page 产出：%v", got3)
	}
}
