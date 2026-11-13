package extractor

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// ExampleFromHTML 演示外部调用方最常用的模式：给一段 HTML，拿回 URL 候选。
//
// 注意 Raw 是**原始字符串**：相对路径的解析基准由调用方掌握
// （通常来自响应 URL），extractor 刻意不替调用方做这个决定。
func ExampleFromHTML() {
	doc, err := html.Parse(strings.NewReader(`<html><body>
		<a href="/api/orders">orders</a>
		<a href="https://cdn.example.com/lib.js">lib</a>
		<script src="/app.js"></script>
	</body></html>`))
	if err != nil {
		fmt.Println("parse:", err)
		return
	}

	base, _ := url.Parse("https://www.example.com/docs/")
	for _, r := range FromHTML(doc, DefaultOptions()) {
		// 标准模式：先解析原始字符串，相对地址按调用方基准补全，
		// 绝对地址原样通过
		ref, err := url.Parse(r.Raw)
		if err != nil {
			continue
		}
		fmt.Printf("%-40s %s\n", base.ResolveReference(ref).String(), r.Source)
	}
	// Output:
	// https://www.example.com/api/orders       a
	// https://cdn.example.com/lib.js           a
	// https://www.example.com/app.js           script
}

// ExampleFromJS 演示从一段脚本内容里提取接口地址。
// 默认 Options 不带 AST 引擎（零第三方依赖）；接入 goja/treesitter
// 引擎见根仓库 README 的 JSEngine 说明。
func ExampleFromJS() {
	reqs := FromJS(`
		fetch("/api/users?page=1");
		axios.post("/api/sessions", {user: "admin"});
		var img = "/static/logo.png";
	`, DefaultOptions())
	for _, r := range reqs {
		fmt.Println(r.Raw, r.Source)
	}
	// Output:
	// /api/users?page=1 js
	// /api/sessions js
}
