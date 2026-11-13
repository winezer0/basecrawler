package basecrawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
)

// ExampleCrawler 演示最基础的库用法：本地起一个页面服务，采集并按序打印结果。
// robots / sitemap 在这里关掉，让输出只取决于示例页面本身。
func ExampleCrawler() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="/page.html">next</a>
			<script src="/app.js"></script></body></html>`)
	})
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>done</body></html>`)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		// 注意：AST 引擎按「段数 ≥3 或末段含扩展名」判定端点，
		// 2 段路径（如 /api/v1/orders）只有正则引擎会提取——见 ISSUES 观察项。
		fmt.Fprint(w, `var api="/api/v1/orders";`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.FollowRobots = false
	opts.FollowSitemap = false
	// OnResult 由库内部串行化，回调里收集即可，不需要额外加锁。
	// 注意回调到达顺序与并发调度有关，参与输出比对前要先排序。
	var paths []string
	opts.OnResult = func(r Result) {
		paths = append(paths, r.URL[len(srv.URL):]+"\t"+r.Source)
	}

	c, err := New(opts)
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer c.Close()

	results, err := c.Crawl(context.Background(), srv.URL)
	if err != nil {
		fmt.Println("crawl:", err)
		return
	}
	sort.Strings(paths)
	fmt.Println("total:", len(results))
	for _, p := range paths {
		fmt.Println(p)
	}
	// Output:
	// total: 4
	// /	seed
	// /api/v1/orders	js-ast
	// /app.js	script
	// /page.html	a
}
