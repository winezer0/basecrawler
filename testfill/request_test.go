package testfill

import (
	"strings"
	"testing"

	"github.com/winezer0/basecrawler"
	"github.com/winezer0/basecrawler/extractor"
)

func fixtureResults() []basecrawler.Result {
	return []basecrawler.Result{
		{
			URL: "https://api.openai.com/v1/chat/completions", Method: "POST",
			Kind: "curl", StatusCode: 0,
			Raw: `curl https://api.openai.com/v1/chat/completions -H "Content-Type: application/json" -H "Authorization: Bearer $OPENAI_API_KEY" -d '{"model": "gpt-4o-mini", "temperature": 0.7, "user": {"id": 3}}'`,
		},
		{
			URL: "https://target.example.com/api/users/:id/orders", Method: "GET",
			StatusCode: 200, Params: []string{"status", "page"},
			ParamValues: nil,
		},
		{
			URL: "https://target.example.com/api/search", Method: "GET",
			StatusCode: 200, Params: []string{"q"},
			ParamValues: nil,
		},
		{
			URL: "https://target.example.com/docs", Method: "GET",
			StatusCode: 200, ContentType: "text/html; charset=utf-8", // 页面，默认跳过
		},
		{
			URL: "https://target.example.com/gone", Method: "GET",
			StatusCode: 404, // 默认过滤
		},
	}
}

func TestGenerateRequestsCurl(t *testing.T) {
	reqs := GenerateRequests(fixtureResults(), Config{AuthHeader: "Authorization: Bearer ${TOKEN}"})
	// 404 被过滤，curl 1 条 + 普通 2 条
	if len(reqs) != 3 {
		t.Fatalf("应 3 条, got %d: %+v", len(reqs), reqs)
	}
	curl := reqs[0]
	if curl.Method != "POST" || curl.URL != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("curl 请求: %+v", curl)
	}
	if curl.Headers["Authorization"] != "Bearer ${TOKEN}" {
		t.Errorf("认证头应被占位替换: %q", curl.Headers["Authorization"])
	}
	if !strings.Contains(curl.Body, `"temperature":0.7`) {
		t.Errorf("body 数字应保留: %s", curl.Body)
	}
	if !strings.Contains(curl.Body, `"model":"sample-1"`) {
		t.Errorf("body 字符串应按 key 填充: %s", curl.Body)
	}
}

func TestGenerateRequestsPathAndQuery(t *testing.T) {
	reqs := GenerateRequests(fixtureResults(), Config{})
	var users, search *TestRequest
	for i := range reqs {
		switch {
		case strings.Contains(reqs[i].FromURL, "/api/users/"):
			users = &reqs[i]
		case strings.Contains(reqs[i].FromURL, "/api/search"):
			search = &reqs[i]
		}
	}
	if users == nil || search == nil {
		t.Fatal("两条普通请求都应生成")
	}
	if users.URL != "https://target.example.com/api/users/1/orders" {
		t.Errorf("路径参数应填充: %q", users.URL)
	}
	// 同请求内 kind 独立计数：:id 已消耗 int#1，page 取 int#2
	if users.Query["status"] != "sample-1" || users.Query["page"] != "2" {
		t.Errorf("查询参数应按名填充: %v", users.Query)
	}
	if search.Query["q"] != "sample-1" {
		t.Errorf("q 应填充: %v", search.Query)
	}
}

func TestGenerateRequestsOnlyOK(t *testing.T) {
	reqs := GenerateRequests(fixtureResults(), Config{})
	for _, r := range reqs {
		if strings.Contains(r.FromURL, "/gone") {
			t.Error("404 结果不应生成请求")
		}
		if strings.Contains(r.FromURL, "/docs") {
			t.Error("HTML 页面不应生成请求（默认）")
		}
	}
}

func TestGenerateRequestsIncludePages(t *testing.T) {
	reqs := GenerateRequests(fixtureResults(), Config{IncludePages: true})
	found := false
	for _, r := range reqs {
		if strings.Contains(r.FromURL, "/docs") {
			found = true
		}
	}
	if !found {
		t.Error("IncludePages=true 应包含页面结果")
	}
}

func TestGenerateRequestsMaxRequests(t *testing.T) {
	reqs := GenerateRequests(fixtureResults(), Config{MaxRequests: 1})
	if len(reqs) != 1 {
		t.Errorf("MaxRequests=1 应只 1 条, got %d", len(reqs))
	}
}

func TestRenderCurl(t *testing.T) {
	reqs := []TestRequest{{
		Method: "POST", URL: "https://a.com/x",
		Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"},
		Query:   map[string]string{"page": "1"},
		Body:    `{"a":1}`,
	}}
	got := RenderCurl(reqs)
	if !strings.Contains(got, "curl -X POST 'https://a.com/x?page=1'") {
		t.Errorf("URL 应带 query: %s", got)
	}
	if !strings.Contains(got, "-H 'Authorization: Bearer ${TOKEN}'") {
		t.Errorf("头缺失: %s", got)
	}
	if !strings.Contains(got, `-d '{"a":1}'`) {
		t.Errorf("body 缺失: %s", got)
	}
}

func TestRenderPython(t *testing.T) {
	reqs := []TestRequest{{
		Method: "GET", URL: "https://a.com/x",
		Headers: map[string]string{"X-Trace": "1"},
	}}
	got := RenderPython(reqs)
	if !strings.Contains(got, "import requests") {
		t.Error("应有 import")
	}
	if !strings.Contains(got, "requests.get(") || !strings.Contains(got, `"https://a.com/x"`) {
		t.Errorf("方法映射错: %s", got)
	}
	if !strings.Contains(got, `"X-Trace": "1"`) {
		t.Errorf("头缺失: %s", got)
	}
}

func TestRenderPostman(t *testing.T) {
	reqs := []TestRequest{{
		Method: "POST", URL: "https://a.com/x",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"a":1}`,
	}}
	got := RenderPostman(reqs)
	for _, want := range []string{`"info"`, `"_postman_id"`, `"name": "basecrawler test requests"`, `"raw"`, `"url"`, `"method": "POST"`, `"body"`} {
		if !strings.Contains(got, want) {
			t.Errorf("collection 缺 %s: %s", want, got)
		}
	}
}

func TestRenderHTTP(t *testing.T) {
	reqs := []TestRequest{{
		Method: "GET", URL: "https://a.com/x",
		Headers: map[string]string{"X-Trace": "1"},
	}}
	got := RenderHTTP(reqs)
	if !strings.Contains(got, "GET https://a.com/x") || !strings.Contains(got, "X-Trace: 1") {
		t.Errorf("http 格式错: %s", got)
	}
}

func TestRenderDispatch(t *testing.T) {
	reqs := []TestRequest{{Method: "GET", URL: "https://a.com/x"}}
	for _, format := range []string{"curl", "python", "postman", "http", "json"} {
		got, err := Render(reqs, format)
		if err != nil || got == "" {
			t.Errorf("Render(%s) = %q, %v", format, got, err)
		}
	}
	if _, err := Render(reqs, "bogus"); err == nil {
		t.Error("未知格式应报错")
	}
}

// O-1 真实数据：文档站自身资产（同种子域）默认不进测试请求。
func TestGenerateRequestsSiteHosts(t *testing.T) {
	results := []basecrawler.Result{
		{URL: "https://api.github.com/user/repos", Method: "GET",
			Kind: "curl", StatusCode: 0, Raw: `curl https://api.github.com/user/repos -H "Authorization: Bearer <YOUR-TOKEN>"`},
		{URL: "https://docs.github.com/assets/cb-345/images/site/favicon.png", Method: "GET",
			StatusCode: 200, ContentType: "image/png"},
		{URL: "https://docs.github.com/manifest.json", Method: "GET",
			StatusCode: 200, ContentType: "application/json"},
	}
	reqs := GenerateRequests(results, Config{SiteHosts: []string{"docs.github.com"}})
	if len(reqs) != 1 {
		t.Fatalf("同种子域资产应被过滤, got %d: %+v", len(reqs), reqs)
	}
	if !strings.Contains(reqs[0].URL, "api.github.com") {
		t.Errorf("应保留外域接口: %q", reqs[0].URL)
	}
	// curl 结果不受 SiteHosts 影响（即使端点就在种子域）
	reqs2 := GenerateRequests(results, Config{SiteHosts: []string{"api.github.com", "docs.github.com"}})
	if len(reqs2) != 1 {
		t.Errorf("curl 结果不应被 SiteHosts 过滤: %d", len(reqs2))
	}
}

// O-1 数据：无 Authorization 头的 curl 命令被统一加认证头占位。
func TestGenerateRequestsAuthAddedWhenMissing(t *testing.T) {
	results := []basecrawler.Result{{
		URL: "https://api.github.com/repos/o/r", Method: "GET",
		Kind: "curl", StatusCode: 0,
		Raw: `curl https://api.github.com/repos/o/r -H "Accept: application/vnd.github+json"`,
	}}
	reqs := GenerateRequests(results, Config{AuthHeader: "Authorization: Bearer ${GITHUB_TOKEN}"})
	if reqs[0].Headers["Authorization"] != "Bearer ${GITHUB_TOKEN}" {
		t.Errorf("无认证头应加占位: %v", reqs[0].Headers)
	}
}

// O-1 真实数据：CSP/robots 等情报线索不进测试请求。判定改为语义标记
// （Confidence=clue），未标记的新来源按接口形状放行（fail-open）。
func TestGenerateRequestsSkipClueSources(t *testing.T) {
	results := []basecrawler.Result{
		{URL: "https://collector.githubapp.com", Method: "GET",
			StatusCode: 0, Source: "csp:report-to", Confidence: "clue"},
		{URL: "https://target/secret", Method: "GET",
			StatusCode: 0, Source: "robots", Confidence: "clue"},
		{URL: "https://target/api/items", Method: "GET",
			StatusCode: 200, Source: "js"},
		{URL: "https://target/new-endpoint", Method: "GET",
			StatusCode: 0, Source: "brand-new-source"},
	}
	reqs := GenerateRequests(results, Config{})
	if len(reqs) != 2 {
		t.Fatalf("线索应被跳过、其余保留, got %d: %+v", len(reqs), reqs)
	}
	joined := reqs[0].URL + " " + reqs[1].URL
	if !strings.Contains(joined, "/api/items") || !strings.Contains(joined, "/new-endpoint") {
		t.Errorf("接口形状与新增来源都应保留: %q", joined)
	}
}

// P-2 E2E 暴露的缺陷：SiteHosts 过滤此前把「同站的一切」都当站点
// 资产——表单 action 指向本站 /login 时（单页应用的 API 多在同站），
// 真正的接口被滤掉。修正：同站 + 资产路径才滤，接口路径不滤。
func TestSiteHostsFilterOnlyAssets(t *testing.T) {
	results := []basecrawler.Result{
		{URL: "https://site.com/login", Method: "GET", StatusCode: 0,
			Source: "form", Kind: "page"}, // 同站表单接口：不该滤
		{URL: "https://site.com/api/items", Method: "GET", StatusCode: 0,
			Source: "js"}, // 同站 JS 接口：不该滤
		{URL: "https://site.com/favicon.ico", Method: "GET", StatusCode: 200,
			Source: "link"}, // 站点资产：该滤
		{URL: "https://site.com/manifest.json", Method: "GET", StatusCode: 200,
			Source: "link"}, // 站点资产：该滤
		{URL: "https://site.com/_next/static/css/abc.css", Method: "GET",
			StatusCode: 200, Source: "link"}, // 站点资产：该滤
	}
	reqs := GenerateRequests(results, Config{SiteHosts: []string{"site.com"}})
	got := map[string]bool{}
	for _, r := range reqs {
		got[r.FromURL] = true
	}
	if !got["https://site.com/login"] {
		t.Error("同站表单 action 接口不应被 SiteHosts 过滤")
	}
	if !got["https://site.com/api/items"] {
		t.Error("同站 JS 接口不应被 SiteHosts 过滤")
	}
	for _, asset := range []string{
		"https://site.com/favicon.ico",
		"https://site.com/manifest.json",
		"https://site.com/_next/static/css/abc.css",
	} {
		if got[asset] {
			t.Errorf("站点资产应被过滤: %s", asset)
		}
	}
}

func TestLooksLikeAssetPath(t *testing.T) {
	cases := map[string]bool{
		"/favicon.ico":               true,
		"/manifest.json":             true,
		"/robots.txt":                true,
		"/sitemap.xml":               true,
		"/llms.txt":                  true,
		"/_next/static/css/a1b2.css": true,
		"/assets/app.9f8e7d.js":      true,
		"/img/logo.svg":              true,
		"/login":                     false,
		"/api/items":                 false,
		"/v1/users/USERNAME/repos":   false,
		"/data.json":                 false, // API 的 json 不是资产
	}
	for p, want := range cases {
		if got := looksLikeAssetPath(p); got != want {
			t.Errorf("looksLikeAssetPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// P-2：POST 表单的参数必须进 body（form-urlencoded），不是 query——
// 拼到 query 上会改变语义（M 批次代码注释里的既有约定，此前
// fromPlain 对 method 不分区，全部拼 query）。
func TestGenerateRequestsFormBodyForPost(t *testing.T) {
	results := []basecrawler.Result{{
		URL: "https://site.com/login", Method: "POST", StatusCode: 0,
		Source: "form", Attribute: "action", Confidence: "interface",
		Params: []string{"email", "password"},
		ParamValues: []extractor.ParamValue{
			{Name: "csrf", Kind: "hidden", Values: []string{"tok-abc"}},
		},
	}}
	reqs := GenerateRequests(results, Config{})
	if len(reqs) != 1 {
		t.Fatalf("应生成 1 条, got %d", len(reqs))
	}
	r := reqs[0]
	if len(r.Query) != 0 {
		t.Errorf("POST 表单参数不该进 query: %v", r.Query)
	}
	if r.Body == "" || !strings.Contains(r.Body, "csrf=") || strings.Contains(r.Body, "tok-abc") {
		t.Errorf("POST 表单参数应进 body，且 hidden 静态值应为占位符: %q", r.Body)
	}
	if !strings.Contains(r.Body, "email=") {
		t.Errorf("body 应含 email: %q", r.Body)
	}
}
