// Package fetcher 负责 HTTP 抓取：连接复用、限速、按 host 限并发、重试、
// body 截断、重定向链记录。
//
// 它只认识 *url.URL，不认识「爬虫」这个概念——没有队列、没有去重、
// 没有解析。这样既可以被爬虫用，也可以被单次探测、被动扫描等场景直接调用。
//
// 该包不依赖本工程的其他包。
package fetcher

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Response 是一次抓取的结果。
type Response struct {
	URL         *url.URL
	StatusCode  int
	Header      http.Header
	ContentType string
	Body        []byte
	// Redirects 记录本次请求经过的每一跳 Location，跳转目标本身也是资产。
	Redirects []string
}

// Options 是抓取层配置。
type Options struct {
	Timeout      time.Duration
	MaxBodySize  int64
	MaxRedirects int
	// NoRedirects 为 true 时不跟随重定向：首个 3xx 响应直接返回，
	// Location 仍记录进 Redirects（重定向目标本身是资产）。
	// 零值 = 跟随，不翻转默认行为。
	NoRedirects        bool
	Retries            int
	Insecure           bool
	Proxy              string
	UserAgent          string
	Headers            http.Header
	PerHostConcurrency int
	// QPS 全局每秒请求上限，0 表示不限速。
	QPS int
}

func (o *Options) normalize() {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.MaxBodySize <= 0 {
		o.MaxBodySize = 2 << 20
	}
	if o.MaxRedirects <= 0 {
		o.MaxRedirects = 5
	}
	if o.PerHostConcurrency <= 0 {
		o.PerHostConcurrency = 4
	}
	if o.UserAgent == "" {
		o.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
	}
}

// StatusError 表示服务端返回了 5xx（已重试仍失败）。
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("status %d", e.Code) }

// Fetcher 是并发安全的抓取器。
type Fetcher struct {
	opts    Options
	client  *http.Client
	limiter *rateLimiter

	hostSemMu sync.Mutex
	hostSem   map[string]chan struct{}
}

// New 创建抓取器。
func New(o Options) (*Fetcher, error) {
	o.normalize()

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		DisableCompression:    true, // 自己解压，才能正确统计 body 大小
	}

	if o.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if o.Proxy != "" {
		pu, err := url.Parse(o.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy %q: %w", o.Proxy, err)
		}
		transport.Proxy = http.ProxyURL(pu)
	}

	f := &Fetcher{
		opts: o,
		client: &http.Client{
			Transport: transport,
			Timeout:   o.Timeout,
			// 关闭自动跟随：自己走跳转链才能把每一跳都记录下来
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		hostSem: make(map[string]chan struct{}),
	}
	if o.QPS > 0 {
		f.limiter = newRateLimiter(o.QPS)
	}
	return f, nil
}

// Close 释放连接池与限速协程。
func (f *Fetcher) Close() {
	if f.limiter != nil {
		f.limiter.stop()
	}
	f.client.CloseIdleConnections()
}

// Get 抓取单个 URL。返回的 Response 在发生 5xx 时会与 StatusError 一起返回，
// 便于调用方仍能拿到状态码与响应体。
func (f *Fetcher) acquireHost(ctx context.Context, host string) (func(), error) {
	f.hostSemMu.Lock()
	sem, ok := f.hostSem[host]
	if !ok {
		sem = make(chan struct{}, f.opts.PerHostConcurrency)
		f.hostSem[host] = sem
	}
	f.hostSemMu.Unlock()

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func drainClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, 32<<10))
	_ = rc.Close()
}

// readBody 读取响应体并做透明解压 + 长度截断。
// 截断而不是报错，是为了拿到大页面的前半部分——资产提取通常在前若干 KB 就已足够。
func readBody(resp *http.Response, limit int64) ([]byte, error) {
	var reader io.Reader = resp.Body
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		reader = zr
	case "deflate":
		zr, err := zlib.NewReader(resp.Body)
		if err != nil {
			// 有些服务器发的是裸 deflate
			fr := flate.NewReader(resp.Body)
			defer fr.Close()
			reader = fr
		} else {
			defer zr.Close()
			reader = zr
		}
	}
	defer resp.Body.Close()

	buf, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		if len(buf) > 0 {
			return buf, nil
		}
		return nil, err
	}
	return buf, nil
}

// rateLimiter 是一个朴素的令牌桶，按 QPS 匀速放行。
type rateLimiter struct {
	tokens chan struct{}
	stopCh chan struct{}
	once   sync.Once
}

func newRateLimiter(qps int) *rateLimiter {
	if qps <= 0 {
		return nil
	}
	l := &rateLimiter{
		tokens: make(chan struct{}, qps),
		stopCh: make(chan struct{}),
	}
	for i := 0; i < qps; i++ {
		l.tokens <- struct{}{}
	}
	interval := time.Second / time.Duration(qps)
	if interval <= 0 {
		interval = time.Millisecond
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				select {
				case l.tokens <- struct{}{}:
				default: // 桶满则丢弃，避免积压
				}
			case <-l.stopCh:
				return
			}
		}
	}()
	return l
}

func (l *rateLimiter) wait(ctx context.Context) error {
	select {
	case <-l.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *rateLimiter) stop() {
	l.once.Do(func() { close(l.stopCh) })
}
