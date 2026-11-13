package fetcher

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 表驱动：重定向后的方法切换规则（纯函数）。
// 301/302/303 历史上允许改为 GET；307/308 必须保留原方法与 body。
func TestRedirectMethod(t *testing.T) {
	cases := []struct {
		orig     string
		code     int
		wantM    string
		wantKeep bool // 是否保留 body
	}{
		{"POST", 301, "GET", false},
		{"POST", 302, "GET", false},
		{"POST", 303, "GET", false},
		{"POST", 307, "POST", true},
		{"POST", 308, "POST", true},
		{"GET", 301, "GET", true},
		{"DELETE", 302, "GET", false},
		{"PUT", 308, "PUT", true},
	}
	for _, tc := range cases {
		gotM, gotKeep := redirectMethod(tc.orig, tc.code)
		if gotM != tc.wantM || gotKeep != tc.wantKeep {
			t.Errorf("redirectMethod(%s, %d) = (%s, %v), want (%s, %v)",
				tc.orig, tc.code, gotM, gotKeep, tc.wantM, tc.wantKeep)
		}
	}
}

// TestSendMethodAndBody 验证 Send 携带方法与 body 到达服务端。
func TestSendMethodAndBody(t *testing.T) {
	var gotMethod, gotBody, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotBody, gotHeader = r.Method, string(b), r.Header.Get("X-Trace")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	f, err := New(Options{Timeout: 5 * time.Second, MaxBodySize: 1 << 20, Retries: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.Close()

	u, _ := url.Parse(srv.URL + "/api/echo")
	resp, err := f.Send(context.Background(), Request{
		Method:  "POST",
		URL:     u,
		Headers: map[string]string{"X-Trace": "t-1", "Content-Type": "application/json"},
		Body:    `{"a":1}`,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if gotMethod != "POST" || gotBody != `{"a":1}` || gotHeader != "t-1" {
		t.Errorf("服务端收到: method=%q body=%q header=%q", gotMethod, gotBody, gotHeader)
	}
	if !strings.Contains(string(resp.Body), `"ok"`) {
		t.Errorf("body: %s", resp.Body)
	}
}

// TestSendDefaultsToGET 零值 Method 按 GET 发送。
func TestSendDefaultsToGET(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
	}))
	defer srv.Close()

	f, _ := New(Options{Timeout: 5 * time.Second})
	defer f.Close()
	u, _ := url.Parse(srv.URL)
	if _, err := f.Send(context.Background(), Request{URL: u}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != "GET" {
		t.Errorf("默认方法: %q", gotMethod)
	}
}

// TestSendRedirectPreserves307 验证 307 保留方法与 body、301 降级为 GET。
func TestSendRedirectPreserves307(t *testing.T) {
	var methods, bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		methods = append(methods, r.Method)
		bodies = append(bodies, string(b))
		w.WriteHeader(200)
	})
	mux.HandleFunc("/r307", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/r301", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f, _ := New(Options{Timeout: 5 * time.Second, MaxRedirects: 3})
	defer f.Close()

	u307, _ := url.Parse(srv.URL + "/r307")
	if _, err := f.Send(context.Background(), Request{Method: "POST", URL: u307, Body: "keep-me"}); err != nil {
		t.Fatalf("307 Send: %v", err)
	}
	u301, _ := url.Parse(srv.URL + "/r301")
	if _, err := f.Send(context.Background(), Request{Method: "POST", URL: u301, Body: "drop-me"}); err != nil {
		t.Fatalf("301 Send: %v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("应两次到达 final, got %v", methods)
	}
	if methods[0] != "POST" || bodies[0] != "keep-me" {
		t.Errorf("307 应保留方法与 body: %q %q", methods[0], bodies[0])
	}
	if methods[1] != "GET" || bodies[1] != "" {
		t.Errorf("301 应降级 GET 且丢 body: %q %q", methods[1], bodies[1])
	}
}

// TestSendNilURL 零值防御。
func TestSendNilURL(t *testing.T) {
	f, _ := New(Options{})
	defer f.Close()
	if _, err := f.Send(context.Background(), Request{}); err == nil {
		t.Error("nil URL 应报错")
	}
}

// TestGetStillWorks 确认 Get 委托后行为不变。
func TestGetStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Get 应发 GET, got %s", r.Method)
		}
		w.Write([]byte("hi"))
	}))
	defer srv.Close()
	f, _ := New(Options{Timeout: 5 * time.Second})
	defer f.Close()
	u, _ := url.Parse(srv.URL)
	resp, err := f.Get(context.Background(), u, "")
	if err != nil || string(resp.Body) != "hi" {
		t.Fatalf("Get: %v %s", err, resp.Body)
	}
}
