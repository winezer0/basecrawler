package testfill

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winezer0/basecrawler/extractor"
	"github.com/winezer0/basecrawler/fetcher"
	"github.com/winezer0/basecrawler/safety"
)

// TestGenerateRequestsHiddenMasked：默认不把 hidden 的静态真实值填进 body，
// 显式 UseHiddenValues 才使用原值；字段名要进入 Fields 供 safety 判定。
func TestGenerateRequestsHiddenMasked(t *testing.T) {
	results := []assetcrawler.Result{{
		URL:    "https://x/login",
		Method: "POST",
		ParamValues: []extractor.ParamValue{
			{Name: "csrf", Kind: "hidden", Values: []string{"tok-abc"}},
		},
	}}
	reqs := GenerateRequests(results, Config{})
	if len(reqs) != 1 {
		t.Fatalf("生成 %d 条请求，期望 1", len(reqs))
	}
	if strings.Contains(reqs[0].Body, "tok-abc") {
		t.Errorf("默认不应把 hidden 原值写入 body: %q", reqs[0].Body)
	}
	if len(reqs[0].Fields) == 0 || reqs[0].Fields[0] != "csrf" {
		t.Errorf("Fields 应含参数名 csrf，得到 %v", reqs[0].Fields)
	}
	withHidden := GenerateRequests(results, Config{UseHiddenValues: true})
	if len(withHidden) != 1 || !strings.Contains(withHidden[0].Body, "tok-abc") {
		t.Errorf("--testfill-use-hidden 应使用原值，得到 %+v", withHidden)
	}
}

// TestSendRequestsSafetyAndMaxSend：safety 命中不发、MaxSend 限流、
// 两者都不计入已发送条数。
func TestSendRequestsSafetyAndMaxSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	f, err := fetcher.New(fetcher.Options{Timeout: 2 * time.Second, MaxBodySize: 1 << 20})
	if err != nil {
		t.Fatalf("fetcher.New: %v", err)
	}
	defer f.Close()

	s, err := safety.NewSafety(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := []TestRequest{
		{Method: "GET", URL: srv.URL + "/ok"},
		{Method: "GET", URL: srv.URL + "/logout"},
		{Method: "DELETE", URL: srv.URL + "/api/delete-user/1"},
		{Method: "POST", URL: srv.URL + "/ok"},
		{Method: "GET", URL: srv.URL + "/later"},
	}
	cfs := SendRequests(context.Background(), reqs, SendOptions{
		Fetcher: f, AllowUnsafe: true, MaxSend: 2, Safety: s,
	})
	if len(cfs) != len(reqs) {
		t.Fatalf("确认结果 %d 条，期望 %d", len(cfs), len(reqs))
	}
	if !cfs[0].Sent || !cfs[3].Sent {
		t.Errorf("正常请求应发送: %+v", cfs)
	}
	for _, i := range []int{1, 2} {
		if cfs[i].Sent || !strings.HasPrefix(cfs[i].SkipReason, "safety") {
			t.Errorf("第 %d 条应被 safety 拦截，得到 %+v", i, cfs[i])
		}
	}
	if cfs[4].Sent || !strings.Contains(cfs[4].SkipReason, "上限") {
		t.Errorf("第 5 条应被 MaxSend 限流，得到 %+v", cfs[4])
	}
}
