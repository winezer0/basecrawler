package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/winezer0/basecrawler/safety"
)

// cliConfig 是命令行解析的完整结果：采集选项 + 输出控制。
//
// 之所以把解析从 main 里拆出来，是因为「flag 名字与 Options 字段的映射」
// 是最容易写错、又最没人测的一层：改一个默认值、把 -no-js 写成取反、
// 新增一个开关忘了接到 Options 上，所有这些只有跑起来才看得见。
// 拆成纯函数后可以用表驱动用例逐个锁定。
//
// 解析引擎是 kong（结构体 tag 驱动）：flag 定义、默认值、help 文本集中在
// cliSpec 一个类型上，parseArgs 只负责 spec → cliConfig/Options 的映射。
// CLI 契约是 GNU 风格：多字符参数 --max-urls，单字符 -u/-d/-O 等保留短名。
type cliConfig struct {
	// URLs 目标地址，来自可重复的 -u，逗号分隔也会被拆开。
	URLs []string
	// Opts 与命令行参数一一对应。
	Opts *assetcrawler.Options
	// OutputPath 输出文件路径；空表示 stdout。
	OutputPath string
	// OutputType 输出格式：json（JSONL）| txt（纯 URL），默认 json。
	OutputType string
	// Silent 是否只输出 URL、不打汇总。
	Silent bool
	// Verbose 是否把错误打到 stderr。
	Verbose bool
	// StatusFilter 是 -s 白名单与 -S 黑名单组装出的输出过滤器。
	StatusFilter statusFilter
	// TotalTimeout 是 -ct 解析出的总时长上限，0 表示不限。
	TotalTimeout time.Duration
	// SimilarFilter 是否启用相似 URL 过滤（-fsu）。
	SimilarFilter bool
	// TestData 测试请求产物格式（空=不生成）：curl/python/postman/http/json。
	TestData string
	// TestDataOut 测试请求产物输出路径（空=stdout）。
	TestDataOut string
	// TestDataAuth 测试请求的认证头模板（如 "Authorization: Bearer ${TOKEN}"）。
	TestDataAuth string
	// TestFill 是否生成并自动发送测试请求（默认开，--no-testfill 关闭）。
	// 不再区分幂等/非幂等方法；危险动作由 safety 关键字拦截。
	TestFill bool
	// Safety 危险操作拦截器（--no-safety 关闭）。
	Safety *safety.Safety
	// UseHiddenValues 是否把 hidden/select 静态值原样填入请求。
	UseHiddenValues bool
	// MaxTestFill 单次采集的测试请求发送上限，0 不限。
	MaxTestFill int
	// Version 是否只查询版本（早于一切初始化退出）。
	Version bool
}

// errNoTarget 表示没有给出任何 -u。
var errNoTarget = errors.New("至少需要一个 -u 目标")

// multiFlag 是可重复、可逗号分隔的字符串参数。
// kong 通过 Decode 接口识别自定义映射器，无需注册。
type multiFlag []string

func (m *multiFlag) Decode(ctx *kong.DecodeContext, _ reflect.Type) error {
	var v string
	if err := ctx.Scan.PopValueInto("flag", &v); err != nil {
		return err
	}
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*m = append(*m, p)
		}
	}
	return nil
}

// cliSpec 是 kong 的 flag 定义结构体。tag 只写 name/short/default/help，
// 语义校验（正则、枚举、状态码范围）保留在 parseArgs 的映射段——
// 它们需要带上下文的错误信息，kong 的 enum 报错做不到。
type cliSpec struct {
	URLs    multiFlag `kong:"name=u,short=u,help=target URL (repeatable or comma-separated)"`
	Headers multiFlag `kong:"name=H,short=H,help='extra header, e.g. -H Cookie: a=b (repeatable)'"`

	Depth        int    `kong:"name=d,short=d,default=3,help=max crawl depth (0 = unlimited)"`
	Concurrency  int    `kong:"name=c,short=c,default=10,help=global concurrency"`
	PerHost      int    `kong:"name=phc,default=4,help=per-host concurrency"`
	RateLimit    int    `kong:"name=rl,default=0,help=global requests per second (0 = unlimited)"`
	Timeout      int    `kong:"name=timeout,default=10,help=per-request timeout in seconds"`
	MaxURLs      int    `kong:"name=max-urls,default=2000,help=max pages to fetch"`
	MaxBody      int64  `kong:"name=max-body,default=2097152,help=max response body bytes"`
	Scope        string `kong:"name=scope,default=rdn,help=scope: rdn | fqdn | dn | regex | all (aliases: subdomain | same-host | any)"`
	ScopeRegex   string `kong:"name=scope-regex,default=,help='custom scope regex, used with -scope regex (matches full URL)'"`
	MDP          int    `kong:"name=mdp,default=0,help='max HTML pages per host, 0 = unlimited (crawler trap guard)'"`
	Status       string `kong:"name=status,short=s,default='200,204,301,302,307,401,403,405,500',help='status codes to include (allow list), comma separated with ranges, e.g. 200-399,401,403 or all'"`
	FilterStatus string `kong:"name=filter-status,short=S,default=,help='status codes to exclude (deny list), comma separated with ranges, e.g. 404,500-599'"`
	CrawlTimeout string `kong:"name=ct,default=,help='total crawl time limit, e.g. 5m / 90s / 90 (bare number = seconds); 0 or empty = unlimited'"`

	NoDeobf     bool `kong:"name=no-deobf,group='Active actions',help='disable signature detection + fallback unpack (enabled by default)'"`
	NoDeobfExec bool `kong:"name=no-deobf-exec,group='Active actions',help='disable sandboxed execution of string-array decoders (enabled by default with signature gate)'"`
	Similar     bool `kong:"name=fsu,help=similar URL filter (path-shape merging via winezer0/requestfilter; /users/123 ≈ /users/456 collapses after 10 distinct values)"`

	Proxy       string `kong:"name=proxy,default=,help='proxy URL, e.g. http://127.0.0.1:8080'"`
	Output      string `kong:"name=o,short=o,default=,help=output file (parent dirs auto-created); default: <sanitized-seed-url>_<timestamp>.<ext>"`
	OutType     string `kong:"name=O,short=O,default=json,help=output format: json (JSONL) | txt (plain URLs)"`
	AsJSONL     bool   `kong:"name=jsonl,help=legacy alias for -O json"`
	Insecure    bool   `kong:"name=k,short=k,help=skip TLS verification"`
	NoRedirects bool   `kong:"name=dr,help=do not follow redirects (redirect targets are still reported)"`
	InsidePath  bool   `kong:"name=i,short=i,help=only crawl inside the seed path (e.g. seed /en-US/ keeps /en-US/* only)"`

	NoRobots          bool   `kong:"name=no-robots,group='Active actions',help=do not read robots.txt"`
	NoRobotsDisallow  bool   `kong:"name=no-robots-disallow,group='Active actions',help='do not enqueue robots.txt Disallow paths (enabled by default)'"`
	FollowFormActions bool   `kong:"name=follow-form-actions,group='Active actions',help='enqueue non-GET form actions for an extra GET fetch (off by default)'"`
	NoSitemap         bool   `kong:"name=no-sitemap,group='Active actions',help=do not read sitemap.xml"`
	NoJS              bool   `kong:"name=no-js,help=disable JS/CSS endpoint extraction"`
	JSEngine          string `kong:"name=js-engine,default=auto,help=JS extraction engine: auto | regex | goja | treesitter"`
	NoChunk           bool   `kong:"name=no-chunk-graph,help=disable chunk/route graph recovery from bundles"`
	NoRouteExec       bool   `kong:"name=no-route-exec,group='Active actions',help='disable execution of bundle JS to recover runtime routes (enabled by default with bundle signature gate)'"`
	NoRouteExpand     bool   `kong:"name=no-route-expand,group='Active actions',help='disable expansion of :param routes (enabled by default, capped by --route-expand-max)'"`
	RouteMax          int    `kong:"name=route-expand-max,group='Active actions',default=50,help=max routes to expand when -route-expand is on"`
	MaxSeen           int    `kong:"name=max-seen,default=200000,help=cap on the in-memory seen set (0 = unlimited)"`
	NoCSP             bool   `kong:"name=no-csp,help=do not report CSP allow-list hosts and report endpoints"`
	StableOrder       bool   `kong:"name=stable-host-order,help=deterministic per-host task order for reproducible baselines (fewer URLs under quota)"`
	NoNoise           bool   `kong:"name=no-noise-filter,help='do not drop noisy js-ast candidates (node_modules paths, source-file extensions, :param routes)'"`
	NoCurl            bool   `kong:"name=no-curl-blocks,help=do not emit curl commands found in HTML code blocks (Kind=curl results)"`

	NoFuzz         bool   `kong:"name=no-fuzz,group='Active actions',help='disable builtin path dictionary probing (enabled by default)'"`
	FuzzDictFile   string `kong:"name=fuzz-dict,group='Active actions',default=,help='custom paths appended to the builtin dict (one per line, # comments, deduplicated); --no-fuzz takes precedence'"`
	NoProbeOpenAPI bool   `kong:"name=no-probe-openapi,group='Active actions',help='disable OpenAPI/Swagger spec probing (enabled by default)'"`

	TestData         string    `kong:"name=testdata,default=,help=render filled test requests: curl | python | postman | http | json"`
	TestDataOut      string    `kong:"name=testdata-out,default=,help=write test requests to this path (default stdout)"`
	TestAuth         string    `kong:"name=testdata-auth,default=,help='auth header template for test requests, e.g. Authorization: Bearer TOKEN'"`
	NoTestFill       bool      `kong:"name=no-testfill,group='Active actions',help='disable auto-filled test requests and their submission (enabled by default, all methods)'"`
	NoSafety         bool      `kong:"name=no-safety,group='Active actions',help='disable keyword-based dangerous action blocking (enabled by default), dangerous actions will really fire - use with caution'"`
	SafetyURLSkip    multiFlag `kong:"name=safety-url-skip,group='Active actions',help='extra URL skip regex appended to safety rules (repeatable)'"`
	SafetyFormSkip   multiFlag `kong:"name=safety-form-skip,group='Active actions',help='extra form/action skip regex appended to safety rules (repeatable)'"`
	SafetyActionSkip multiFlag `kong:"name=safety-action-skip,group='Active actions',help='extra non-idempotent action skip regex appended to safety rules (repeatable)'"`
	NoUnsafeSend     bool      `kong:"name=no-unsafe-send,group='Active actions',help='only send idempotent test requests (GET/HEAD/OPTIONS); enabled by default: all methods'"`
	UseHiddenValues  bool      `kong:"name=testfill-use-hidden,group='Active actions',help='fill hidden/select values with their original static values (default: placeholders; may send real csrf/session tokens)'"`
	MaxTestFill      int       `kong:"name=max-testfill,group='Active actions',default=200,help='max test requests actually sent, 0 = unlimited'"`

	ShowVersion bool   `kong:"name=version,help=print version and exit"`
	Silent      bool   `kong:"name=silent,help='only print URLs, no summary'"`
	Verbose     bool   `kong:"name=v,short=v,help=print errors to stderr"`
	UA          string `kong:"name=ua,default=,help=custom User-Agent"`
	Exclude     string `kong:"name=exclude,default=,help=exclude URLs matching this regex"`
	Include     string `kong:"name=include,default=,help=only keep URLs matching this regex"`
}

// parseArgs 解析命令行参数并组装 Options。
//
// 只做解析与映射，不碰网络、不写文件，因此可以直接被测试调用。
func parseArgs(args []string, errOut io.Writer) (*cliConfig, error) {
	var spec cliSpec
	helpShown := false
	parser := kong.Must(&spec,
		kong.Name("assetcrawl"),
		kong.Description("web asset & API endpoint crawler"),
		kong.Writers(errOut, errOut),
		// 帮助/错误路径不允许 kong 直接退出：parseArgs 必须返回给
		// main 统一处理（main 里 -h 静默、其余错误 exit 2）。
		kong.Exit(func(code int) {
			if code == 0 {
				helpShown = true
			}
		}),
	)
	if _, err := parser.Parse(args); err != nil {
		if helpShown {
			return nil, flag.ErrHelp
		}
		return nil, err
	}

	// 版本查询不要求目标：-version 单独即可运行
	if len(spec.URLs) == 0 && !spec.ShowVersion {
		return nil, errNoTarget
	}

	opts := assetcrawler.DefaultOptions()
	opts.MaxDepth = spec.Depth
	opts.Concurrency = spec.Concurrency
	opts.PerHostConcurrency = spec.PerHost
	opts.RateLimit = spec.RateLimit
	opts.MaxURLs = spec.MaxURLs
	opts.MaxBodySize = spec.MaxBody
	opts.Timeout = seconds(spec.Timeout)
	opts.Proxy = spec.Proxy
	opts.Insecure = spec.Insecure
	opts.NoRedirects = spec.NoRedirects
	opts.InsidePath = spec.InsidePath
	opts.MaxPagesPerHost = spec.MDP
	opts.FollowRobots = !spec.NoRobots
	opts.FollowRobotsDisallow = !spec.NoRobotsDisallow
	opts.FollowFormActions = spec.FollowFormActions
	opts.AllowUnsafeSend = !spec.NoUnsafeSend
	opts.FollowSitemap = !spec.NoSitemap
	opts.ExtractJS = !spec.NoJS
	// 引擎名必须校验：JSEngine 的零值等价于 auto，拼错的名字会静默
	// 落到默认分支——用户以为指定了 goja，其实拿到的可能不是他想要的。
	switch name := assetcrawler.JSEngine(spec.JSEngine); name {
	case assetcrawler.JSEngineAuto, assetcrawler.JSEngineRegex,
		assetcrawler.JSEngineGoja, assetcrawler.JSEngineTreesitter:
		opts.JSEngine = name
	default:
		return nil, fmt.Errorf("invalid -js-engine %q: want auto|regex|goja|treesitter", spec.JSEngine)
	}
	opts.ExtractChunkGraph = !spec.NoChunk
	opts.RouteExec = !spec.NoRouteExec
	opts.DeobfExec = !spec.NoDeobfExec
	opts.RouteExpand = !spec.NoRouteExpand
	opts.MaxRouteExpand = spec.RouteMax
	opts.Deobfuscate = !spec.NoDeobf
	opts.DeobfForce = !spec.NoDeobf
	opts.MaxSeen = spec.MaxSeen
	opts.CSPHints = !spec.NoCSP
	opts.StableHostQuota = spec.StableOrder
	opts.NoiseFilter = !spec.NoNoise
	opts.ExtractCurlBlocks = !spec.NoCurl
	opts.ProbeOpenAPI = !spec.NoProbeOpenAPI
	opts.FuzzPaths = !spec.NoFuzz
	if spec.FuzzDictFile != "" {
		content, err := os.ReadFile(spec.FuzzDictFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read -fuzz-dict: %w", err)
		}
		dict := assetcrawler.ParseFuzzDict(string(content))
		if len(dict) == 0 {
			return nil, fmt.Errorf("-fuzz-dict %q has no usable paths", spec.FuzzDictFile)
		}
		// 追加语义：内置字典 + 自定义路径，保序去重。
		opts.FuzzDict = assetcrawler.AppendFuzzDict(assetcrawler.BuiltinFuzzDict(), dict)
	}
	if spec.UA != "" {
		opts.UserAgent = spec.UA
	}

	switch spec.Scope {
	case "fqdn", "same-host":
		opts.Scope = assetcrawler.ScopeSameHost
	case "dn":
		opts.Scope = assetcrawler.ScopeDN
	case "regex":
		opts.Scope = assetcrawler.ScopeRegex
	case "all", "any":
		opts.Scope = assetcrawler.ScopeAny
	default:
		// rdn / subdomain
		opts.Scope = assetcrawler.ScopeSubdomain
	}
	if spec.ScopeRegex != "" {
		re, err := regexp.Compile(spec.ScopeRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid -scope-regex: %w", err)
		}
		opts.ScopeRegex = re
	}
	if opts.Scope == assetcrawler.ScopeRegex && opts.ScopeRegex == nil {
		return nil, fmt.Errorf("-scope regex 需要配合 -scope-regex 提供正则")
	}

	if spec.Exclude != "" {
		re, err := regexp.Compile(spec.Exclude)
		if err != nil {
			return nil, fmt.Errorf("invalid -exclude regex: %w", err)
		}
		opts.ExcludeRegex = append(opts.ExcludeRegex, re)
	}
	if spec.Include != "" {
		re, err := regexp.Compile(spec.Include)
		if err != nil {
			return nil, fmt.Errorf("invalid -include regex: %w", err)
		}
		opts.IncludeRegex = append(opts.IncludeRegex, re)
	}

	applyHeaders(opts, spec.Headers)

	// 状态码输出过滤：-s 白名单 + -S 黑名单，判定集中在 statusFilter.Allow。
	statusSel, err := newStatusFilter(spec.Status, spec.FilterStatus)
	if err != nil {
		return nil, err
	}

	// -ct 总时长上限：裸数字按秒、否则按 Go duration 语法解析；0 = 不限。
	totalTimeout := time.Duration(0)
	if ct := strings.TrimSpace(spec.CrawlTimeout); ct != "" && ct != "0" {
		d, err := parseDurationSeconds(ct)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid -ct %q: want e.g. 5m, 90s or 90", spec.CrawlTimeout)
		}
		totalTimeout = d
	}

	// -O 输出类型校验；-jsonl 是旧别名，映射到 json。
	outTypeName := strings.ToLower(strings.TrimSpace(spec.OutType))
	if spec.AsJSONL {
		outTypeName = "json"
	}
	switch outTypeName {
	case "json", "txt":
	default:
		return nil, fmt.Errorf("invalid -O %q: want json|txt", spec.OutType)
	}

	// 未指定 -o 时按首个种子 URL 自动命名，特殊符号转下划线。
	outputPath := spec.Output
	if outputPath == "" && len(spec.URLs) > 0 {
		ext := ".jsonl"
		if outTypeName == "txt" {
			ext = ".txt"
		}
		outputPath = defaultOutputPath(spec.URLs[0], ext, time.Now())
	}

	// safety 危险操作拦截：三类词表都允许从 CLI 追加规则；非法正则在
	// 解析阶段报错退出，不留半防护状态。
	safetyFilter, err := safety.NewSafetyWithActions(!spec.NoSafety,
		[]string(spec.SafetyURLSkip), []string(spec.SafetyFormSkip),
		[]string(spec.SafetyActionSkip))
	if err != nil {
		return nil, err
	}

	return &cliConfig{
		URLs:            spec.URLs,
		Opts:            opts,
		OutputPath:      outputPath,
		OutputType:      outTypeName,
		Silent:          spec.Silent,
		Verbose:         spec.Verbose,
		StatusFilter:    statusSel,
		TotalTimeout:    totalTimeout,
		SimilarFilter:   spec.Similar,
		TestData:        spec.TestData,
		TestDataOut:     spec.TestDataOut,
		TestDataAuth:    spec.TestAuth,
		TestFill:        !spec.NoTestFill,
		Safety:          safetyFilter,
		UseHiddenValues: spec.UseHiddenValues,
		MaxTestFill:     spec.MaxTestFill,
		Version:         spec.ShowVersion,
	}, nil
}

// parseDurationSeconds 解析时长：裸数字按秒，其余交给 time.ParseDuration（如 5m、90s、1h30m）。
func parseDurationSeconds(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return time.ParseDuration(s)
}

// defaultOutputPath 从种子 URL 生成默认输出文件名：host+path 中
// [A-Za-z0-9._-] 以外的字符一律转下划线，追加时间戳防覆盖。
func defaultOutputPath(seed, ext string, now time.Time) string {
	s := seed
	if u, err := url.Parse(seed); err == nil && u.Host != "" {
		s = u.Host + u.Path
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "result"
	}
	return fmt.Sprintf("%s_%s%s", name, now.Format("20060102-150405"), ext)
}

// applyHeaders 把 "K: V" 形式的 -H 合并进 Options.Headers。
// 没有冒号的条目直接忽略：它不可能是合法头，报错反而打断批量脚本。
func applyHeaders(opts *assetcrawler.Options, headers []string) {
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if opts.Headers == nil {
			opts.Headers = make(map[string][]string)
		}
		opts.Headers[k] = append(opts.Headers[k], strings.TrimSpace(v))
	}
}

// seconds 把秒数转成 time.Duration，供 flag 使用。
func seconds(n int) time.Duration {
	if n <= 0 {
		return 10 * time.Second
	}
	return time.Duration(n) * time.Second
}
