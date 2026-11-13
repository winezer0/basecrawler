package testfill

import (
	"testing"
)

// TestIsClueResult：线索判定基于语义字段（StatusCode / Confidence / Kind），
// robots-disallow 抓取命中的真实接口不再被误当成线索，新增来源默认放行。
func TestIsClueResult(t *testing.T) {
	cases := []struct {
		name string
		r    basecrawler.Result
		want bool
	}{
		{"CSP 上报端点", basecrawler.Result{Source: "csp:report-uri", Confidence: "clue", StatusCode: 0}, true},
		{"robots Disallow 线索", basecrawler.Result{Source: "robots", Confidence: "clue", StatusCode: 0}, true},
		{"robots Allow 线索", basecrawler.Result{Source: "robots:allow", Confidence: "clue", StatusCode: 0}, true},
		{"重定向线索兜底", basecrawler.Result{Source: "redirect", StatusCode: 0}, true},
		{"路由形状", basecrawler.Result{Kind: "route", Source: "route-expand", StatusCode: 0}, true},
		{"robots-disallow 抓取命中", basecrawler.Result{Source: "robots-disallow", StatusCode: 200}, false},
		{"robots-disallow 需授权", basecrawler.Result{Source: "robots-disallow", StatusCode: 403}, false},
		{"普通已抓结果", basecrawler.Result{Source: "a", StatusCode: 200}, false},
		{"同站 JS 接口形状", basecrawler.Result{Source: "js-ast", StatusCode: 0}, false},
		{"未知新来源默认放行", basecrawler.Result{Source: "brand-new-source", StatusCode: 0}, false},
		{"sitemap 已抓页面", basecrawler.Result{Source: "sitemap", StatusCode: 200}, false},
		{"POST 表单接口形状", basecrawler.Result{Source: "form", Method: "POST", Confidence: "interface", StatusCode: 0}, false},
		{"OpenAPI 模板形状", basecrawler.Result{Source: "openapi", Confidence: "template", StatusCode: 0}, false},
		{"curl 代码块", basecrawler.Result{Kind: "curl", Source: "code:curl", StatusCode: 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isClueResult(c.r); got != c.want {
				t.Errorf("isClueResult(%+v) = %v，期望 %v", c.r, got, c.want)
			}
		})
	}
}
