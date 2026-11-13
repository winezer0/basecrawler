package basecrawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/winezer0/basecrawler/frontier"
)

// TestReserveDispatch：同址只登记一次，不同址互不影响。
func TestReserveDispatch(t *testing.T) {
	c, err := New(testNoProbeOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	u1, _ := url.Parse("https://x/shared")
	u2, _ := url.Parse("https://x/other")
	if !c.reserveDispatch(u1) {
		t.Error("首次派发应放行")
	}
	if c.reserveDispatch(u1) {
		t.Error("同址第二次派发应被挡")
	}
	if !c.reserveDispatch(u2) {
		t.Error("不同地址不应被误挡")
	}
	if c.reserveDispatch(nil) {
		t.Error("nil URL 应拒绝")
	}
}

// TestRunLevelDispatchDedupe：即使上游漏了层内去重，同一 URL 也只抓一次，
// 不重复占用 MaxURLs 配额。
func TestRunLevelDispatchDedupe(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>ok</body></html>")
	}))
	defer srv.Close()

	c, err := New(testNoProbeOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	u, ok := frontier.Normalize(srv.URL+"/shared", nil)
	if !ok {
		t.Fatal("URL 归一化失败")
	}
	// 故意构造同层重复任务，绕过 dedupeTasks，直接验证派发通道兜底。
	tasks := []task{
		{url: u, depth: 0, source: "test", method: "GET"},
		{url: u, depth: 0, source: "test", method: "GET"},
	}
	c.runLevel(context.Background(), tasks)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("同一 URL 应只抓一次，实际 %d 次", got)
	}
}
