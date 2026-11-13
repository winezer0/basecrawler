package basecrawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRobotsAllowClueAndWildcardLimit：Allow 路径要输出为线索，含 *$ 的
// 通配路径跳过量要进 LimitReport，不再静默吞掉。
func TestRobotsAllowClueAndWildcardLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /admin\nDisallow: /private/*\nAllow: /public/api\n")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>ok</body></html>")
	}))
	defer srv.Close()

	opts := testNoProbeOptions()
	opts.FollowRobots = true
	opts.FollowSitemap = false
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	results, err := c.Crawl(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	allowSeen := false
	for _, r := range results {
		if r.Source == "robots:allow" && strings.HasSuffix(r.URL, "/public/api") {
			allowSeen = true
			if r.StatusCode != 0 {
				t.Errorf("Allow 线索应为未访问（StatusCode=0），得到 %d", r.StatusCode)
			}
			if r.Confidence != "clue" {
				t.Errorf("Allow 线索应带 Confidence=clue，得到 %q", r.Confidence)
			}
		}
	}
	if !allowSeen {
		t.Errorf("robots Allow 未输出为线索，结果: %+v", results)
	}

	limitSeen := false
	for _, e := range c.Limits() {
		if string(e.Kind) == "robots-wildcards" {
			limitSeen = true
			if e.Truncated <= 0 {
				t.Errorf("通配路径跳过量应被统计，得到 %+v", e)
			}
		}
	}
	if !limitSeen {
		t.Errorf("LimitReport 缺少 robots-wildcards 统计: %+v", c.Limits())
	}
}
