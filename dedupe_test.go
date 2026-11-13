package basecrawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func dedupeTask(p, source string) task {
	u, _ := url.Parse("https://example.com" + p)
	return task{url: u, source: source}
}

// TestDedupeTasksKeepsFirst 同层重复 URL 只保留第一次出现的那条。
//
// R-0 实测（vite.dev）：/ 与 /blog 在同一层各有 2 条任务，来源分别是
// seed/a 与 a/js-ast。保留首个即保留「最先发现它」的上下文。
func TestDedupeTasksKeepsFirst(t *testing.T) {
	in := []task{
		dedupeTask("/", "seed"),
		dedupeTask("/blog", "a"),
		dedupeTask("/", "a"),
		dedupeTask("/blog", "js-ast"),
		dedupeTask("/docs", "a"),
	}
	got := dedupeTasks(in)
	if len(got) != 3 {
		t.Fatalf("去重后 %d 条，期望 3 条: %v", len(got), paths(got))
	}
	wantPaths := []string{"/", "/blog", "/docs"}
	wantSources := []string{"seed", "a", "a"}
	for i := range wantPaths {
		if got[i].url.Path != wantPaths[i] || got[i].source != wantSources[i] {
			t.Fatalf("位置 %d = %s/%s，期望 %s/%s",
				i, got[i].url.Path, got[i].source, wantPaths[i], wantSources[i])
		}
	}
}

// TestDedupeTasksKeepsDroppingNil nil URL 的任务（种子解析失败时的残留）直接丢弃。
func TestDedupeTasksKeepsDroppingNil(t *testing.T) {
	got := dedupeTasks([]task{{url: nil}, dedupeTask("/a", "a")})
	if len(got) != 1 || got[0].url == nil {
		t.Fatalf("got %v", got)
	}
}

func TestDedupeTasksShortInput(t *testing.T) {
	if got := dedupeTasks(nil); len(got) != 0 {
		t.Fatalf("nil 应返回空，得到 %v", got)
	}
	single := []task{dedupeTask("/a", "a")}
	if got := dedupeTasks(single); len(got) != 1 {
		t.Fatalf("单条应原样返回，得到 %v", got)
	}
}

func paths(ts []task) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if t.url != nil {
			out = append(out, t.url.Path)
		}
	}
	return out
}

// TestCrawlFetchesSameLayerDuplicateOnce 端到端锁定配额不被同层重复吃掉。
//
// 首页与 /hub 都链接到 /shared：两层父页落在同一层时，未去重的实现会给
// /shared 发两次请求并产出两条结果（R-0 在 vite.dev 上实测到）。
func TestCrawlFetchesSameLayerDuplicateOnce(t *testing.T) {
	var sharedHits int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="/hub">hub</a><a href="/shared">shared</a>`)
	})
	mux.HandleFunc("/hub", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="/shared">shared</a>`)
	})
	mux.HandleFunc("/shared", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sharedHits, 1)
		fmt.Fprint(w, `<p>shared</p>`)
	})

	opts := testNoProbeOptions()
	opts.MaxDepth = 2
	opts.Concurrency = 4
	opts.Timeout = 5 * time.Second
	opts.MaxURLs = 50
	opts.Scope = ScopeSameHost
	opts.FollowRobots = false
	opts.FollowSitemap = false

	c, err := New(opts)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer c.Close()

	res, err := c.Crawl(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}

	if n := atomic.LoadInt32(&sharedHits); n != 1 {
		t.Errorf("/shared 被抓 %d 次，期望 1 次（同层重复吃配额）", n)
	}
	var rows int
	for _, r := range res {
		if r.URL == srv.URL+"/shared" {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("/shared 出现 %d 条结果，期望 1 条", rows)
	}
}
