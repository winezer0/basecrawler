package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/winezer0/basecrawler"
)

// TestParseArgsDefaults 锁定「不给任何可选参数时的映射」。
//
// 默认值最容易在重构里被悄悄改掉：改一个数字不会有任何测试变红，
// 但会改变所有使用者的行为。
func TestParseArgsDefaults(t *testing.T) {
	cfg, err := parseArgs([]string{"-u", "https://example.com/"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(cfg.URLs) != 1 || cfg.URLs[0] != "https://example.com/" {
		t.Errorf("URLs = %v", cfg.URLs)
	}

	o := cfg.Opts
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"MaxDepth", o.MaxDepth, 3},
		{"Concurrency", o.Concurrency, 10},
		{"PerHostConcurrency", o.PerHostConcurrency, 4},
		{"RateLimit", o.RateLimit, 0},
		{"MaxURLs", o.MaxURLs, 2000},
		{"MaxBodySize", o.MaxBodySize, int64(2 << 20)},
		{"Timeout", o.Timeout, 10 * time.Second},
		{"Scope", o.Scope, basecrawler.ScopeSubdomain},
		{"FollowRobots", o.FollowRobots, true},
		{"FollowRobotsDisallow", o.FollowRobotsDisallow, true},
		{"FollowSitemap", o.FollowSitemap, true},
		{"ExtractJS", o.ExtractJS, true},
		{"JSEngine", o.JSEngine, basecrawler.JSEngineAuto},
		{"ExtractChunkGraph", o.ExtractChunkGraph, true},
		{"RouteExec", o.RouteExec, true},
		{"DeobfExec", o.DeobfExec, true},
		{"RouteExpand", o.RouteExpand, true},
		{"FuzzPaths", o.FuzzPaths, true},
		{"ProbeOpenAPI", o.ProbeOpenAPI, true},
		{"Deobfuscate", o.Deobfuscate, true},
		{"DeobfForce", o.DeobfForce, true},
		{"MaxRouteExpand", o.MaxRouteExpand, 50},
		{"StableHostQuota", o.StableHostQuota, false},
		{"NoiseFilter", o.NoiseFilter, true},
	}
	if !cfg.TestFill {
		t.Error("默认应开启 testfill 自动发送请求（TestFill=true）")
	}
	for _, c := range []struct {
		code int
		want bool
	}{
		{200, true}, {204, true}, {301, true}, {302, true}, {307, true},
		{401, true}, {403, true}, {405, true}, {500, true},
		{0, false}, {201, false}, {404, false}, {503, false},
	} {
		if got := cfg.StatusFilter.Allow(c.code); got != c.want {
			t.Errorf("默认码表 Allow(%d) = %v，期望 %v", c.code, got, c.want)
		}
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v，期望 %v", c.name, c.got, c.want)
		}
	}
}

// TestParseArgsMapping 逐个 flag 验证「名字 → 字段」的接线没有错位。
func TestParseArgsMapping(t *testing.T) {
	args := []string{
		"-u", "https://a.example/", "-u", "https://b.example/",
		"-d", "5", "-c", "20", "--phc", "8", "--rl", "50", "--timeout", "3",
		"--max-urls", "100", "--max-body", "1024",
		"--scope", "same-host", "--proxy", "http://127.0.0.1:8080", "-k",
		"--no-robots", "--no-sitemap", "--no-js", "--js-engine", "treesitter",
		"--no-chunk-graph", "--no-route-exec", "--no-deobf-exec", "--no-route-expand",
		"--no-fuzz", "--no-probe-openapi", "--no-deobf", "--route-expand-max", "7",
		"--ua", "probe/1.0", "-o", "out.txt", "--jsonl", "--silent", "-v",
		"-s", "200,403,404", "-S", "404",
		"--no-csp", "--stable-host-order", "--no-noise-filter", "--no-curl-blocks",
		"--testdata", "curl", "--testdata-out", "test.sh", "--testdata-auth", "Authorization: Bearer ${TOKEN}",
		"--no-testfill", "--no-robots-disallow",
	}
	cfg, err := parseArgs(args, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(cfg.URLs) != 2 {
		t.Errorf("URLs = %v，期望 2 条", cfg.URLs)
	}
	if cfg.OutputPath != "out.txt" || cfg.OutputType != "json" || !cfg.Silent || !cfg.Verbose {
		t.Errorf("输出控制未接线：%+v", cfg)
	}
	if !cfg.StatusFilter.Allow(200) || !cfg.StatusFilter.Allow(403) ||
		cfg.StatusFilter.Allow(404) || cfg.StatusFilter.Allow(500) {
		t.Error("-s / -S 映射错误：白名单 200/403 应放行，黑名单 404 与白名单外的 500 应丢弃")
	}
	if cfg.TestFill {
		t.Error("--no-testfill 应置 TestFill=false")
	}

	o := cfg.Opts
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"MaxDepth", o.MaxDepth, 5},
		{"Concurrency", o.Concurrency, 20},
		{"PerHostConcurrency", o.PerHostConcurrency, 8},
		{"RateLimit", o.RateLimit, 50},
		{"MaxURLs", o.MaxURLs, 100},
		{"MaxBodySize", o.MaxBodySize, int64(1024)},
		{"Timeout", o.Timeout, 3 * time.Second},
		{"Scope", o.Scope, basecrawler.ScopeSameHost},
		{"Proxy", o.Proxy, "http://127.0.0.1:8080"},
		{"Insecure", o.Insecure, true},
		{"FollowRobots", o.FollowRobots, false},
		{"FollowRobotsDisallow", o.FollowRobotsDisallow, false},
		{"FollowSitemap", o.FollowSitemap, false},
		{"ExtractJS", o.ExtractJS, false},
		{"JSEngine", o.JSEngine, basecrawler.JSEngineTreesitter},
		{"ExtractChunkGraph", o.ExtractChunkGraph, false},
		{"RouteExec", o.RouteExec, false},
		{"DeobfExec", o.DeobfExec, false},
		{"RouteExpand", o.RouteExpand, false},
		{"FuzzPaths", o.FuzzPaths, false},
		{"ProbeOpenAPI", o.ProbeOpenAPI, false},
		{"Deobfuscate", o.Deobfuscate, false},
		{"DeobfForce", o.DeobfForce, false},
		{"MaxRouteExpand", o.MaxRouteExpand, 7},
		{"UserAgent", o.UserAgent, "probe/1.0"},
		{"CSPHints", o.CSPHints, false},
		{"StableHostQuota", o.StableHostQuota, true},
		{"NoiseFilter", o.NoiseFilter, false},
		{"ExtractCurlBlocks", o.ExtractCurlBlocks, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v，期望 %v", c.name, c.got, c.want)
		}
	}
}

// TestParseArgsScope 覆盖 scope 三档：非法值回落到默认的 subdomain 而不是报错，
// 因为扫描脚本里偶尔会传入旧版本的值，中断整个批处理不划算。
func TestParseArgsScope(t *testing.T) {
	cases := map[string]basecrawler.Scope{
		"same-host": basecrawler.ScopeSameHost,
		"subdomain": basecrawler.ScopeSubdomain,
		"any":       basecrawler.ScopeAny,
		"bogus":     basecrawler.ScopeSubdomain,
	}
	for in, want := range cases {
		cfg, err := parseArgs([]string{"-u", "https://x/", "--scope", in}, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("scope=%s: %v", in, err)
		}
		if cfg.Opts.Scope != want {
			t.Errorf("scope=%s 映射到 %v，期望 %v", in, cfg.Opts.Scope, want)
		}
	}
}

// TestParseArgsHeaders 覆盖 -H 的两种写法与两类脏数据。
func TestParseArgsHeaders(t *testing.T) {
	cfg, err := parseArgs([]string{
		"-u", "https://x/",
		"-H", "Cookie: a=b",
		"-H", "X-Token: t1",
		"-H", "X-Token: t2",
		"-H", "no-colon-here",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	h := cfg.Opts.Headers
	if got := h["Cookie"]; len(got) != 1 || got[0] != "a=b" {
		t.Errorf("Cookie = %v", got)
	}
	if got := h["X-Token"]; len(got) != 2 || got[0] != "t1" || got[1] != "t2" {
		t.Errorf("X-Token = %v，重复头应累加", got)
	}
	if _, ok := h["no-colon-here"]; ok {
		t.Errorf("无冒号的 -H 应被忽略，实际写入 %v", h)
	}
}

// TestParseArgsErrors 覆盖三类失败：没有目标、正则写错。
func TestParseArgsErrors(t *testing.T) {
	if _, err := parseArgs(nil, &bytes.Buffer{}); err == nil {
		t.Error("没有 -u 时应报错")
	}
	if _, err := parseArgs([]string{"-u", "https://x/", "--exclude", "("}, &bytes.Buffer{}); err == nil {
		t.Error("-exclude 非法正则应报错")
	}
	if _, err := parseArgs([]string{"-u", "https://x/", "--include", "["}, &bytes.Buffer{}); err == nil {
		t.Error("-include 非法正则应报错")
	}
}

// TestMultiFlagComma 验证逗号分隔与重复使用可以混用。
func TestMultiFlagComma(t *testing.T) {
	cfg, err := parseArgs([]string{"-u", "https://a/,https://b/", "-u", "https://c/"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(cfg.URLs) != 3 {
		t.Errorf("URLs = %v，期望 3 条", cfg.URLs)
	}
	for _, u := range cfg.URLs {
		if strings.TrimSpace(u) == "" {
			t.Errorf("URLs 里出现空条目：%v", cfg.URLs)
		}
	}
}

// TestParseArgsFuzzDictAppend 验证 --fuzz-dict 追加到内置字典、跨列表去重。
func TestParseArgsFuzzDictAppend(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "extra.txt")
	// /admin 与内置字典重复，应只留一份；/custom/only 为新增。
	content := "# custom\n/admin\n/admin\ncustom/only\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseArgs([]string{"-u", "https://x/", "--fuzz-dict", f}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	builtin := basecrawler.BuiltinFuzzDict()
	if len(cfg.Opts.FuzzDict) <= len(builtin) {
		t.Fatalf("追加后字典 %d 条，未超过内置 %d 条", len(cfg.Opts.FuzzDict), len(builtin))
	}
	seen := map[string]bool{}
	adminCount, customSeen := 0, false
	for _, p := range cfg.Opts.FuzzDict {
		if seen[p] {
			t.Errorf("字典路径 %q 重复", p)
		}
		seen[p] = true
		if p == "/admin" {
			adminCount++
		}
		if p == "/custom/only" {
			customSeen = true
		}
	}
	if adminCount != 1 || !customSeen {
		t.Errorf("追加语义错误：/admin 出现 %d 次，自定义条目=%v", adminCount, customSeen)
	}
	// 内置字典的首条仍在，说明是追加而非替换。
	if cfg.Opts.FuzzDict[0] != builtin[0] {
		t.Errorf("首条 = %q，期望内置首条 %q", cfg.Opts.FuzzDict[0], builtin[0])
	}
}

// TestParseArgsProbeOpenAPI 验证默认开启、--no-probe-openapi 显式关闭。
func TestParseArgsProbeOpenAPI(t *testing.T) {
	cfg, err := parseArgs([]string{"-u", "https://x/"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.Opts.ProbeOpenAPI {
		t.Error("ProbeOpenAPI 应默认开启")
	}

	cfg, err = parseArgs([]string{"-u", "https://x/", "--no-probe-openapi"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.Opts.ProbeOpenAPI {
		t.Error("--no-probe-openapi 应关闭 spec 探测")
	}
	if _, err := parseArgs([]string{"-u", "https://x/", "--probe-openapi"}, &bytes.Buffer{}); err == nil {
		t.Error("旧正向参数 --probe-openapi 应报 unknown flag")
	}
}

// TestParseArgsBoundaries 覆盖数值类 flag 的边界：非数字、0、负数。
// 这些值要么被 flag 包直接拒绝，要么落到「不限制」语义，不能静默变成
// 一个意外的小值（例如 -max-urls 0 被当成 1 会让人以为跑完了其实只抓一页）。
func TestParseArgsBoundaries(t *testing.T) {
	bad := [][]string{
		{"-u", "https://x/", "-c", "abc"},
		{"-u", "https://x/", "-d", "1.5"},
		{"-u", "https://x/", "--max-urls", "x"},
		{"-u", "https://x/", "--timeout", "abc"},
	}
	for _, args := range bad {
		if _, err := parseArgs(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v 应报错", args)
		}
	}

	// 0 表示不限制：必须原样传递，不能被兜底成别的值
	cfg, err := parseArgs([]string{"-u", "https://x/", "--max-urls", "0", "-d", "0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.Opts.MaxURLs != 0 {
		t.Errorf("MaxURLs = %d，期望 0（不限制）", cfg.Opts.MaxURLs)
	}
	if cfg.Opts.MaxDepth != 0 {
		t.Errorf("MaxDepth = %d，期望 0", cfg.Opts.MaxDepth)
	}
}

// TestParseArgsJSProbe 验证引擎枚举非法值被拒（否则会静默退化成 regex，
// 用户以为拿到了 AST 能力却其实没有）。
func TestParseArgsJSProbe(t *testing.T) {
	if _, err := parseArgs([]string{"-u", "https://x/", "--js-engine", "v8"}, &bytes.Buffer{}); err == nil {
		t.Error("非法引擎名应报错")
	}
	for _, name := range []string{"auto", "regex", "goja", "treesitter"} {
		if _, err := parseArgs([]string{"-u", "https://x/", "--js-engine", name}, &bytes.Buffer{}); err != nil {
			t.Errorf("引擎 %q 应被接受: %v", name, err)
		}
	}
}

// TestParseArgsScope 验证 scope 五档命名、旧别名兼容与 regex 组合校验。
func TestParseArgsScopeNames(t *testing.T) {
	cases := map[string]basecrawler.Scope{
		"rdn":       basecrawler.ScopeSubdomain,
		"subdomain": basecrawler.ScopeSubdomain, // 旧别名
		"fqdn":      basecrawler.ScopeSameHost,
		"same-host": basecrawler.ScopeSameHost, // 旧别名
		"dn":        basecrawler.ScopeDN,
		"regex":     basecrawler.ScopeRegex,
		"all":       basecrawler.ScopeAny,
		"any":       basecrawler.ScopeAny, // 旧别名
		"":          basecrawler.ScopeSubdomain,
	}
	for name, want := range cases {
		args := []string{"-u", "https://x/"}
		if name != "" {
			args = append(args, "--scope", name)
		}
		if name == "regex" {
			args = append(args, "--scope-regex", `company\.com`)
		}
		cfg, err := parseArgs(args, &bytes.Buffer{})
		if err != nil {
			t.Errorf("scope %q 解析失败: %v", name, err)
			continue
		}
		if cfg.Opts.Scope != want {
			t.Errorf("scope %q = %v，期望 %v", name, cfg.Opts.Scope, want)
		}
	}
	// regex 缺正则应报错
	if _, err := parseArgs([]string{"-u", "https://x/", "--scope", "regex"}, &bytes.Buffer{}); err == nil {
		t.Error("-scope regex 缺 -scope-regex 应报错")
	}
	// 非法正则应报错
	if _, err := parseArgs([]string{"-u", "https://x/", "--scope", "regex", "--scope-regex", "("}, &bytes.Buffer{}); err == nil {
		t.Error("非法 -scope-regex 应报错")
	}
}

// TestParseArgsStatusFilter 验证 -s 白名单 / -S 黑名单的 CLI 映射。
func TestParseArgsStatusFilter(t *testing.T) {
	// 精确码 + 范围 + 0（线索）
	cfg, err := parseArgs([]string{"-u", "https://x/", "-s", "200,300-399,0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("合法 -s 解析失败: %v", err)
	}
	for _, c := range []struct {
		code int
		want bool
	}{{200, true}, {302, true}, {0, true}, {404, false}, {500, false}} {
		if got := cfg.StatusFilter.Allow(c.code); got != c.want {
			t.Errorf("-s 200,300-399,0: Allow(%d) = %v，期望 %v", c.code, got, c.want)
		}
	}
	// all = 全放行，叠加 -S 黑名单（排除优先）
	cfg, err = parseArgs([]string{"-u", "https://x/", "-s", "all", "-S", "404,500-599"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("-s all -S 解析失败: %v", err)
	}
	if !cfg.StatusFilter.Allow(200) || !cfg.StatusFilter.Allow(0) ||
		cfg.StatusFilter.Allow(404) || cfg.StatusFilter.Allow(503) {
		t.Error("-s all -S 404,500-599：应只排除 404 与 5xx")
	}
	// 非法输入
	for _, args := range [][]string{
		{"-u", "https://x/", "-s", "2x0"},
		{"-u", "https://x/", "-s", "99"},
		{"-u", "https://x/", "-s", "200-100"},
		{"-u", "https://x/", "-S", "all"},
	} {
		if _, err := parseArgs(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v 应报错", args[2:])
		}
	}
}

// TestParseArgsCrawlTimeout 验证 -ct 总时长解析（裸数字=秒、duration 语法、0/空=不限、非法报错）。
func TestParseArgsCrawlTimeout(t *testing.T) {
	cases := map[string]time.Duration{
		"90":    90 * time.Second,
		"90s":   90 * time.Second,
		"5m":    5 * time.Minute,
		"1h30m": 90 * time.Minute,
		"":      0,
		"0":     0,
	}
	for in, want := range cases {
		args := []string{"-u", "https://x/"}
		if in != "" {
			args = append(args, "--ct", in)
		}
		cfg, err := parseArgs(args, &bytes.Buffer{})
		if err != nil {
			t.Errorf("-ct %q 解析失败: %v", in, err)
			continue
		}
		if cfg.TotalTimeout != want {
			t.Errorf("-ct %q = %v，期望 %v", in, cfg.TotalTimeout, want)
		}
	}
	for _, bad := range []string{"-5", "5x", "m", "-1"} {
		if _, err := parseArgs([]string{"-u", "https://x/", "--ct", bad}, &bytes.Buffer{}); err == nil {
			t.Errorf("-ct %q 应报错", bad)
		}
	}
}

// TestParseArgsOutputType 验证 -O 输出类型解析与 -jsonl 旧别名兼容。
func TestParseArgsOutputType(t *testing.T) {
	cases := map[string]string{
		"json": "json",
		"txt":  "txt",
		"":     "json", // 默认
	}
	for in, want := range cases {
		args := []string{"-u", "https://x/"}
		if in != "" {
			args = append(args, "-O", in)
		}
		cfg, err := parseArgs(args, &bytes.Buffer{})
		if err != nil {
			t.Errorf("-O %q 解析失败: %v", in, err)
			continue
		}
		if cfg.OutputType != want {
			t.Errorf("-O %q = %q，期望 %q", in, cfg.OutputType, want)
		}
	}
	// 旧别名 -jsonl 强制 json
	cfg, err := parseArgs([]string{"-u", "https://x/", "--jsonl", "-O", "txt"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("-jsonl 解析失败: %v", err)
	}
	if cfg.OutputType != "json" {
		t.Errorf("-jsonl 应覆盖 -O txt 为 json，得到 %q", cfg.OutputType)
	}
	// 非法类型报错
	if _, err := parseArgs([]string{"-u", "https://x/", "-O", "csv"}, &bytes.Buffer{}); err == nil {
		t.Error("-O csv 应报错")
	}
}

// TestDefaultOutputPath 验证自动命名：特殊符号转下划线、时间戳后缀、扩展名透传。
func TestDefaultOutputPath(t *testing.T) {
	now := time.Date(2026, 11, 10, 21, 50, 0, 0, time.UTC)
	cases := []struct{ seed, ext, want string }{
		{"https://example.com/a/b", ".jsonl", "example.com_a_b_20261110-215000.jsonl"},
		{"https://example.com:8080/x", ".txt", "example.com_8080_x_20261110-215000.txt"},
		{"https://a.com/en-US/?q=1#f", ".jsonl", "a.com_en-US_20261110-215000.jsonl"},
	}
	for _, c := range cases {
		got := defaultOutputPath(c.seed, c.ext, now)
		if got != c.want {
			t.Errorf("defaultOutputPath(%q) = %q，期望 %q", c.seed, got, c.want)
		}
	}
	if got := defaultOutputPath("https://///", ".jsonl", now); !strings.HasSuffix(got, "_20261110-215000.jsonl") {
		t.Errorf("退化输入应仍产出合法文件名，得到 %q", got)
	}
}

// TestParseArgsDeobf 验证混淆探测链默认开启、--no-deobf 关闭整条链。
func TestParseArgsDeobf(t *testing.T) {
	cfg, err := parseArgs([]string{"-u", "https://x/"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.Opts.Deobfuscate || !cfg.Opts.DeobfForce {
		t.Error("签名检测与漏报兜底都应默认开启")
	}
	cfg, err = parseArgs([]string{"-u", "https://x/", "--no-deobf"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.Opts.Deobfuscate || cfg.Opts.DeobfForce {
		t.Error("--no-deobf 应同时关闭签名检测与漏报兜底")
	}
	if _, err := parseArgs([]string{"-u", "https://x/", "--deobf"}, &bytes.Buffer{}); err == nil {
		t.Error("旧正向参数 --deobf 应报 unknown flag")
	}
}
