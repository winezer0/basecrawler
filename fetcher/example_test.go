package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"
)

// ExampleFetcher 演示外部调用方单独使用抓取层：
// 连接复用、按 host 限并发、重定向链记录都在这一层。
func ExampleFetcher() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello")
	}))
	defer srv.Close()

	f, err := New(Options{
		Timeout:            5 * time.Second,
		MaxBodySize:        1 << 20,
		Retries:            1,
		PerHostConcurrency: 2,
		UserAgent:          "example/1.0",
	})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer f.Close()

	u, _ := url.Parse(srv.URL)
	resp, err := f.Get(context.Background(), u, "")
	if err != nil {
		fmt.Println("get:", err)
		return
	}
	fmt.Println("status:", resp.StatusCode)
	fmt.Println("content-type:", resp.ContentType)
	fmt.Println("body:", string(resp.Body))
	// Output:
	// status: 200
	// content-type: text/plain
	// body: hello
}

// ExampleFetcher_redirects 演示重定向链：每一跳的 Location 都是资产，
// Response.Redirects 原样带回，调用方自行决定要不要入队。
func ExampleFetcher_redirects() {
	mux := http.NewServeMux()
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "arrived")
	})
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f, err := New(Options{Timeout: 5 * time.Second, MaxRedirects: 5})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer f.Close()

	u, _ := url.Parse(srv.URL + "/start")
	resp, err := f.Get(context.Background(), u, "")
	if err != nil {
		fmt.Println("get:", err)
		return
	}
	fmt.Println("status:", resp.StatusCode)
	fmt.Println("final path:", resp.URL.Path)
	for i, loc := range resp.Redirects {
		// Location 是绝对地址；端口每次不同，只取路径部分
		hop, err := url.Parse(loc)
		if err != nil {
			fmt.Println("hop parse:", err)
			return
		}
		fmt.Printf("hop %d: %s\n", i+1, hop.Path)
	}
	// Output:
	// status: 200
	// final path: /final
	// hop 1: /final
}
