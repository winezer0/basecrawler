package extractor

// 以下用例验证的是 FromHTML 对内联 script 的分派与回退行为，
// 属根包编排层职责，故放在根包测试（拆包前在 inlinejson/nextjs 的文件里）。

import (
	"strings"
	"testing"
)

// TestInlineScriptFallsBackWithoutPayload 守住「命中 __next_f 但无 payload」的归零。
//
// 初始化脚本里除了 __next_f 还写着普通接口路径；只跑 FromNextFlight 会把
// 整段脚本丢掉，必须回退到通用 JS 提取。
func TestInlineScriptFallsBackWithoutPayload(t *testing.T) {
	doc := parseHTML(t, `<html><body><script>self.__next_f=self.__next_f||[];fetch("/api/from-init-script")</script></body></html>`)
	got := FromHTML(doc, Options{ExtractJS: true})
	if !contains(raws(got), "/api/from-init-script") {
		t.Fatalf("初始化脚本里的普通接口路径被丢弃，实际 %v", raws(got))
	}
}

// TestInlineScriptMergesASTAndJSON 锁定「合并而非二选一」。
//
// 只跑结构化解析会丢掉 payload 里的绝对外链：它们可能排在 200 条上限之后，
// 或属于被我们过滤的静态资源，但旧版从同一份 payload 里是能拿到的。
func TestInlineScriptMergesASTAndJSON(t *testing.T) {
	const payload = `["/docs/4.x","https://scripts.example.com/a.js","/img/logo.svg"]`
	doc := parseHTML(t, `<html><body><script id="__NUXT_DATA__" type="application/json">`+payload+`</script></body></html>`)
	urls := raws(FromHTML(doc, Options{ExtractJS: true}))

	for _, want := range []string{"/docs/4.x", "https://scripts.example.com/a.js"} {
		if !contains(urls, want) {
			t.Errorf("缺少 %s，实际 %v", want, urls)
		}
	}
	var structured bool
	for _, r := range FromInlineJSON(payload, "nuxt-data") {
		if r.Raw == "/docs/4.x" && r.Source == "inline-json:nuxt-data" {
			structured = true
		}
	}
	if !structured {
		t.Error("结构化解析的独立 source 丢失")
	}
}

// TestFromHTMLInlineJSON 是端到端分派：内联 hydration JSON 走专用解析。
func TestFromHTMLInlineJSON(t *testing.T) {
	doc := parseHTML(t, `<html><body><script id="__NUXT_DATA__" type="application/json">[["\u002Fdocs\u002F4.x","/api/orders"]]</script></body></html>`)
	got := FromHTML(doc, Options{ExtractJS: true})

	var found bool
	for _, r := range got {
		if r.Raw == "/docs/4.x" {
			found = true
			if r.Source != "inline-json:nuxt-data" {
				t.Errorf("source = %q", r.Source)
			}
		}
	}
	if !found {
		t.Fatalf("未从内联 hydration JSON 取出路径，实际 %v", raws(got))
	}
}

// TestImportMapMalformedIgnored 残缺 JSON 不报错也不产出：真实站点上很常见。
func TestImportMapMalformedIgnored(t *testing.T) {
	reqs := FromHTML(parseHTML(t, `<script type="importmap">{"imports":{"a":"/a.js",}}</script>`), DefaultOptions())
	for _, r := range reqs {
		if r.Source == "importmap" {
			t.Fatalf("残缺 importmap 不应产出，得到 %q", r.Raw)
		}
	}
}

// TestImportMapNotConfusedWithModuleScript 普通 module script 不触发；
// 内联模块脚本仍走 JS 提取，不能被 importmap 分支吞掉。
func TestImportMapNotConfusedWithModuleScript(t *testing.T) {
	doc := parseHTML(t, `<script type="module">import "/api/from-module.js"</script>`)
	reqs := FromHTML(doc, DefaultOptions())
	var sawModule, sawImportmap bool
	for _, r := range reqs {
		if r.Source == "importmap" {
			sawImportmap = true
		}
		if strings.Contains(r.Raw, "/api/from-module.js") {
			sawModule = true
		}
	}
	if sawImportmap {
		t.Fatal("type=module 不应被当成 importmap")
	}
	if !sawModule {
		t.Fatalf("内联模块的 import 路径应照常提取，得到 %+v", reqs)
	}
}
