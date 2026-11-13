package testfill

import (
	"context"
	"fmt"
	"net/url"

	"github.com/winezer0/basecrawler/fetcher"
	"github.com/winezer0/basecrawler/safety"
)

// 幂等方法表统一由 safety.IsSafeMethod 提供（公共工具包），此处不再
// 重复维护。非幂等方法（POST/PUT/PATCH/DELETE）是否发送由
// SendOptions.AllowUnsafe 决定；CLI 默认放行全部方法，但会先过 safety
// 关键字拦截。

// SendOptions 控制「确认接口」的发送行为。
type SendOptions struct {
	// Fetcher 复用调用方的抓取器：限速、代理、超时、重定向链全部现成。
	// 为空时全部跳过（不发送）。
	Fetcher *fetcher.Fetcher
	// AllowUnsafe 允许非幂等方法（POST/PUT/PATCH/DELETE）。
	AllowUnsafe bool
	// MaxSend 发送条数上限，0 不限（超出部分记跳过原因）。
	MaxSend int
	// Safety 危险操作拦截（nil 表示不拦截）。命中时记 SkipReason、不发请求。
	Safety *safety.Safety
}

// Confirmation 是一条测试请求的发送结论。
type Confirmation struct {
	TestRequest
	StatusCode int    `json:"status_code,omitempty"`
	Sent       bool   `json:"sent"`
	SkipReason string `json:"skip_reason,omitempty"`
	Error      string `json:"error,omitempty"`
}

// SendRequests 逐条发送测试请求，返回发送结论（顺序与入参一致）。
// 判定顺序：无 fetcher → 非幂等未授权 → 发送上限 → safety 拦截 → 真实发送。
func SendRequests(ctx context.Context, reqs []TestRequest, opts SendOptions) []Confirmation {
	out := make([]Confirmation, 0, len(reqs))
	sent := 0
	for _, r := range reqs {
		c := Confirmation{TestRequest: r}
		switch {
		case opts.Fetcher == nil:
			c.SkipReason = "无 fetcher，未发送"
		case !safety.IsSafeMethod(r.Method) && !opts.AllowUnsafe:
			c.SkipReason = fmt.Sprintf("%s 非幂等方法，需 AllowUnsafe 才发送", r.Method)
		case opts.MaxSend > 0 && sent >= opts.MaxSend:
			c.SkipReason = fmt.Sprintf("达到发送上限 %d", opts.MaxSend)
		default:
			// 关键字级危险动作拦截：发现结果不受影响，只是不真发请求。
			if opts.Safety != nil && opts.Safety.Enabled() {
				if skip, reason := opts.Safety.SkipRequest(r.Method, r.URL, r.Fields); skip {
					c.SkipReason = "safety 拦截: " + reason
					out = append(out, c)
					continue
				}
			}
			u, err := url.Parse(r.SentURL())
			if err != nil {
				c.Error = err.Error()
				out = append(out, c)
				continue
			}
			resp, err := opts.Fetcher.Send(ctx, fetcher.Request{
				Method:  r.Method,
				URL:     u,
				Headers: r.Headers,
				Body:    r.Body,
			})
			sent++
			c.Sent = true
			if resp != nil {
				c.StatusCode = resp.StatusCode
			}
			if err != nil {
				c.Error = err.Error()
			}
		}
		out = append(out, c)
	}
	return out
}

// RenderConfirmed 渲染带确认注释的产物。注释形态：
//   - curl/python/http：行内注释（# → 200 / # 跳过: 原因）
//   - postman：item 名后缀（见 RenderPostmanConfirmed）
//   - json：Confirmation 自带 status/sent/skip_reason 字段
func RenderConfirmed(format string, cfs []Confirmation) (string, error) {
	ann := annotations(cfs)
	switch format {
	case "curl":
		return renderCurlLines(cfsToReqs(cfs), ann), nil
	case "python":
		return renderPythonLines(cfsToReqs(cfs), ann), nil
	case "http":
		return renderHTTPLines(cfsToReqs(cfs), ann), nil
	case "postman":
		return RenderPostman(cfsToReqs(cfs)), nil
	case "json":
		return encodeJSONL(cfs)
	}
	return "", fmt.Errorf("testfill: 未知格式 %q（curl/python/postman/http/json）", format)
}

// annotations 把确认结论摊成「Method URL → 注释文本」的映射。
func annotations(cfs []Confirmation) map[string]string {
	out := make(map[string]string, len(cfs))
	for _, c := range cfs {
		switch {
		case c.Sent && c.StatusCode > 0:
			out[annotateKey(c.TestRequest)] = fmt.Sprintf("→ %d", c.StatusCode)
		case c.Sent && c.Error != "":
			out[annotateKey(c.TestRequest)] = "发送失败: " + c.Error
		case c.SkipReason != "":
			out[annotateKey(c.TestRequest)] = "跳过: " + c.SkipReason
		}
	}
	return out
}

func annotateKey(r TestRequest) string {
	return r.Method + " " + r.URL
}

// cfsToReqs 从确认列表取回请求列表（渲染复用）。
func cfsToReqs(cfs []Confirmation) []TestRequest {
	out := make([]TestRequest, len(cfs))
	for i, c := range cfs {
		out[i] = c.TestRequest
	}
	return out
}
