package basecrawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFuzzMissNotEmitted 锁定「fuzz 字典探测只保留有效路径」：
// 命中的 200 路径进入结果，404 miss 既不产出结果也不做正文提取。
// 这是 CLI 输出过滤之前的第一道闸门（crawler.go isProbeSource 分支）。
func TestFuzzMissNotEmitted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>seed</body></html>`)
	})
	mux.HandleFunc("/exists", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.MaxDepth = 1
	opts.Concurrency = 2
	opts.Timeout = 5 * time.Second
	opts.FollowRobots = false
	opts.FollowSitemap = false
	opts.RouteExec = false
	opts.DeobfExec = false
	opts.FuzzPaths = true
	opts.FuzzDict = []string{"/exists", "/missing"}

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

	foundHit := false
	for _, r := range results {
		if strings.HasSuffix(r.URL, "/missing") {
			t.Errorf("fuzz 未命中路径不应产出结果：%+v", r)
		}
		if strings.HasSuffix(r.URL, "/exists") && r.Source == "fuzz" {
			foundHit = true
			if r.StatusCode != 200 {
				t.Errorf("fuzz 命中结果状态码 = %d，期望 200", r.StatusCode)
			}
		}
	}
	if !foundHit {
		t.Errorf("fuzz 命中的 200 路径未产出，结果 %v", results)
	}
}
