package fetcher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Request 描述一次要发送的请求。Method 空按 GET；Body 为字符串
// （测试请求的 body 都是 KB 级，不值得为它引 io.Reader）。
type Request struct {
	Method  string
	URL     *url.URL
	Referer string
	Headers map[string]string
	Body    string
}

// redirectMethod 返回重定向后应使用的方法与是否保留 body。
// 301/302/303 历史上允许（且普遍）降级为 GET；307/308 必须保留。
// 纯函数。
func redirectMethod(orig string, code int) (string, bool) {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther:
		if orig != http.MethodGet && orig != http.MethodHead {
			return http.MethodGet, false
		}
		return orig, true
	case http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return orig, true
	}
	return orig, true
}

// Send 发送任意方法与 body 的请求，复用 Get 的全部机制：
// 按 host 限并发、全局限速、5xx 重试、重定向链记录、body 截断。
func (f *Fetcher) Send(ctx context.Context, req Request) (*Response, error) {
	if req.URL == nil {
		return nil, errors.New("nil url")
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}

	release, err := f.acquireHost(ctx, req.URL.Host)
	if err != nil {
		return nil, err
	}
	defer release()

	if f.limiter != nil {
		if err := f.limiter.wait(ctx); err != nil {
			return nil, err
		}
	}

	attempts := f.opts.Retries + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		res, err := f.doWithRedirects(ctx, method, req)
		if err == nil {
			return res, nil
		}
		lastErr = err
		var se *StatusError
		if errors.As(err, &se) && se.Code < 500 {
			return res, err
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		backoff := time.Duration(100*(1<<i)) * time.Millisecond
		if backoff > time.Second {
			backoff = time.Second
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return res, ctx.Err()
		}
	}
	return nil, lastErr
}

// Get 保留的 GET 便捷入口，行为委托 Send。
func (f *Fetcher) Get(ctx context.Context, u *url.URL, referer string) (*Response, error) {
	return f.Send(ctx, Request{Method: http.MethodGet, URL: u, Referer: referer})
}

// doWithRedirects 手动跟随重定向，收集整条跳转链。
// method/body 按 redirectMethod 规则在跳转间演进。
func (f *Fetcher) doWithRedirects(ctx context.Context, method string, req Request) (*Response, error) {
	cur := req.URL
	var chain []string
	var lastStatus int
	var lastHeader http.Header
	body := req.Body

	for hop := 0; hop <= f.opts.MaxRedirects; hop++ {
		// typed-nil 陷阱：*bytes.Reader(nil) 传给 io.Reader 后接口非空，
		// NewRequestWithContext 会对其调 Len() 而 panic。空 body 传真正的 nil。
		var bodyReader io.Reader
		if body != "" {
			bodyReader = bytes.NewReader([]byte(body))
		}
		httpReq, err := http.NewRequestWithContext(ctx, method, cur.String(), bodyReader)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", f.opts.UserAgent)
		httpReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8")
		httpReq.Header.Set("Accept-Encoding", "gzip, deflate")
		if req.Referer != "" {
			httpReq.Header.Set("Referer", req.Referer)
		}
		for k, vs := range f.opts.Headers {
			for _, v := range vs {
				httpReq.Header.Add(k, v)
			}
		}
		// 调用方头优先于默认头（Content-Type 等必须由调用方说话）
		for k, v := range req.Headers {
			httpReq.Header.Set(k, v)
		}

		resp, err := f.client.Do(httpReq)
		if err != nil {
			return nil, err
		}

		lastStatus = resp.StatusCode
		lastHeader = resp.Header

		if isRedirect(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			if loc != "" {
				if next, err := cur.Parse(loc); err == nil && next.Host != "" {
					chain = append(chain, next.String())
					drainClose(resp.Body)
					// NoRedirects：重定向目标本身记录为资产，但不追进去抓。
					if f.opts.NoRedirects {
						break
					}
					nextMethod, keepBody := redirectMethod(method, resp.StatusCode)
					method = nextMethod
					if !keepBody {
						body = ""
					}
					cur = next
					continue
				}
			}
			drainClose(resp.Body)
			break
		}

		b, err := readBody(resp, f.opts.MaxBodySize)
		out := &Response{
			URL:         cur,
			StatusCode:  resp.StatusCode,
			Header:      lastHeader,
			ContentType: resp.Header.Get("Content-Type"),
			Body:        b,
			Redirects:   chain,
		}
		if err != nil {
			return out, err
		}
		if resp.StatusCode >= 500 {
			return out, &StatusError{Code: resp.StatusCode}
		}
		return out, nil
	}
	return &Response{
		URL:         cur,
		StatusCode:  lastStatus,
		Header:      lastHeader,
		ContentType: lastHeader.Get("Content-Type"),
		Redirects:   chain,
	}, nil
}
