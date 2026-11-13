package basecrawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCrawlEndToEnd 用一个本地站点验证完整链路：
// 种子 → 页面链接 → JS 里的接口 → robots 线索 → sitemap 入口，以及去重与回调一致性。
func TestCrawlEndToEnd(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<a href="/a.html">a</a>
			<a href="/b.html">b</a>
			<script src="/app.js"></script>
		</body></html>`)
	})
	mux.HandleFunc("/a.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="/c.html">c</a></body></html>`)
	})
	mux.HandleFunc("/b.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>no links</body></html>`)
	})
	mux.HandleFunc("/c.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="/a.html">already seen</a></body></html>`)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `var api="/api/hidden/endpoint";`)
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "User-agent: *\nDisallow: /secret\nSitemap: /sitemap.xml\n")
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0"?><urlset><url><loc>/from-sitemap.html</loc></url></urlset>`)
	})
	mux.HandleFunc("/from-sitemap.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>sitemap page</body></html>")
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.Concurrency = 4
	opts.Timeout = 5 * time.Second

	var collected []Result
	opts.OnResult = func(r Result) { collected = append(collected, r) }

	c, err := New(opts)
	if err != nil {
		t.Fatalf("new crawler: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := c.Crawl(ctx, srv.URL+"/")
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}

	got := make(map[string]Result)
	for _, r := range results {
		got[r.URL] = r
	}

	expect := []string{
		srv.URL + "/",
		srv.URL + "/a.html",
		srv.URL + "/b.html",
		srv.URL + "/c.html",
		srv.URL + "/app.js",
		srv.URL + "/api/hidden/endpoint",
		srv.URL + "/from-sitemap.html",
		srv.URL + "/secret", // robots 线索，仅记录不抓取
	}
	for _, e := range expect {
		if _, ok := got[e]; !ok {
			t.Errorf("missing %s", e)
		}
	}

	// robots 线索必须标记为 StatusCode 0（表示未实际访问）
	if r, ok := got[srv.URL+"/secret"]; ok {
		if r.Source != "robots" || r.StatusCode != 0 {
			t.Errorf("robots clue = %+v, want Source=robots Status=0", r)
		}
	}
	// sitemap 入口的来源应被保留
	if r, ok := got[srv.URL+"/from-sitemap.html"]; ok && r.Source != "sitemap" {
		t.Errorf("sitemap entry source = %q, want sitemap", r.Source)
	}
	// 抓取过的页面必须有状态码
	if r, ok := got[srv.URL+"/a.html"]; ok && r.StatusCode != 200 {
		t.Errorf("/a.html status = %d, want 200", r.StatusCode)
	}
	// 去重：a.html 被 c.html 再次引用，不应重复出现
	n := 0
	for _, r := range results {
		if r.URL == srv.URL+"/a.html" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("/a.html appeared %d times, want 1", n)
	}
	// 回调与返回值条数应一致
	if len(collected) != len(results) {
		t.Errorf("OnResult called %d times, results %d", len(collected), len(results))
	}
}

// TestMaxURLs 验证配额生效：抓取数被限制，且不因取消而报错。
func TestMaxURLs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<html><body>
			<a href="/1">1</a><a href="/2">2</a><a href="/3">3</a>
			<a href="/4">4</a><a href="/5">5</a><a href="/6">6</a>
		</body></html>`)
			return
		}
		fmt.Fprint(w, "<html><body>page</body></html>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxURLs = 3
	opts.MaxDepth = 1
	opts.FollowRobots = false
	opts.FollowSitemap = false
	opts.Concurrency = 2

	var errCount int
	opts.OnError = func(url string, err error) { errCount++ }

	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	results, err := c.Crawl(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatalf("crawl returned error: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("fetched %d pages, want <= 3", len(results))
	}
	if errCount != 0 {
		t.Errorf("quota exhaustion should not report errors, got %d", errCount)
	}
}

// TestScopeAlias 确保对外暴露的 Scope 常量可直接用于 Options。
func TestScopeAlias(t *testing.T) {
	for _, s := range []Scope{ScopeSameHost, ScopeSubdomain, ScopeAny, ScopeDN, ScopeRegex} {
		opts := testNoProbeOptions()
		opts.Scope = s
		c, err := New(opts)
		if err != nil {
			t.Fatalf("New with scope %v: %v", s, err)
		}
		c.Close()
	}
	// CLI 新命名：fqdn / rdn / dn / regex / all
	for s, name := range map[Scope]string{
		ScopeSameHost: "fqdn", ScopeSubdomain: "rdn", ScopeAny: "all",
		ScopeDN: "dn", ScopeRegex: "regex",
	} {
		if s.String() != name {
			t.Errorf("Scope %d String() = %q，期望 %q", s, s.String(), name)
		}
	}
}

// TestChunkGraphEndToEnd 验证打包产物的 chunk 与路由恢复能贯通到结果里。
//
// 场景还原真实情况：HTML 只有一个入口 bundle，懒加载 chunk 和前端路由
// 都不在任何静态链接上，只能从 bundle 的运行时清单里读出来。
func TestChunkGraphEndToEnd(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><div id="app"></div>
			<script src="/static/js/app.js"></script></body></html>`)
	})

	bundle := `(function(){
	  var o={}; o.p="/static/js/";
	  o.u=function(e){return "chunk-"+e+"."+{101:"a1b2c3",102:"d4e5f6"}[e]+".js"};
	  var routes=[
	    {path:"/admin/reports",name:"reports",component:function(){return o.e(101)}},
	    {path:"/settings/profile",name:"profile",component:function(){return o.e(102)}},
	    {path:"/user/:id",name:"user",component:function(){return o.e(102)}}
	  ];
	  window.__routes=routes;
	})();`

	mux.HandleFunc("/static/js/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, bundle)
	})
	mux.HandleFunc("/static/js/chunk-101.a1b2c3.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `console.log("chunk 101");`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.RateLimit = 0
	opts.JSEngine = JSEngineGoja

	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	results, err := c.Crawl(ctx, srv.URL+"/")
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	byPath := map[string]Result{}
	for _, r := range results {
		byPath[r.URL] = r
	}
	base := srv.URL

	// 懒加载 chunk 必须被发现并抓取
	chunkURL := base + "/static/js/chunk-101.a1b2c3.js"
	r, ok := byPath[chunkURL]
	if !ok {
		t.Fatalf("未发现 chunk，实际: %v", keysOfResult(byPath))
	}
	if r.Kind != "chunk" {
		t.Errorf("chunk 的 kind = %q，期望 chunk", r.Kind)
	}
	if r.StatusCode != 200 {
		t.Errorf("chunk 状态码 = %d，期望 200", r.StatusCode)
	}

	// 带参数占位的路由只报告形状，不发请求
	paramURL := base + "/user/:id"
	pr, ok := byPath[paramURL]
	if !ok {
		t.Errorf("未发现参数路由 %s，实际: %v", paramURL, keysOfResult(byPath))
	} else {
		if pr.Kind != "route" {
			t.Errorf("参数路由 kind = %q，期望 route", pr.Kind)
		}
		if pr.StatusCode != 0 {
			t.Errorf("参数路由不应被抓取，状态码 = %d，期望 0", pr.StatusCode)
		}
	}

	// 前端路由必须被识别并打标记
	for _, want := range []string{"/admin/reports", "/settings/profile"} {
		u := base + want
		rr, ok := byPath[u]
		if !ok {
			t.Errorf("未发现路由 %s，实际: %v", want, keysOfResult(byPath))
			continue
		}
		if rr.Kind != "route" {
			t.Errorf("路由 %s 的 kind = %q，期望 route", want, rr.Kind)
		}
	}
}

func keysOfResult(m map[string]Result) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSitemapDoesNotStarveAssets 锁定「sitemap 不能吃光配额」。
//
// 实测问题：vuejs.org 的 sitemap 有 59 条，以 depth 0 与种子页同层入队，
// -max-urls 60 时配额被 sitemap 全部占满，种子页里的 <script src> 一条都没抓到，
// 结果里 JS 数为 0，看起来像「这个站没有 JS」。
//
// 现在 sitemap 页面放 depth 1，且第 0 层结束后才并入，排在种子页提取出的资源之后。
func TestSitemapDoesNotStarveAssets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><script src="/app.js"></script></body></html>`)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `fetch("/api/from-js");`)
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Sitemap: /sitemap.xml\n")
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0"?><urlset>`)
		for i := 0; i < 40; i++ {
			fmt.Fprintf(&sb, `<url><loc>/page-%d</loc></url>`, i)
		}
		sb.WriteString(`</urlset>`)
		fmt.Fprint(w, sb.String())
	})
	for i := 0; i < 40; i++ {
		p := fmt.Sprintf("/page-%d", i)
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><body>page</body></html>")
		})
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.MaxURLs = 16 // 远小于 sitemap 条目数（40）
	opts.Concurrency = 2
	opts.PerHostConcurrency = 2
	opts.RateLimit = 0

	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	results, err := c.Crawl(ctx, srv.URL+"/")
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	byURL := map[string]Result{}
	for _, r := range results {
		byURL[r.URL] = r
	}

	// 核心断言：脚本必须被抓到，且它是 JS 里发现的接口的跳板
	if r, ok := byURL[srv.URL+"/app.js"]; !ok {
		t.Fatalf("配额被 sitemap 吃光，/app.js 未被抓到；实际 %v", keysOfResult(byURL))
	} else if r.StatusCode != 200 {
		t.Errorf("/app.js 状态码 = %d，期望 200", r.StatusCode)
	}
	// 说明：这里不断言 js 内发现的 /api/from-js——
	// 它在 depth 2，而配额 16 在 depth 1（40 条 sitemap）就已用尽。
	// 本用例只验证「资源优先于 sitemap」，js 内提取由 TestCrawlEndToEnd 覆盖。

	// sitemap 页面应当落在 depth 1
	for u, r := range byURL {
		if strings.Contains(u, "/page-") && r.Depth != 1 {
			t.Errorf("sitemap 页面 %s 的 depth = %d，期望 1", u, r.Depth)
		}
	}
}

// TestCrawlerOneInstancePerCrawl 锁定「一实例一采集」契约：
// 第二次 Crawl 必须显式报错，而不是把上一次的结果/已见集合悄悄累积进去。
func TestCrawlerOneInstancePerCrawl(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="/a.html">a</a></body></html>`)
	})
	mux.HandleFunc("/a.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>ok</body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.Concurrency = 2
	opts.Timeout = 5 * time.Second

	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	first, err := c.Crawl(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("第一次 Crawl: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("第一次 Crawl 应有结果")
	}

	_, err = c.Crawl(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("第二次 Crawl 应报错（一实例一采集）")
	}
	if want := "one instance per crawl"; !strings.Contains(err.Error(), want) {
		t.Errorf("错误应含 %q, 实际: %v", want, err)
	}

	// Closed 之后仍是「已关闭」错误，而不是「已使用」
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := c.Crawl(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("关闭后 Crawl 应报 closed, 实际: %v", err)
	}
}

// TestCurlBlockEmit 验证 HTML 代码块里的 curl 命令被发射为 Kind=curl 结果：
// 携带结构化 URL/Method/Raw，StatusCode=0（未请求），且端点不被真的抓取。
func TestCurlBlockEmit(t *testing.T) {
	var endpointHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h1>API docs</h1>
			<pre><code>curl https://api.example.com/v1/chat \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "hi"}]}'</code></pre>
			<a href="/page2.html">next</a>
		</body></html>`)
	})
	mux.HandleFunc("/page2.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>page2</body></html>`)
	})
	mux.HandleFunc("/v1/chat", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&endpointHits, 1)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 1
	opts.Concurrency = 2
	opts.Timeout = 5 * time.Second
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	results, err := c.Crawl(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	var curl *Result
	for i := range results {
		if results[i].Kind == "curl" {
			curl = &results[i]
			break
		}
	}
	if curl == nil {
		t.Fatal("应发射 Kind=curl 结果")
	}
	if curl.URL != "https://api.example.com/v1/chat" {
		t.Errorf("URL: got %q", curl.URL)
	}
	if curl.Method != "POST" {
		t.Errorf("Method: got %q", curl.Method)
	}
	if curl.StatusCode != 0 {
		t.Errorf("StatusCode 应为 0（未请求）, got %d", curl.StatusCode)
	}
	if !strings.Contains(curl.Raw, "Authorization: Bearer $API_KEY") {
		t.Errorf("Raw 应保真: %q", curl.Raw)
	}
	if curl.Source != "code:curl" {
		t.Errorf("Source: got %q", curl.Source)
	}
	if n := atomic.LoadInt32(&endpointHits); n != 0 {
		t.Errorf("外域 endpoint 不应被请求, 实际 %d 次", n)
	}
}

// TestFormActionEmitOnly 锁定 P-2 修正：表单 action（POST）只作为
// 「接口形状」发射，不入队抓取——爬虫抓取语义是 GET，POST action
// 入队只会得到 404 噪音（I-1 数据）；method+参数是 testfill 填充
// 发送的输入（Confidence=interface，StatusCode=0）。
func TestFormActionEmitOnly(t *testing.T) {
	var actionHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
<form action="/login" method="post">
  <input name="email" type="email">
  <input name="csrf" type="hidden" value="tok-abc">
  <input type="submit" value="Go">
</form>
<a href="/page2.html">next</a>
</body></html>`)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&actionHits, 1)
	})
	mux.HandleFunc("/page2.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>page2</body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.Concurrency = 2
	opts.Timeout = 5 * time.Second
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	results, err := c.Crawl(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	var form *Result
	for i := range results {
		if results[i].Attribute == "action" && results[i].Method == "POST" {
			form = &results[i]
			break
		}
	}
	if form == nil {
		t.Fatal("应发射 POST 表单 action 结果")
	}
	if form.StatusCode != 0 {
		t.Errorf("StatusCode 应为 0（未请求）, got %d", form.StatusCode)
	}
	if form.Confidence != "interface" {
		t.Errorf("Confidence = %q, want interface", form.Confidence)
	}
	if len(form.ParamValues) != 2 {
		t.Errorf("ParamValues 应带 2 个（email/csrf）, got %+v", form.ParamValues)
	}
	if n := atomic.LoadInt32(&actionHits); n != 0 {
		t.Errorf("POST action 不应被抓取, 实际 %d 次", n)
	}
}

func TestInsidePathFiltersRobotsClues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="/docs/page">in path</a></body></html>`)
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /outside/secret\nDisallow: /docs/hidden\n")
	})
	srv := httptest.NewServer(mux)

	opts := testNoProbeOptions()
	opts.InsidePath = true
	opts.Scope = ScopeSameHost
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var clues []Result
	res, err := c.Crawl(context.Background(), srv.URL+"/docs/")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Source == "robots" {
			clues = append(clues, r)
		}
	}
	if len(clues) != 1 || !strings.HasSuffix(clues[0].URL, "/docs/hidden") {
		t.Errorf("robots 线索 = %v，期望只剩路径内的 /docs/hidden", clues)
	}
}

func TestCrawlerInScopeOutputFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head>
<link rel="stylesheet" href="https://cdn.other.com/lib.css">
<script src="https://cdn.other.com/app.js"></script>
</head><body><a href="/page2">local</a></body></html>`)
	})
	srv := httptest.NewServer(mux)

	opts := testNoProbeOptions()
	opts.Scope = ScopeSameHost
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// 种子在 Crawl 开始时才登记，所以 InScope 的判定也在爬取过程中做。
	var seenLocal, seenOther bool
	opts.OnResult = func(r Result) {
		if strings.Contains(r.URL, "cdn.other.com") {
			seenOther = true
		}
		if strings.Contains(r.URL, "/page2") {
			seenLocal = true
		}
		if strings.Contains(r.URL, "cdn.other.com") && c.InScope(r.URL) {
			t.Errorf("范围外结果 %s 不应通过 InScope", r.URL)
		}
		if strings.Contains(r.URL, srv.URL) && !c.InScope(r.URL) {
			t.Errorf("范围内结果 %s 不应被 InScope 拒绝", r.URL)
		}
	}
	if _, err := c.Crawl(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if !seenLocal {
		t.Errorf("同 host 结果应产出")
	}
	if seenOther {
		t.Errorf("same-host 下跨域资产本就不该入队抓取")
	}
	if c.InScope("::::not-a-url") {
		t.Errorf("非法 URL 应判 false")
	}
}

func TestMaxPagesPerHost(t *testing.T) {
	var pages int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// robots/sitemap 由引导流程抓取，不计入页面数
		if !strings.HasSuffix(r.URL.Path, ".txt") && !strings.HasSuffix(r.URL.Path, ".xml") {
			atomic.AddInt32(&pages, 1)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><a href="/p%d">next</a></body></html>`, atomic.LoadInt32(&pages))
	})
	srv := httptest.NewServer(mux)

	opts := testNoProbeOptions()
	opts.MaxPagesPerHost = 3
	opts.MaxDepth = 5
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Crawl(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&pages); got > 3 {
		t.Errorf("抓取页面数 = %d，超过 -mdp 3 的上限", got)
	}
}
