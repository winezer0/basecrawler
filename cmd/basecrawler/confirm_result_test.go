package main

import (
	"testing"

	"github.com/winezer0/basecrawler/testfill"
)

// TestConfirmResults：只有「真发出 + 拿到响应状态」的确认才转成 Result；
// 状态码是否写入结果文件由 writeResult 的 -s/-S 决定，这里不过滤 404。
func TestConfirmResults(t *testing.T) {
	cfs := []testfill.Confirmation{
		{
			TestRequest: testfill.TestRequest{
				Method: "POST", URL: "https://x/login",
				Query: map[string]string{"user": "test"},
			},
			Sent: true, StatusCode: 200,
		},
		{
			TestRequest: testfill.TestRequest{Method: "GET", URL: "https://x/missing"},
			Sent:        true, StatusCode: 404, // 交给 -s 白名单丢弃
		},
		{
			TestRequest: testfill.TestRequest{Method: "DELETE", URL: "https://x/item"},
			Sent:        true, StatusCode: 0, Error: "timeout", // 无响应，丢弃
		},
		{
			TestRequest: testfill.TestRequest{Method: "GET", URL: "https://x/skip"},
			Sent:        false, SkipReason: "无 fetcher，未发送",
		},
	}
	got := confirmResults(cfs)
	if len(got) != 2 {
		t.Fatalf("确认结果 %d 条，期望 2（200 与 404），得到 %+v", len(got), got)
	}
	first := got[0]
	if first.URL != "https://x/login?user=test" {
		t.Errorf("URL = %q，期望含填充参数", first.URL)
	}
	if first.Method != "POST" || first.Source != "testfill" || first.StatusCode != 200 {
		t.Errorf("确认字段错误：method=%q source=%q status=%d", first.Method, first.Source, first.StatusCode)
	}
	if got[1].StatusCode != 404 || got[1].Source != "testfill" {
		t.Errorf("404 确认应保留给 -s 过滤，得到 %+v", got[1])
	}
}

// TestConfirmResultsNoTestFill：--no-testfill 时 cfs 为空，不产出任何记录。
func TestConfirmResultsNoTestFill(t *testing.T) {
	if got := confirmResults(nil); len(got) != 0 {
		t.Errorf("空确认列表应产出 0 条，得到 %+v", got)
	}
}
