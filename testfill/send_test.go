package testfill

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/winezer0/basecrawler/fetcher"
	"github.com/winezer0/basecrawler/safety"
)

func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"method":"` + r.Method + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testFetcher(t *testing.T) *fetcher.Fetcher {
	t.Helper()
	f, err := fetcher.New(fetcher.Options{
		Timeout:     5 * time.Second,
		MaxBodySize: 1 << 20,
		Retries:     0,
	})
	if err != nil {
		t.Fatalf("fetcher.New: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// TestSendRequestsSafeMethods GET 默认发送并带回状态码。
func TestSendRequestsSafeMethods(t *testing.T) {
	srv := echoServer(t)
	u, _ := url.Parse(srv.URL + "/api/ping")
	reqs := []TestRequest{{FromURL: srv.URL, Method: "GET", URL: u.String()}}
	cfs := SendRequests(context.Background(), reqs, SendOptions{Fetcher: testFetcher(t)})
	if len(cfs) != 1 {
		t.Fatalf("应 1 条, got %d", len(cfs))
	}
	if !cfs[0].Sent || cfs[0].StatusCode != 200 {
		t.Errorf("GET 应发送且 200: %+v", cfs[0])
	}
}

// TestSendRequestsUnsafeBlocked 非幂等方法默认跳过，AllowUnsafe 后才发。
func TestSendRequestsUnsafeBlocked(t *testing.T) {
	srv := echoServer(t)
	u, _ := url.Parse(srv.URL + "/api/orders")
	reqs := []TestRequest{{
		FromURL: srv.URL, Method: "POST", URL: u.String(),
		Body: `{"item":1}`,
	}}

	blocked := SendRequests(context.Background(), reqs, SendOptions{Fetcher: testFetcher(t)})
	if blocked[0].Sent {
		t.Error("POST 默认不应发送")
	}
	if blocked[0].SkipReason == "" {
		t.Error("应带跳过原因")
	}
	if !strings.Contains(blocked[0].SkipReason, "AllowUnsafe") {
		t.Errorf("原因应指向 AllowUnsafe: %q", blocked[0].SkipReason)
	}

	allowed := SendRequests(context.Background(), reqs,
		SendOptions{Fetcher: testFetcher(t), AllowUnsafe: true})
	if !allowed[0].Sent || allowed[0].StatusCode != 200 {
		t.Errorf("AllowUnsafe 后 POST 应发送: %+v", allowed[0])
	}
}

// TestSendRequestsNoFetcher 无抓取器时全部跳过（不 panic）。
func TestSendRequestsNoFetcher(t *testing.T) {
	cfs := SendRequests(context.Background(),
		[]TestRequest{{Method: "GET", URL: "https://a.com/x"}}, SendOptions{})
	if len(cfs) != 1 || cfs[0].Sent || cfs[0].SkipReason == "" {
		t.Errorf("无 fetcher 应跳过: %+v", cfs)
	}
}

// TestSendRequestsMaxSend 条数上限。
func TestSendRequestsMaxSend(t *testing.T) {
	srv := echoServer(t)
	u, _ := url.Parse(srv.URL)
	reqs := []TestRequest{
		{Method: "GET", URL: u.String() + "/1"},
		{Method: "GET", URL: u.String() + "/2"},
	}
	cfs := SendRequests(context.Background(), reqs,
		SendOptions{Fetcher: testFetcher(t), MaxSend: 1})
	if len(cfs) != 2 {
		t.Fatalf("应返回 2 条确认, got %d", len(cfs))
	}
	if !cfs[0].Sent {
		t.Error("第一条应发送")
	}
	if cfs[1].Sent || cfs[1].SkipReason == "" {
		t.Errorf("第二条应被上限跳过: %+v", cfs[1])
	}
}

// TestSafeMethod 锁定 testfill 依赖的幂等方法表（统一由 safety 公共工具包提供）。
func TestSafeMethod(t *testing.T) {
	for _, m := range []string{"GET", "HEAD", "OPTIONS", "get", "Head"} {
		if !safety.IsSafeMethod(m) {
			t.Errorf("%q 应安全", m)
		}
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", "", "  "} {
		if safety.IsSafeMethod(m) {
			t.Errorf("%q 应不安全", m)
		}
	}
}

// TestRenderConfirmedCurl 确认注释进 curl 产物。
func TestRenderConfirmedCurl(t *testing.T) {
	cfs := []Confirmation{
		{TestRequest: TestRequest{Method: "GET", URL: "https://a.com/x"}, Sent: true, StatusCode: 200},
		{TestRequest: TestRequest{Method: "POST", URL: "https://a.com/y", Body: `{"a":1}`},
			SkipReason: "非幂等方法需 AllowUnsafe"},
	}
	got, err := RenderConfirmed("curl", cfs)
	if err != nil {
		t.Fatalf("RenderConfirmed: %v", err)
	}
	if !strings.Contains(got, "# → 200") {
		t.Errorf("应有 200 确认注释: %s", got)
	}
	if !strings.Contains(got, "# 跳过: 非幂等方法需 AllowUnsafe") {
		t.Errorf("应有跳过注释: %s", got)
	}
	// 未确认的渲染（Render）不应带注释
	plain, _ := Render(cfsToReqs(cfs), "curl")
	if strings.Contains(plain, "# →") {
		t.Errorf("Render 不应带确认注释: %s", plain)
	}
}
