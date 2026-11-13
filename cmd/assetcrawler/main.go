// Command assetcrawl 是 assetcrawl 包的参考命令行工具。
//
// 用法示例：
//
//	assetcrawl -u https://example.com -d 3 -o urls.txt
//	assetcrawl -u https://example.com -O json -o result.jsonl
//	assetcrawl -u https://example.com --scope same-host -H "Cookie: a=b"
//
// 参数解析与 Options 的映射在 flags.go，本文件只负责跑起来并把结果打出去。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/winezer0/basecrawler/fetcher"
	"github.com/winezer0/basecrawler/testfill"
)

func main() {
	cfg, err := parseArgs(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "%v\n", err)
		fmt.Fprintln(os.Stderr, "usage: assetcrawl -u <url> [-d depth] [-O json|txt] [-o out]")
		os.Exit(2)
	}
	// 版本查询早于一切：不加载配置、不建抓取器、不发请求
	if maybePrintVersion(cfg) {
		return
	}

	opts := cfg.Opts
	// 混淆探测链、string-array 沙箱、路径探测、spec 探测、路由展开都在
	// parseArgs 里按默认开启 / --no-* 关闭映射完毕，这里不再二次赋值。

	outWriter := os.Stdout
	var file *os.File
	if cfg.OutputPath != "" {
		if dir := filepath.Dir(cfg.OutputPath); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "cannot create output dir: %v\n", err)
				os.Exit(1)
			}
		}
		f, err := os.Create(cfg.OutputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot create output file: %v\n", err)
			os.Exit(1)
		}
		file = f
		outWriter = f
		defer file.Close()
	}

	var (
		wmu             sync.Mutex
		count           int
		filteredInvalid int
		uniq            = make(map[string]struct{}, 1024)
	)
	// 作用域双层生效：导航入队由 frontier 把关，输出显示在这里按同一
	// 口径过滤（范围外的结果，包括已抓取的跨域资产，一律不打印）。
	// crawler 在 New 之后才存在，所以过滤闭包延迟到过滤器就绪后替换。
	var inScope func(string) bool
	scopeFilter := func(raw string) bool { return inScope == nil || inScope(raw) }
	// V-9 相似 URL 过滤：在 scope/状态码之后、去重之前套一层路径形状
	// 归并，/users/123 ≈ /users/456 只留首条。
	var fsu *similarFilter
	if cfg.SimilarFilter {
		p, err := newSimilarFilter()
		if err != nil {
			fmt.Fprintf(os.Stderr, "fsu init failed: %v\n", err)
			os.Exit(1)
		}
		fsu = p
		defer fsu.Close()
	}
	// writeResult 是结果文件的唯一写入口：Crawl 的 OnResult 与 testfill 的
	// 确认结果都走这里，保证 scope / -s / -S / 去重 / fsu 口径完全一致。
	writeResult := func(r assetcrawler.Result) {
		wmu.Lock()
		defer wmu.Unlock()
		if !scopeFilter(r.URL) {
			return
		}
		// 输出过滤只有这一处：-s 白名单 + -S 黑名单（ffuf 模型）。
		// 0 = 未访问的线索，只是普通状态码，默认码表不含它所以不输出。
		if !cfg.StatusFilter.Allow(r.StatusCode) {
			filteredInvalid++
			return
		}
		// 纯 URL 输出要去重；JSONL 保留每条结果的来源差异
		jsonOut := cfg.OutputType == "json"
		if _, dup := uniq[r.URL]; dup && !jsonOut {
			return
		}
		if fsu != nil && !fsu.Allow(r.URL) {
			return
		}
		uniq[r.URL] = struct{}{}
		count++
		if jsonOut {
			b, err := json.Marshal(r)
			if err == nil {
				fmt.Fprintln(outWriter, string(b))
			}
			return
		}
		fmt.Fprintln(outWriter, r.URL)
	}
	opts.OnResult = writeResult
	if cfg.Verbose {
		opts.OnError = func(u string, err error) {
			fmt.Fprintf(os.Stderr, "[err] %s: %v\n", u, err)
		}
	}

	crawler, err := assetcrawler.New(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
		os.Exit(1)
	}
	defer crawler.Close()
	inScope = crawler.InScope

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// --ct 总时长上限：在信号 context 之上再包一层超时。
	// 到点后与 Ctrl+C 走同一条收敛路径（isCtxErr），已采集结果照常输出。
	if cfg.TotalTimeout > 0 {
		var cancelTimeout context.CancelFunc
		ctx, cancelTimeout = context.WithTimeout(ctx, cfg.TotalTimeout)
		defer cancelTimeout()
	}

	results, err := crawler.Crawl(ctx, cfg.URLs...)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "crawl error: %v\n", err)
	}

	// 测试请求填充与自动发送（--no-testfill 关闭，默认开启）。
	// 不再区分幂等/非幂等方法；发送前由 safety 公共工具包按关键字拦截
	// 危险动作，并受 --max-testfill 条数上限约束。
	// 发送不再依赖 --testdata 产物格式，两者可独立使用。
	reqs := testfill.GenerateRequests(results, testfill.Config{
		AuthHeader:      cfg.TestDataAuth,
		SiteHosts:       seedHosts(cfg.URLs),
		UseHiddenValues: cfg.UseHiddenValues,
	})
	var cfs []testfill.Confirmation
	if cfg.TestFill {
		f, err := fetcher.New(fetcher.Options{
			Timeout:            cfg.Opts.Timeout,
			MaxBodySize:        cfg.Opts.MaxBodySize,
			MaxRedirects:       cfg.Opts.MaxRedirects,
			Retries:            cfg.Opts.Retries,
			Insecure:           cfg.Opts.Insecure,
			Proxy:              cfg.Opts.Proxy,
			UserAgent:          cfg.Opts.UserAgent,
			Headers:            cfg.Opts.Headers,
			PerHostConcurrency: cfg.Opts.PerHostConcurrency,
			QPS:                cfg.Opts.RateLimit,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "testfill error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		cfs = testfill.SendRequests(ctx, reqs, testfill.SendOptions{
			Fetcher:     f,
			AllowUnsafe: cfg.Opts.AllowUnsafeSend,
			MaxSend:     cfg.MaxTestFill,
			Safety:      cfg.Safety,
		})
		if !cfg.Silent {
			sent, blocked := 0, 0
			for _, c := range cfs {
				if c.Sent {
					sent++
				}
				if strings.HasPrefix(c.SkipReason, "safety") {
					blocked++
				}
			}
			fmt.Fprintf(os.Stderr, "[testfill] 已发送 %d / %d 条（所有方法，含 POST/PUT/PATCH/DELETE）\n",
				sent, len(cfs))
			fmt.Fprintf(os.Stderr, "[safety] 拦截 %d 条危险请求（--no-safety 可关闭）\n", blocked)
			fmt.Fprintf(os.Stderr, "[confirm] %s\n", confirmSummary(cfs))
		}
		// 发送确认结果写回主结果文件：与 Crawl 结果共用 writeResult，
		// 因此同样受 scope / -s / -S / 去重 / fsu 约束；txt 只留 URL，
		// JSONL 带 source=testfill、method、status 供区分。
		for _, r := range confirmResults(cfs) {
			writeResult(r)
		}
	}

	// 产物渲染：--testdata 指定格式时输出；发送确认状态已合入产物。
	if cfg.TestData != "" {
		var out string
		var err error
		if cfg.TestFill {
			out, err = testfill.RenderConfirmed(cfg.TestData, cfs)
		} else {
			out, err = testfill.Render(reqs, cfg.TestData)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "testdata error: %v\n", err)
			os.Exit(1)
		}
		if cfg.TestDataOut != "" {
			if err := os.WriteFile(cfg.TestDataOut, []byte(out), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "testdata-out error: %v\n", err)
				os.Exit(1)
			}
			if !cfg.Silent {
				fmt.Fprintf(os.Stderr, "[testdata] %d 条请求 -> %s\n", len(reqs), cfg.TestDataOut)
			}
		} else {
			fmt.Print(out)
		}
	}

	// 所有写入（Crawl 结果 + testfill 确认结果）完成后再汇总，count
	// 才是结果文件的实际条数；文件也在这里做最后一次 Sync。
	if file != nil {
		file.Sync()
	}
	if !cfg.Silent {
		fmt.Fprintf(os.Stderr, "\n[done] %d URLs collected", count)
		if filteredInvalid > 0 {
			fmt.Fprintf(os.Stderr, "（另有 %d 条被状态码过滤，-s all 可看全量）", filteredInvalid)
		}
		fmt.Fprintln(os.Stderr)
		// --ct 到点截断要显式区分：批量脚本据此知道这轮不是自然收敛，
		// 结果可能不完整，需要加大 --ct 或拆任务重跑。
		if cfg.TotalTimeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "[limit] time budget reached, results may be incomplete (raise --ct or split the task)\n")
		}
		for _, e := range crawler.Limits() {
			fmt.Fprintf(os.Stderr, "[limit] %-12s 候选 %d / 产出 %d / 截断 %d\n",
				e.Kind, e.Candidates, e.Emitted, e.Truncated)
		}
		if fsu != nil {
			fsu.Summary()
		}
	}
}

// seedHosts 从种子 URL 提取 host 列表（testfill 的 SiteHosts 过滤用）。
func seedHosts(urls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		if !seen[u.Hostname()] {
			seen[u.Hostname()] = true
			out = append(out, u.Hostname())
		}
	}
	return out
}

// confirmSummary 汇总发送确认状态：200 N / 3xx N / 4xx N / 5xx N / 失败 N。
func confirmSummary(cfs []testfill.Confirmation) string {
	var ok2, ok3, c4, c5, failed, other int
	for _, c := range cfs {
		if !c.Sent {
			continue
		}
		switch {
		case c.Error != "":
			failed++
		case c.StatusCode >= 500:
			c5++
		case c.StatusCode >= 400:
			c4++
		case c.StatusCode >= 300:
			ok3++
		case c.StatusCode >= 200:
			ok2++
		default:
			other++
		}
	}
	return fmt.Sprintf("200 %d / 3xx %d / 4xx %d / 5xx %d / 失败 %d / 其他 %d",
		ok2, ok3, c4, c5, failed, other)
}
