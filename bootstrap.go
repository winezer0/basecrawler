package basecrawler

import (
	"context"
	"net/url"
	"strings"

	"github.com/winezer0/basecrawler/extractor"
	"github.com/winezer0/basecrawler/frontier"
)

// bootstrap 在正式爬取前处理 robots.txt / sitemap.xml / 字典探测 / spec 探测，
// 把它们提供的入口作为 depth 0 任务返回。
//
// 这三者是资产收集里性价比最高的一步：很多站点的主导航是 JS 渲染的，
// 爬虫顺着 a[href] 走不了几页，但 sitemap.xml 会直接给出全站页面清单。
func (c *Crawler) bootstrap(ctx context.Context, seeds []task) []task {
	if !c.opts.FollowRobots && !c.opts.FollowSitemap &&
		!c.opts.ProbeOpenAPI && !c.opts.FuzzPaths {
		return nil
	}

	// 按 origin 去重，多个种子同域时只探一次
	origins := make(map[string]*url.URL)
	var order []string
	for _, s := range seeds {
		o := &url.URL{Scheme: s.url.Scheme, Host: s.url.Host}
		k := o.String()
		if _, ok := origins[k]; !ok {
			origins[k] = o
			order = append(order, k)
		}
	}

	var out []task
	// add 把引导来源转成任务。depth 由调用方决定：
	// sitemap 页面属于「从站点根再走一跳」，必须放 depth 1，
	// 否则几十上百条 sitemap 会以 depth 0 占满整个抓取配额，
	// 种子页里提取出的 JS/CSS 一条都轮不到。
	add := func(raw, source string, depth int) {
		u, added := c.frontier.Push(raw, nil)
		if !added {
			return
		}
		out = append(out, task{url: u, depth: depth, source: source, method: "GET"})
	}

	for _, k := range order {
		if ctx.Err() != nil {
			break
		}
		origin := origins[k]

		var sitemaps []string
		if c.opts.FollowRobots {
			var disallow, allow []string
			sitemaps, disallow, allow = c.readRobots(ctx, origin)
			for _, u := range disallow {
				if c.opts.FollowRobotsDisallow {
					// API 发现定位：Disallow 路径也入队抓取，命中 200/401/403 即真接口。
					add(u, "robots-disallow", 0)
				} else {
					c.emit(Result{URL: u, Method: "GET", Source: "robots", Depth: 0, Confidence: "clue"})
				}
			}
			// Allow 是站点明确允许抓取的路径，只作为线索输出、不自动入队：
			// 它们通常已在正常导航里，额外入队只会增加请求量。
			for _, u := range allow {
				c.emit(Result{URL: u, Method: "GET", Source: "robots:allow", Depth: 0, Confidence: "clue"})
			}
		}
		if c.opts.FollowSitemap {
			if len(sitemaps) == 0 {
				sitemaps = []string{origin.String() + "/sitemap.xml"}
			}
			seen := make(map[string]struct{})
			for _, sm := range sitemaps {
				c.walkSitemap(ctx, sm, 0, seen, func(raw string) { add(raw, "sitemap", 1) })
			}
		}
		// Q-4：OpenAPI/Swagger spec 探测（默认开启）。
		//
		// 只探已知候选路径，深度 0——spec 命中时能一次给出整套接口契约
		//（路径 + 方法 + 参数名）；不命中就只是 14 次便宜请求。
		if c.opts.ProbeOpenAPI {
			for _, p := range extractor.OpenAPIProbePaths {
				add(origin.String()+p, "openapi-probe", 0)
			}
		}
		// 路径 fuzz：字典条目作为 depth 0 任务，命中后该
		// 页面进入正常爬取与提取，等于给爬虫补上缺失的导航入口。
		if c.opts.FuzzPaths {
			dict := c.opts.FuzzDict
			if len(dict) == 0 {
				dict = BuiltinFuzzDict()
			}
			for _, p := range dict {
				add(origin.String()+"/"+strings.TrimPrefix(p, "/"), "fuzz", 0)
			}
		}
	}
	return out
}

// readRobots 读取 robots.txt，返回 sitemap 地址与可还原的 Disallow / Allow 路径。
// 是否把 Disallow 入队由 Options.FollowRobotsDisallow 决定（默认入队）；
// Allow 始终只输出线索。含 *$ 的通配路径与范围外路径不再静默跳过，
// 跳过量记入 LimitReport，供 [limit] 汇总查看。
func (c *Crawler) readRobots(ctx context.Context, origin *url.URL) ([]string, []string, []string) {
	u, ok := frontier.Normalize(origin.String()+"/robots.txt", nil)
	if !ok {
		return nil, nil, nil
	}
	res, err := c.fetcher.Get(ctx, u, "")
	if res == nil || res.StatusCode != 200 || len(res.Body) == 0 {
		if err != nil && !isCancellation(err) {
			c.emitError(u.String(), err)
		}
		return nil, nil, nil
	}

	info := extractor.ParseRobots(res.Body)
	var disallow, allow []string
	total, kept := 0, 0
	collect := func(paths []string, out *[]string) {
		for _, p := range dedupStrings(paths) {
			total++
			// 通配符与正则式路径无法直接还原成可访问地址，跳过
			if strings.ContainsAny(p, "*$") {
				continue
			}
			clue, ok := frontier.Normalize(origin.String()+"/"+strings.TrimPrefix(p, "/"), nil)
			if !ok {
				continue
			}
			// -i（InsidePath）下路径外的线索对调用方没有意义，与入队口径一致。
			if !c.frontier.PathAllowed(clue) {
				continue
			}
			*out = append(*out, clue.String())
			kept++
		}
	}
	collect(info.Disallow, &disallow)
	collect(info.Allow, &allow)
	c.limits.Record(extractor.LimitEvent{
		Kind:       extractor.LimitRobotsWildcards,
		Candidates: total,
		Emitted:    kept,
		Truncated:  total - kept,
	})
	return resolveAgainst(u, dedupStrings(info.Sitemaps)), disallow, allow
}

// walkSitemap 递归解析 sitemap（含 sitemap index），把页面地址交给 add。
func (c *Crawler) walkSitemap(ctx context.Context, raw string, depth int, seen map[string]struct{}, add func(string)) {
	if depth > 3 || ctx.Err() != nil {
		return
	}
	u, ok := frontier.Normalize(raw, nil)
	if !ok {
		return
	}
	k := u.String()
	if _, dup := seen[k]; dup {
		return
	}
	seen[k] = struct{}{}

	res, err := c.fetcher.Get(ctx, u, "")
	if res == nil || res.StatusCode != 200 || len(res.Body) == 0 {
		if err != nil && !isCancellation(err) {
			c.emitError(u.String(), err)
		}
		return
	}

	urls, sitemaps := extractor.ParseSitemap(res.Body)
	for _, p := range urls {
		// sitemap 规范要求 loc 是绝对地址，但现实中存在写相对路径的站点，
		// 这里用 sitemap 自身的地址做基准兜一层。
		if ru, ok := frontier.Normalize(p, u); ok {
			add(ru.String())
		}
	}
	for _, sm := range sitemaps {
		c.walkSitemap(ctx, sm, depth+1, seen, add)
	}
}

// resolveAgainst 把可能是相对路径的地址按基准 URL 展开，丢弃无法解析的项。
func resolveAgainst(base *url.URL, in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if u, ok := frontier.Normalize(s, base); ok {
			out = append(out, u.String())
		}
	}
	return out
}

// dedupStrings 保序去重。
func dedupStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
