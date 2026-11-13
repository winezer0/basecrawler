package basecrawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winezer0/basecrawler/routegraph"
)

// esmExecBundle 同时包含显式路由与运行时 map 生成的路由：
// 静态解析只能看到 /static，执行兜底才拿得到 /billing 等。
// 这是「ESM/Vite 在执行链上是否真的接通」的夹具，不直接调 ExtractByExec。
const esmExecBundle = `import { createRouter, createWebHistory } from "vue-router";
const sections = ["billing", "audit", "settings"];
const dynamicRoutes = sections.map(function (s) { return { path: "/" + s, name: s }; });
export const routes = [
  { path: "/static", name: "static", component: function () { return import("./static.js"); } }
].concat(dynamicRoutes);
export const router = createRouter({ history: createWebHistory(), routes: routes });
`

// runESMExecCrawl 起一个本地 ESM 站点，走完整 Crawler 链路。
func runESMExecCrawl(t *testing.T, routeExec bool) []Result {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><script type="module" src="/app.mjs"></script></body></html>`)
	})
	mux.HandleFunc("/app.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, esmExecBundle)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.MaxURLs = 100
	opts.Concurrency = 4
	opts.Timeout = 5 * time.Second
	opts.RouteExec = routeExec
	opts.DeobfExec = false // 本用例只验证路由执行链
	opts.FollowRobots = false
	opts.FollowSitemap = false

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
	return results
}

// TestCrawlerESMExecDefaultOn：默认 RouteExec 下，ESM bundle 里
// 运行时生成的路由能进入完整采集结果，且显式路由不因静态非空而丢失。
func TestCrawlerESMExecDefaultOn(t *testing.T) {
	if !testNoProbeOptions().RouteExec {
		t.Fatal("RouteExec 默认值应为 true")
	}
	rs := runESMExecCrawl(t, true)
	// 由 httptest 生成的 host 不固定，按路径后缀找。
	for _, want := range []string{"/static", "/billing", "/audit", "/settings"} {
		found := false
		for _, r := range rs {
			if strings.HasSuffix(r.URL, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("采集结果缺 %s（共 %d 条）", want, len(rs))
		}
	}
	for _, r := range rs {
		if strings.HasSuffix(r.URL, "/billing") && !strings.Contains(r.Source, "exec") {
			t.Errorf("/billing 不是执行兜底产出，Source=%q", r.Source)
		}
	}
}

// TestCrawlerESMExecExplicitOff：--no-route-exec 对应的库层开关能关掉执行。
func TestCrawlerESMExecExplicitOff(t *testing.T) {
	rs := runESMExecCrawl(t, false)
	for _, r := range rs {
		if strings.HasSuffix(r.URL, "/billing") || strings.HasSuffix(r.URL, "/audit") {
			t.Errorf("关闭执行后仍有动态路由 %s（Source=%q）", r.URL, r.Source)
		}
	}
	foundStatic := false
	for _, r := range rs {
		if strings.HasSuffix(r.URL, "/static") {
			foundStatic = true
		}
	}
	if !foundStatic {
		t.Error("关闭执行不应影响静态路由 /static")
	}
}

// TestMergeExecResultChunkIDMapping：执行兜底只给 webpack 数字 ID 时，
// 用静态 u 函数表的 ID→文件名映射补上 Path，否则无法入队抓取。
func TestMergeExecResultChunkIDMapping(t *testing.T) {
	dst := routegraph.Result{
		Chunks: []routegraph.Chunk{
			{ID: "101", Path: "chunk-101.a.js", Source: "webpack.u"},
			{ID: "102", Path: "chunk-102.b.js", Source: "webpack.u"},
			{ID: "104", Path: "chunk-104.c.js", Source: "webpack.u"},
		},
		Routes: []routegraph.Route{{Path: "/static", Source: "route-table"}},
	}
	src := routegraph.Result{
		Chunks: []routegraph.Chunk{
			{ID: "104", Source: "exec.e"},              // 由静态 ID→文件名映射对齐后去重
			{ID: "999", Source: "exec.e"},              // 静态表无映射，跳过
			{Path: "./lazy.js", Source: "exec.import"}, // 新增可抓候选
		},
		Routes: []routegraph.Route{
			{Path: "/static", Source: "exec"}, // 重复
			{Path: "/dynamic", Source: "exec"},
		},
	}
	mergeExecResult(&dst, src)
	// 静态 3 条 + exec.import 新增 1 条；104 映射后去重、999 无映射跳过。
	if len(dst.Chunks) != 4 {
		t.Errorf("chunk 合并结果 %d 条，期望 4，得到 %v", len(dst.Chunks), dst.Chunks)
	}
	found := false
	for _, c := range dst.Chunks {
		if c.Path == "./lazy.js" {
			found = true
		}
	}
	if !found {
		t.Errorf("带 Path 的执行 chunk 未并入，得到 %v", dst.Chunks)
	}
	if len(dst.Routes) != 2 {
		t.Errorf("route 合并应去重，得到 %v", dst.Routes)
	}
}

// TestCrawlerESMExecStaticPlusDynamic：静态与执行结果合并去重的语义。
// 同一路径不得在结果里出现两次（静态优先）。
func TestCrawlerESMExecStaticPlusDynamic(t *testing.T) {
	rs := runESMExecCrawl(t, true)
	seen := map[string]int{}
	for _, r := range rs {
		if strings.HasSuffix(r.URL, "/static") || strings.HasSuffix(r.URL, "/billing") {
			seen[r.URL]++
		}
	}
	for u, n := range seen {
		if n != 1 {
			t.Errorf("%s 重复出现 %d 次", u, n)
		}
	}
}
