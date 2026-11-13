package basecrawler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/winezer0/basecrawler/extractor"
	"github.com/winezer0/basecrawler/fetcher"
	"github.com/winezer0/basecrawler/frontier"
	"github.com/winezer0/basecrawler/routegraph"
)

// Crawler 是采集引擎。零值不可用，请用 New 构造。
//
// 它自身不实现抓取、解析、去重中的任何一项，只负责把这三者串起来，
// 因此状态很少：配置、两个协作者、一个结果缓冲区。
type Crawler struct {
	opts     *Options
	fetcher  *fetcher.Fetcher
	frontier *frontier.Frontier
	// limits 是全流程唯一的截断统计聚合器，由 New 创建、由 extractorOptions 注入。
	// 解析器本身不持有状态，仍是纯函数。
	limits *extractor.LimitReport
	// used 落实「一实例一采集」契约：结果集与已见集合在 Crawl 间不重置，
	// 复用实例会累积脏数据，因此第二次调用直接报错而不是静默出错。
	used bool

	// pagesPerHost 记录每 host 已抓取的 HTML 页面数（MaxPagesPerHost 用），
	// 访问须持 mu。同时兼作 fetched/routeExpanded 的计数锁。
	pagesPerHost map[string]int
	// dispatched 是派发通道的全局去重集合，跨层共享、访问须持 mu。
	// 与 frontier.seen（入队登记）独立：这里管「实际并发派发」。
	dispatched map[string]struct{}

	mu            sync.Mutex
	emitMu        sync.Mutex
	fetched       int
	routeExpanded int
	results       []Result
}

// task 是一个待抓取项。来源字段会一路带到结果里，
// 便于事后区分「这条 URL 是从 a 标签来的，还是从 JS 里抠出来的」。
type task struct {
	url         *url.URL
	depth       int
	referer     string
	source      string
	attribute   string
	tag         string
	method      string
	kind        string
	params      []string
	paramValues []extractor.ParamValue
	// deferPage 表示「同层里可以晚一点抓」的页面类任务。
	// 静态资源优先，避免大批导航路径把 JS/CSS 的配额抢光（见 orderTasks）。
	deferPage bool
	// boost 表示「同层里必须最先抓」——目前只给计算型分页链的下一页：
	// JSON 数据 URL 的级联提取会把配额吃到尽头，链尾页被饿死
	// （dummyjson 实测漏链尾 1 条 = 198/200 配额耗尽）。
	boost bool
}

// New 创建采集器。
func New(opts *Options) (*Crawler, error) {
	if opts == nil {
		opts = DefaultOptions()
	}
	opts.normalize()

	f, err := fetcher.New(opts.fetcherOptions())
	if err != nil {
		return nil, err
	}
	return &Crawler{
		opts:         opts,
		fetcher:      f,
		frontier:     frontier.New(opts.frontierOptions()),
		limits:       &extractor.LimitReport{},
		pagesPerHost: make(map[string]int),
		dispatched:   make(map[string]struct{}, 1024),
	}, nil
}

// InScope 判定 URL 是否在当前作用域内，与导航入队同一口径。
// 输出层（OnResult 消费方）用它实现作用域对显示的第二层生效。
func (c *Crawler) InScope(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return c.frontier.InScope(u)
}

// Limits 返回本次采集中发生过的截断统计，按 kind 排序。
// 没有截断时返回空切片，调用方可以直接遍历。
func (c *Crawler) Limits() []extractor.LimitEvent {
	return c.limits.Events()
}

// Close 释放连接池与后台协程。
func (c *Crawler) Close() error {
	if c.fetcher != nil {
		c.fetcher.Close()
		// 置 nil 让 Crawl 的 closed 检查真正生效：
		// 之前只关连接不置空，Close 后 Crawl 仍会跑在已关闭的 fetcher 上。
		c.fetcher = nil
	}
	return nil
}

// Crawl 从给定种子开始采集，返回全部命中结果。
// 若设置了 Options.OnResult，会在抓取过程中流式回调，返回值用于收尾统计。
//
// 契约：一实例一采集。Crawl 结束后结果集、已见集合与截断统计均不重置，
// 第二次调用会直接报错——需要再采一次就再 New 一个实例。
// Crawler 亦不支持多 goroutine 并发调用 Crawl。
func (c *Crawler) Crawl(ctx context.Context, seeds ...string) ([]Result, error) {
	if len(seeds) == 0 {
		return nil, fmt.Errorf("no seed URL provided")
	}
	if c.fetcher == nil {
		return nil, fmt.Errorf("crawler closed")
	}
	if c.used {
		return nil, fmt.Errorf("crawler already used: one instance per crawl, create a new Crawler")
	}
	c.used = true

	current, err := c.seedTasks(seeds)
	if err != nil {
		return nil, err
	}

	// robots.txt / sitemap.xml / 已知路径：sitemap 页面是 depth 1，
	// 已知路径是 depth 0。这里不直接拼进 current，等第 0 层跑完再并入，
	// 否则它们会和种子页同层，把配额吃光。
	boot := c.bootstrap(ctx, current)

	for depth := 0; len(current) > 0; depth++ {
		if ctx.Err() != nil {
			break
		}
		if c.opts.MaxDepth > 0 && depth > c.opts.MaxDepth {
			break
		}
		// 同层内按规范化 URL 去重后再派发。R-0 实测：同一 URL 出现在同层
		// 多个父页里时（vite.dev 的 /、/blog 各有 2 条），会被抓两遍、
		// 结果里出两行、还多占一份 MaxURLs 配额 —— 配额是硬上限，
		// 多花一份就是实实在在的召回损失。frontier 的去重只在入队时生效，
		// 同批并发派发绕过了它。
		//
		// 去重必须在「本层调度结果」与「第 0 层的引导任务」合并之后做：
		// 引导任务（sitemap 页面 depth 1、已知路径 depth 0）与后续层发现
		// 的同一页面只差一层，不合并就会各抓一次（vite.dev 的 /blog 实测如此）。
		current = dedupeTasks(current)
		level := c.runLevel(ctx, current)
		var next []task
		if c.opts.StableHostQuota {
			next = orderTasksStable(level)
		} else {
			next = orderTasks(level)
		}

		// 第 0 层结束后并入引导任务。顺序上把种子页提取出的资源排在前面：
		// 配额紧张时优先抓 JS/CSS，而不是把额度全给 sitemap 里的页面。
		// 合并与去重放在下面一起做（boot 排在本层结果之前，理由见那里）。
		// 引导任务与本层结果合并后必须再去重一次（并同时登记进 frontier 的
		// 已见集合）。否则第 1 层抓到的页面会在第 2 层被「重新发现」再抓一次：
		// vite.dev 的 /blog 来自 sitemap（depth 1），metadata.js 里也有同一个
		// 地址（depth 2），两份都进结果。
		//
		// 顺序：引导任务 append 在本层结果之后。这是 I-1/§P0-2 定下的配额次序
		// （种子页提取出的 JS/CSS 排在 sitemap 页面之前），TestSitemapDoesNotStarveAssets
		// 锁定了它；放在前面会让 sitemap 抢光小配额下的资源名额。
		if depth == 0 && len(boot) > 0 {
			next = append(next, boot...)
			boot = nil
		}
		current = c.dedupeAndMark(next)
	}

	// 上限截断必须让调用方知道：静默少收一半结果比直接报错更难排查。
	if n := c.frontier.Dropped(); n > 0 {
		c.emitError("frontier", fmt.Errorf("已见集合达到上限 %d，%d 条 URL 未入队，结果不完整（可用 -max-seen 调整）", c.opts.MaxSeen, n))
	}

	c.mu.Lock()
	out := make([]Result, len(c.results))
	copy(out, c.results)
	c.mu.Unlock()
	return out, ctx.Err()
}

// seedTasks 规范化种子、登记范围基准，返回第一层任务。
func (c *Crawler) seedTasks(seeds []string) ([]task, error) {
	var (
		out []task
		bad []string
	)
	for _, s := range seeds {
		u, ok := frontier.Normalize(s, nil)
		if !ok {
			bad = append(bad, s)
			continue
		}
		c.frontier.AddSeed(u)
		if nu, added := c.frontier.PushSeed(u); added {
			out = append(out, task{url: nu, depth: 0, source: "seed", method: "GET"})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid http(s) seed URL in %v", bad)
	}
	return out, nil
}

// runLevel 并发抓取一层，返回下一层任务。
//
// 并发收敛方式：worker 从 jobs 取任务，结果通过互斥锁汇总到 next。
// 达到 MaxURLs 时 cancel 掉本层 context，派发循环随之退出，
// 不依赖 goroutine 被强行终止——所有 worker 都靠 channel 关闭或 ctx 结束自然返回。
func (c *Crawler) runLevel(ctx context.Context, tasks []task) []task {
	workers := c.opts.Concurrency
	if workers > len(tasks) {
		workers = len(tasks)
	}
	if workers <= 0 {
		return nil
	}

	levelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		jobs = make(chan task)
		mu   sync.Mutex
		next []task
		wg   sync.WaitGroup
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if levelCtx.Err() != nil {
					return
				}
				if !c.reserveFetch() {
					cancel() // 达到 MaxURLs，停止本层继续派发
					return
				}
				found := c.processOne(levelCtx, t)
				if len(found) == 0 {
					continue
				}
				mu.Lock()
				next = append(next, found...)
				mu.Unlock()
			}
		}()
	}

	for _, t := range tasks {
		// 派发通道兜底去重：跨层/跨批次的同址任务只派发一次。
		// frontier.seen 管入队登记，这里管实际派发，堵住层内去重与
		// 引导任务合并之间的边界空洞，也避免重复任务白占 MaxURLs 配额。
		if !c.reserveDispatch(t.url) {
			continue
		}
		select {
		case jobs <- t:
		case <-levelCtx.Done():
			close(jobs)
			wg.Wait()
			return next
		}
	}
	close(jobs)
	wg.Wait()
	return next
}

// reserveDispatch 在派发通道登记 URL，返回是否首次派发。
// 与 frontier 的入队去重独立：seen 是候选登记，dispatched 是实际派发；
// 覆盖跨层合并、引导任务追加等边界产生的重复（实测每站 1~6 条）。
func (c *Crawler) reserveDispatch(u *url.URL) bool {
	if u == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := u.String()
	if _, dup := c.dispatched[key]; dup {
		return false
	}
	c.dispatched[key] = struct{}{}
	return true
}

// reservePageSlot 原子预留一个页面配额；超限时返回 false。
func (c *Crawler) reservePageSlot(host string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pagesPerHost[host] >= c.opts.MaxPagesPerHost {
		return false
	}
	c.pagesPerHost[host]++
	return true
}

// processOne 抓取并解析单个 URL，输出一条结果并返回新发现的下层任务。
func (c *Crawler) processOne(ctx context.Context, t task) []task {
	// -mdp：单 host 页面数封顶。抓取前原子预留配额（check-then-act
	// 在并发下会超卖）；只约束导航类 GET 页面，静态资产与线索类不受限。
	// 预留即计数，含最终非 2xx 的页面——它们同样消耗了抓取配额。
	if c.opts.MaxPagesPerHost > 0 && (t.method == "" || t.method == "GET") &&
		!c.reservePageSlot(t.url.Host) {
		return nil
	}
	res, err := c.fetcher.Get(ctx, t.url, t.referer)
	if err != nil && !isCancellation(err) {
		c.emitError(t.url.String(), err)
	}
	if res == nil {
		return nil
	}

	// 探测类任务（fuzz 字典 / openapi-probe / robots Disallow）404 即 miss，
	// 不产出结果也不做正文提取——miss 页面的 404 文案没有资产价值，
	// 还会污染输出。
	if isProbeSource(t.source) && res.StatusCode == 404 {
		return nil
	}

	c.emit(c.resultOf(t, res))

	nextDepth := t.depth + 1
	if c.opts.MaxDepth > 0 && nextDepth > c.opts.MaxDepth {
		return nil
	}
	reqs, doc, base := c.extractAll(res)
	// CSP 线索只报告不入队：它是「目标信任哪些域名」的清单，
	// 抓取价值低（多为域名根，实测 5 个样本只 1 个 200）而信息价值高。
	if c.opts.CSPHints {
		c.emitCSPHints(res.Header, base, t.depth, res.URL.String())
	}
	// curl 代码块只报告不入队：文档里的 endpoint 常在外域
	// （api.openai.com 之于文档站），且抓了等于真发请求。
	// StatusCode 0 表示未请求，与 robots 线索同口径。
	if c.opts.ExtractCurlBlocks && doc != nil {
		for _, cmd := range extractor.CurlCommands(doc) {
			c.emit(Result{
				URL:     cmd.URL,
				Method:  cmd.Method,
				Source:  "code:curl",
				Kind:    "curl",
				Raw:     cmd.Raw,
				Depth:   nextDepth,
				Referer: res.URL.String(),
			})
		}
	}
	return c.toTasks(reqs, base, nextDepth, res.URL.String())
}

// extractAll 从一次响应里穷尽式提取 URL 候选，并返回相对路径的解析基准。
// 提取本身是纯函数，这里只负责「选择用哪个提取函数」以及 base 基准的确定。
// 返回的 doc 仅在 HTML 分支非 nil（供代码块级的非 URL 提取复用，
// 避免同一份 body 解析两遍）。
func (c *Crawler) extractAll(res *fetcher.Response) ([]extractor.Request, *html.Node, *url.URL) {
	var out []extractor.Request
	base := res.URL

	// 跳转链上的每一跳都是资产
	for _, loc := range res.Redirects {
		out = append(out, extractor.Request{Raw: loc, Method: "GET", Source: "redirect"})
	}

	out = append(out, extractor.FromHeaders(res.Header)...)

	// source map 与下面的内容分派并行：`.map` 常以 application/json 返回，
	// 会被 JSON 分支按普通响应处理，而它的 sources 需要专门归一化。
	if extractor.LooksLikeSourceMap(res.URL.Path, res.Body) {
		out = append(out, extractor.FromSourceMapWithLimits(res.Body, c.limits)...)
	}

	// Q-4：OpenAPI/Swagger spec（opt-in）。放在内容分派之前：spec 是
	// JSON，走下面分支只会按普通 JSON 提取链接，把整套接口契约漏掉。
	// 判定用内容而不只是路径——spec 常挂在自定义路径上（/api/schema 等）。
	if c.opts.ProbeOpenAPI && extractor.LooksLikeOpenAPI(res.Body) {
		out = append(out, extractor.FromOpenAPI(res.Body)...)
	}

	switch {
	case extractor.LooksLikeHTML(res.ContentType, res.Body):
		doc, err := html.Parse(bytes.NewReader(res.Body))
		if err != nil {
			c.emitError(res.URL.String(), err)
			return out, nil, base
		}
		out = append(out, extractor.FromHTML(doc, c.opts.extractorOptions(c.limits))...)
		// <base href> 会改变相对路径的解析基准；是否采纳由编排层决定，
		// extractor 只负责把值读出来。
		if bh := extractor.BaseHref(doc); bh != "" {
			if bu, ok := frontier.Normalize(bh, res.URL); ok {
				base = bu
			}
		}
		return out, doc, base
	case c.opts.ExtractJSON && extractor.LooksLikeJSON(res.ContentType, res.URL.Path):
		// 「下一跳」只出现在响应体里，HTML 与 JS 都看不到。
		// 显式字段（next/links）由 FromJSON 提取；计算型分页
		// （page+1 / has_more / continue / skip+limit）响应体里没有
		// 下一页地址，必须由当前 URL 构造——I-2 闸门实测每站漏 4 条。
		out = append(out, extractor.FromJSON(res.Body)...)
		out = append(out, extractor.FromJSONPagination(res.Body, res.URL)...)
	case extractor.LooksLikeCSS(res.ContentType, res.URL.Path):
		if c.opts.ExtractFromStyle {
			out = append(out, extractor.FromCSS(string(res.Body))...)
		}
		// CSS 尾部同样可能声明 sourceMappingURL（R-0：swagger-ui.css.map）。
		out = append(out, extractor.SourceMapRequests(res.Body)...)
	case c.opts.ExtractJS && extractor.LooksLikeJS(res.ContentType, res.URL.Path):
		// 先跑带类型标记的 chunk/route 恢复，再跑通用 JS 提取。
		// 顺序有讲究：两者可能产出同一 URL，frontier 的「先到先得」去重
		// 会让后到的类型标记丢掉，所以带标记的必须排在前面。
		out = append(out, c.extractRouteGraph(res.Body, res.URL.String())...)
		out = append(out, extractor.FromJS(string(res.Body), c.opts.extractorOptions(c.limits))...)
		// 尾部 //# sourceMappingURL=：声明的是「这个文件的源码映射」，
		// 现有逻辑只在响应本身是 .map 时才解析 sources，从不去取这个文件。
		out = append(out, extractor.SourceMapRequests(res.Body)...)
	}

	return out, nil, base
}

// mergeExecResult 把执行兜底的路由/chunk 并入静态结果：
//   - 两条路径可能发现同一地址，按 Path 去重，静态优先（来源更可靠）；
//   - 执行只给出 webpack 数字 chunk ID 时，用静态 u 函数表的 ID→文件名
//     映射补上 Path，否则 crawler 无法入队。
func mergeExecResult(dst *routegraph.Result, src routegraph.Result) {
	byID := make(map[string]string, len(dst.Chunks))
	seenChunk := make(map[string]struct{}, len(dst.Chunks))
	for _, ch := range dst.Chunks {
		if ch.ID != "" && ch.Path != "" {
			byID[ch.ID] = ch.Path
		}
		if ch.Path != "" {
			seenChunk[ch.Path] = struct{}{}
		}
	}
	for _, ch := range src.Chunks {
		if ch.Path == "" && ch.ID != "" {
			ch.Path = byID[ch.ID]
		}
		if ch.Path == "" {
			continue
		}
		if _, dup := seenChunk[ch.Path]; dup {
			continue
		}
		seenChunk[ch.Path] = struct{}{}
		dst.Chunks = append(dst.Chunks, ch)
	}
	seenRoute := make(map[string]struct{}, len(dst.Routes))
	for _, rt := range dst.Routes {
		seenRoute[rt.Path] = struct{}{}
	}
	for _, rt := range src.Routes {
		if _, dup := seenRoute[rt.Path]; dup {
			continue
		}
		seenRoute[rt.Path] = struct{}{}
		dst.Routes = append(dst.Routes, rt)
	}
}

// errBundleTruncated 表示打包产物超出词法扫描上限，结果可能不完整。
var errBundleTruncated = errors.New("打包产物超过词法扫描上限，chunk/route 结果可能不完整")

// extractRouteGraph 从打包产物里恢复「静态链接看不见」的两类目标：
// 懒加载 chunk 文件与前端路由表。
//
// chunk 直接入队（它们是真实文件）；route 也入队，但带 Kind=route 标记——
// 前端路由在服务端往往只是同一个 index.html，调用方可据此过滤。
func (c *Crawler) extractRouteGraph(body []byte, source string) []extractor.Request {
	if !c.opts.ExtractChunkGraph || !routegraph.LooksLikeBundle(body) {
		return nil
	}
	res := routegraph.Extract(body)
	if res.Truncated {
		// 明确报出来：静默截断会让人误以为「这个包里没有 chunk 清单」
		c.emitError(source, errBundleTruncated)
	}

	// 执行兜底与静态结果合并，而不是「静态为空才补跑」：
	// 运行时 map 生成的路由常与显式路由共存，只补空结果会漏掉一半。
	// 门槛是双签名粗筛（bundle + 运行时/路由构造特征），普通 JS 不进 VM。
	if c.opts.RouteExec && routegraph.LooksExecutable(body) {
		execRes, err := routegraph.ExtractByExec(body, routegraph.ExecOptions{})
		if err != nil {
			// 尽力而为的路径：源码形态不匹配时静默跳过，其它错误照常上报。
			if !errors.Is(err, routegraph.ErrSkipExec) {
				c.emitError(source, err)
			}
		} else {
			mergeExecResult(&res, execRes)
		}
	}

	out := make([]extractor.Request, 0, len(res.Chunks)+len(res.Routes))
	for _, ch := range res.Chunks {
		if ch.Path == "" {
			continue // 执行兜底只给 id 不给文件名时无法入队
		}
		out = append(out, extractor.Request{
			Raw:    ch.Path,
			Method: "GET",
			Source: "chunk:" + ch.Source,
			Kind:   "chunk",
		})
	}
	for _, rt := range res.Routes {
		m := rt.Method
		if m == "" {
			m = "GET" // 纯前端路由无 method 键，保持 GET 语义
		}
		out = append(out, extractor.Request{
			Raw:    rt.Path,
			Method: m,
			Source: "route:" + rt.Source,
			Kind:   "route",
		})
	}
	return out
}

// toTasks 把提取结果送入 frontier 归一化/去重，转换为下一层任务。
func (c *Crawler) toTasks(reqs []extractor.Request, base *url.URL, depth int, referer string) []task {
	var next []task
	for _, r := range reqs {
		u, ok := c.frontier.Push(r.Raw, base)
		if !ok {
			continue
		}
		// js-ast 来源的高噪音候选分两类处理（I-1 实测数据 + M 批次修正）：
		//   - node_modules/.pnpm 虚拟路径、源码扩展：真噪音，直接丢弃；
		//   - :param 路由模板：不是噪音而是「接口形状」——直接请求必然 404，
		//     但它是 testfill 填充测试请求的输入，按 Confidence=template
		//     只报告不入队（与 routegraph 的 route 同处理模式）。
		// js-ast 来源的高噪音候选分两类处理（I-1 实测 + O-5 修正）：
		//   - :param 路由模板：接口形状，emit-only 不入队（testfill 的输入）；
		//   - node_modules/.pnpm 虚拟路径、源码扩展：入队但降优先级
		//     （deferPage）——I-1 A/B 实测 remix.run 有 27 条真实部署的
		//     .pnpm/.ts 返回 200，丢弃即误伤；排到同层资源之后，配额紧张时
		//     先抓真资产（噪音率不回升），富余时仍抓到（消误伤）。
		noisy := false
		if c.opts.NoiseFilter && r.Source == "js-ast" && noisyJSPath(r.Raw) {
			if hasRouteParam(r.Raw) {
				c.emit(Result{
					URL:        u.String(),
					Method:     r.Method,
					Source:     r.Source,
					Depth:      depth,
					Referer:    referer,
					Confidence: "template",
				})
				continue
			}
			noisy = true
		}
		// 模板必须能锚定到站点根或绝对地址才有意义。
		// webpack 的 `"chunk-" + e + "." + map[e] + ".js"` 这类片段
		// 解析出来是 `/static/js/chunk-EXPR.EXPR.js`，纯属噪音。
		if r.Confidence == "template" &&
			!strings.HasPrefix(r.Raw, "/") && !strings.HasPrefix(r.Raw, "http") {
			continue
		}
		// 带参数占位的前端路由（/user/:id、/files/*）只报告形状，不发请求：
		// SPA 上它们会返回同一个 index.html，把首页重复抓 N 次；
		// 传统站点上则是一串 404。两者都只是浪费配额。
		if r.Kind == "route" && (strings.Contains(r.Raw, ":") || strings.Contains(r.Raw, "*")) {
			c.emit(Result{
				URL:        u.String(),
				Method:     r.Method,
				Source:     r.Source,
				Depth:      depth,
				Referer:    referer,
				Kind:       "route",
				Confidence: "clue",
			})
			// 开关打开时用占位值展开一次。静态爬取拿不到参数取值，
			// 但 `/user/1` 这类地址往往与真实记录走同一套模板，
			// 从而把内页背后的接口调用暴露出来。配额由 MaxRouteExpand 限制。
			if c.opts.RouteExpand && c.reserveRouteExpand() {
				if ep, ok := expandRoutePath(u.Path); ok {
					if eu, ok := c.frontier.Push(ep, base); ok {
						next = append(next, task{
							url:     eu,
							depth:   depth,
							referer: referer,
							source:  "route-expand",
							method:  "GET",
						})
					}
				}
			}
			continue
		}
		// 含占位符的模板只作为「接口形状」输出，不入队：
		// /api/v3/orders/EXPR 这类地址请求必然 404，抓它纯属浪费配额。
		// Params/ParamValues 必须带上——OpenAPI spec（Q-4）与模板路由
		// 的意义正是「形状 + 参数名」，丢了参数名就只剩一条半成品地址。
		if r.Confidence == "template" {
			c.emit(Result{
				URL:         u.String(),
				Method:      r.Method,
				Source:      r.Source,
				Attribute:   r.Attribute,
				Tag:         r.Tag,
				Depth:       depth,
				Referer:     referer,
				Confidence:  "template",
				Kind:        r.Kind,
				Params:      r.Params,
				ParamValues: r.ParamValues,
			})
			continue
		}
		// 非 GET 候选（表单 action 等）同样只作为「接口形状」输出，
		// 不入队：爬虫的抓取语义是 GET，POST action 入队只会得到
		// 404 噪音（I-1 实测数据）；而 method + 参数名 + 参数候选正是
		// testfill 填充与发送确认的输入（P-2 修正）。
		if r.Method != "" && r.Method != "GET" {
			c.emit(Result{
				URL:         u.String(),
				Method:      r.Method,
				Source:      r.Source,
				Attribute:   r.Attribute,
				Tag:         r.Tag,
				Depth:       depth,
				Referer:     referer,
				Confidence:  "interface",
				Kind:        r.Kind,
				Params:      r.Params,
				ParamValues: r.ParamValues,
			})
			// 授权场景下库调用方可额外按 GET 语义抓一次 action 地址，
			// 用于发现页面背后的链接；404 由 isProbeSource 丢弃。
			if c.opts.FollowFormActions {
				next = append(next, task{
					url: u, depth: depth, referer: referer,
					source: "form-action", method: "GET",
					attribute: r.Attribute, tag: r.Tag,
					kind: r.Kind, params: r.Params, paramValues: r.ParamValues,
				})
			}
			continue
		}
		next = append(next, task{
			url:         u,
			depth:       depth,
			referer:     referer,
			source:      r.Source,
			attribute:   r.Attribute,
			tag:         r.Tag,
			method:      r.Method,
			kind:        r.Kind,
			params:      r.Params,
			paramValues: r.ParamValues,
			deferPage:   shouldDeferPage(r.Source, r.Attribute, noisy),
			boost:       r.Source == "json-pagination",
		})
	}
	return next
}

// dedupeTasks 按规范化 URL 对同层任务去重，保留首次出现的那条。
//
// 保留首次出现而不是「合并来源」是有意的：来源标记用来排查误报，
// 第一个发现者的上下文最有解释力；后来者的来源信息在 frontier 的
// 入队去重里本就已丢弃，这里保持一致。
//
// 只做去重，不登记。纯函数，不修改入参。
func dedupeTasks(tasks []task) []task {
	return dedupeWith(tasks, nil)
}

// dedupeAndMark 在 dedupeTasks 的基础上把保留下来的 URL 登记进已见集合。
//
// 为什么需要：runLevel 是按批并发派发的，同一页面的候选会分别来自
// 「sitemap 引导（depth 1）」与「页面里的链接（depth 2）」两条路径。
// 只在入队时登记、派发时去重，会留下一个空洞：抓取期间新发现的同址候选
// 已经在上游被 frontier 放行，若不在派发时补登记，下一层还会再抓一次。
// 登记本身是幂等的（MarkSeen 见重复即返回 false），已抓过的 URL 重复
// 登记不会产生副作用。
func (c *Crawler) dedupeAndMark(tasks []task) []task {
	return dedupeWith(tasks, func(t task) {
		c.frontier.MarkSeen(t.url)
	})
}

// dedupeWith 是两者的共同实现：按 t.url.String() 去重，可用 mark 回调登记。
func dedupeWith(tasks []task, mark func(task)) []task {
	if len(tasks) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tasks))
	out := make([]task, 0, len(tasks))
	for _, t := range tasks {
		if t.url == nil {
			continue
		}
		key := t.url.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if mark != nil {
			mark(t)
		}
		out = append(out, t)
	}
	return out
}

// shouldDeferPage 判定一个候选是否应排在同层配额的最后。
//
// 「可以晚一点抓」的类别都是「一次能产出上百条」的批量来源：噪音页、
// inline-json（hydration payload 里的整站导航树）、sourcemap sources
// （构建机源码路径，O-4 实测单站 202 条）。它们与同层 JS/CSS 抢
// MaxURLs 配额时会挤掉主召回（Nuxt 首页实测 JS 提取 62 → 16 条）。
//
// 注意：延后不是丢弃——live 与否由请求本身决定（以获取到 URL 资源为准）。
// 纯函数。
func shouldDeferPage(source, attribute string, noisy bool) bool {
	if noisy {
		return true
	}
	if strings.HasPrefix(source, "inline-json:") {
		return true
	}
	return source == "sourcemap" && attribute == "sources"
}

// isProbeSource 判断任务是否属于「猜测型探测」来源：它们的 URL 是
// 字典/清单猜出来的，404 就是 miss。
func isProbeSource(source string) bool {
	switch source {
	case "fuzz", "openapi-probe", "robots-disallow", "form-action":
		return true
	}
	return false
}

// resultOf 把抓取响应与任务上下文组装成对外结果。
func (c *Crawler) resultOf(t task, res *fetcher.Response) Result {
	src := t.source
	if src == "" {
		src = "page"
	}
	method := t.method
	if method == "" {
		method = "GET"
	}
	return Result{
		URL:         res.URL.String(),
		Method:      method,
		Source:      src,
		Attribute:   t.attribute,
		Tag:         t.tag,
		Depth:       t.depth,
		StatusCode:  res.StatusCode,
		ContentType: res.ContentType,
		Referer:     t.referer,
		Kind:        t.kind,
		Params:      t.params,
		ParamValues: t.paramValues,
	}
}

// emit 回调并收集结果。
//
// 回调通过独立的 emitMu 串行化：抓取是并发的，但调用方不需要为了
// 往文件/channel 写结果而自己加锁。
func (c *Crawler) emit(r Result) {
	c.emitMu.Lock()
	defer c.emitMu.Unlock()

	if c.opts.OnResult != nil {
		c.opts.OnResult(r)
	}
	c.mu.Lock()
	c.results = append(c.results, r)
	c.mu.Unlock()
}

// emitError 串行化地派发错误回调，避免调用方需要自己加锁。
func (c *Crawler) emitError(url string, err error) {
	if c.opts.OnError == nil || err == nil {
		return
	}
	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	c.opts.OnError(url, err)
}

// reserveFetch 预留一次抓取配额。
func (c *Crawler) reserveFetch() bool {
	if c.opts.MaxURLs <= 0 {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fetched >= c.opts.MaxURLs {
		return false
	}
	c.fetched++
	return true
}

// reserveRouteExpand 预留一次路由展开配额。
func (c *Crawler) reserveRouteExpand() bool {
	if c.opts.MaxRouteExpand <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.routeExpanded >= c.opts.MaxRouteExpand {
		return false
	}
	c.routeExpanded++
	return true
}

// isCancellation 判断错误是否只是上下文取消，这类错误不应当作采集失败上报。
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
