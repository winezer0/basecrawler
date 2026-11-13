package testfill

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/winezer0/basecrawler/extractor"
)

// Config 控制请求生成。零值即可用：填充全部占位符与参数，
// 只处理 200/未请求的结果，不加认证头。
type Config struct {
	// DefaultMethod 结果无 Method 时的默认方法（空 = GET）。
	DefaultMethod string
	// AuthHeader 认证头模板（如 "Authorization: Bearer ${TOKEN}"）。
	// 空表示不加；已有认证头的结果不覆盖。凭证只占位，不编造。
	AuthHeader string
	// IncludeNonOK 是否也处理非 200 的已请求结果（默认否：
	// 404 之类的失败地址生成测试请求没有意义）。
	IncludeNonOK bool
	// IncludePages 是否也处理 HTML 页面结果（默认否：页面不是接口，
	// 它里面的链接已被提取；curl 代码块结果不受此影响）。
	IncludePages bool
	// MaxRequests 生成条数上限，0 不限。
	MaxRequests int
	// SiteHosts 是采集目标（文档站）自身的 host 列表。命中且非 curl
	// 来源的请求默认跳过——它们是站点资产（favicon/manifest/静态资源），
	// 不是待测接口（O-1 实测：32 个 endpoint 混在 8 个站点资产里）。
	SiteHosts []string
	// Source 取值源，空用 NewSeededSource(1)。
	Source ValueSource
	// UseHiddenValues 是否把 hidden/select 等静态候选值原样填入请求。
	// 默认 false：这些值常是 csrf/session 等真实凭证，默认替换为占位测试值。
	// CLI 用 --testfill-use-hidden 显式打开。
	UseHiddenValues bool
}

// TestRequest 是一条可执行的测试请求。JSON 字段即 -testdata 的
// jsonl 输出契约。
type TestRequest struct {
	FromURL string            `json:"from_url"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
	Body    string            `json:"body,omitempty"`
	Notes   []string          `json:"notes,omitempty"`
	// Fields 是这条请求携带的字段名/参数名，供 safety 做动作关键字判定。
	Fields []string `json:"fields,omitempty"`
}

// SentURL 返回这条测试请求实际发出的 URL：把 Query 里填充的测试值合并进
// r.URL 的查询串。SendRequests 与「确认结果写回结果文件」共用它，保证
// 落盘的 URL 与真正请求的地址一致（含参数），不会出现两个不同口径。
func (r TestRequest) SentURL() string {
	u, err := url.Parse(r.URL)
	if err != nil {
		return r.URL
	}
	if len(r.Query) > 0 {
		q := u.Query()
		for k, v := range r.Query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// assetExts 是站点静态资产的扩展名集合（P-2）。
var assetExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {},
	".webp": {}, ".ico": {}, ".css": {}, ".js": {}, ".mjs": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".mp4": {}, ".webm": {}, ".mp3": {}, ".pdf": {},
}

// assetBaseNames 是站点级资产文件名（无扩展名信息也认）。
var assetBaseNames = map[string]struct{}{
	"favicon.ico": {}, "manifest.json": {}, "robots.txt": {},
	"sitemap.xml": {}, "llms.txt": {}, "browserconfig.xml": {},
}

// looksLikeAssetPath 判定路径是否是站点资产而非接口（P-2）。
// 纯函数。依据 O-1 实测缺口：混进测试请求的是 favicon/manifest/
// _next css/llms.txt；API 的 .json（如 /data.json）不是资产。
func looksLikeAssetPath(p string) bool {
	base := path.Base(strings.SplitN(p, "?", 2)[0])
	if _, ok := assetBaseNames[base]; ok {
		return true
	}
	ext := strings.ToLower(path.Ext(base))
	_, ok := assetExts[ext]
	return ok
}

// isClueResult 判定一条采集结果是不是「只报告不请求的情报线索」。
//
// 主判据完全是 Result 的语义字段，不再按 source 字符串前缀判断：
//   - 已抓取结果（StatusCode > 0）、curl 代码块、接口形状
//     （Confidence=interface / template）都要生成测试请求；
//   - Confidence=clue（CSP 线索、robots Disallow/Allow）与路由形状
//     （Kind=route）是线索，不生成测试请求。
//
// redirect 是唯一未打标记的历史线索产出点，保留单条兜底。其余新增来源
// 默认按接口形状放行（fail-open），不会因为漏登记而误杀新接口。
func isClueResult(r basecrawler.Result) bool {
	if r.StatusCode > 0 || r.Kind == "curl" {
		return false
	}
	if r.Confidence == "interface" || r.Confidence == "template" {
		return false
	}
	if r.Confidence == "clue" || r.Kind == "route" {
		return true
	}
	return r.Source == "redirect"
}

// hostIn 判定 URL 的 host 是否命中列表。
func hostIn(raw string, hosts []string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if u.Hostname() == h {
			return true
		}
	}
	return false
}

// urlPath 取 URL 的路径部分（失败返回空串）。
func urlPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}

// encodeForm 把填充结果编码为 form-urlencoded（按 key 排序保证确定性）。
// 纯函数。
func encodeForm(kv map[string]string) string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := url.Values{}
	for _, k := range keys {
		vals.Set(k, kv[k])
	}
	return vals.Encode()
}

func (c Config) source() ValueSource {
	if c.Source != nil {
		return c.Source
	}
	return NewSeededSource(1)
}

// GenerateRequests 把采集结果转成填充后的测试请求。纯函数。
//
// 两类输入：
//   - Kind=curl：原始命令已含 method/headers/body，按 key 填 body；
//   - 普通结果：填 URL 模板与查询参数（表单/JS 推断出的参数名）。
func GenerateRequests(results []basecrawler.Result, cfg Config) []TestRequest {
	if cfg.DefaultMethod == "" {
		cfg.DefaultMethod = "GET"
	}
	out := make([]TestRequest, 0, len(results))
	for _, r := range results {
		if cfg.MaxRequests > 0 && len(out) >= cfg.MaxRequests {
			break
		}
		if !cfg.IncludeNonOK && r.StatusCode != 0 && r.StatusCode != 200 {
			continue
		}
		// HTML 页面不是接口（它里面的链接已被提取成结果）；
		// curl 代码块结果没有 ContentType，不受此过滤影响。
		if !cfg.IncludePages && r.Kind != "curl" &&
			strings.Contains(strings.ToLower(r.ContentType), "text/html") {
			continue
		}
		// 站点自身资产不是接口（O-1 实测缺口）；同站的接口路径
		// （表单 action / SPA 的同站 API）必须保留——P-2 E2E 实测：
		// 单页应用的 API 多在种子域上，按 host 一刀切会把 /login
		// 这类真接口滤掉，只有资产路径才滤。
		if r.Kind != "curl" && hostIn(r.URL, cfg.SiteHosts) &&
			looksLikeAssetPath(urlPath(r.URL)) {
			continue
		}
		// 线索类结果不是待测接口（按语义判定，不再依赖来源字符串）
		if isClueResult(r) {
			continue
		}
		// 每请求独立源：同一条 result 的输出与它在批次中的位置无关。
		// 调用方显式提供的 Source（有状态）跨请求共享。
		src := cfg.Source
		if src == nil {
			src = NewSeededSource(1)
		}
		if r.Kind == "curl" && r.Raw != "" {
			if tr, ok := fromCurl(r, cfg, src); ok {
				out = append(out, tr)
			}
			continue
		}
		out = append(out, fromPlain(r, cfg, src))
	}
	return out
}

func fromCurl(r basecrawler.Result, cfg Config, src ValueSource) (TestRequest, bool) {
	cmd := extractor.ParseCurl(r.Raw)
	if cmd == nil {
		return TestRequest{}, false
	}
	tr := TestRequest{
		FromURL: r.URL,
		Method:  cmd.Method,
		URL:     cmd.URL,
		Headers: map[string]string{},
		Body:    cmd.Body,
	}
	for k, v := range cmd.Headers {
		tr.Headers[k] = v
	}
	tr.URL = FillTemplate(tr.URL, src)
	if cmd.Body != "" {
		filled, err := FillJSON(cmd.Body, src)
		if err == nil && filled != cmd.Body {
			tr.Body = filled
			tr.Notes = append(tr.Notes, "body 已按 key 填充测试值")
		}
	}
	// 原命令里的 $VAR（$OPENAI_API_KEY 等）执行前需要环境变量
	for _, v := range cmd.Headers {
		if strings.Contains(v, "$") {
			tr.Notes = append(tr.Notes, "头含 $VAR，执行前需设置环境变量")
			break
		}
	}
	applyAuth(&tr, cfg)
	return tr, true
}

func fromPlain(r basecrawler.Result, cfg Config, src ValueSource) TestRequest {
	method := r.Method
	if method == "" {
		method = cfg.DefaultMethod
	}
	tr := TestRequest{FromURL: r.URL, Method: method, URL: r.URL, Headers: map[string]string{}}
	tr.URL = FillTemplate(tr.URL, src)
	if len(r.Params) > 0 || len(r.ParamValues) > 0 {
		filled := FillParamsSafe(r.Params, r.ParamValues, src)
		if cfg.UseHiddenValues {
			filled = FillParams(r.Params, r.ParamValues, src)
			tr.Notes = append(tr.Notes, "hidden 静态值按原值填入（--testfill-use-hidden）")
		}
		for k := range filled {
			tr.Fields = append(tr.Fields, k)
		}
		sort.Strings(tr.Fields)
		// POST/PUT/PATCH 的表单参数进 body（form-urlencoded）——拼到
		// query 上会改变语义（HTML 表单的 method 决定提交位置）。
		if method == "POST" || method == "PUT" || method == "PATCH" {
			tr.Body = encodeForm(filled)
			tr.Headers["Content-Type"] = "application/x-www-form-urlencoded"
			tr.Notes = append(tr.Notes, "表单参数已按 input type 填充进 body")
		} else {
			tr.Query = filled
			tr.Notes = append(tr.Notes, "查询参数按参数名推断填充")
		}
	}
	applyAuth(&tr, cfg)
	return tr
}

// applyAuth 加认证头占位。原命令里的 $VAR 形式视为占位，可被
// AuthHeader 覆盖；不含 $ 的视为真实凭证，不覆盖。
func applyAuth(tr *TestRequest, cfg Config) {
	if cfg.AuthHeader == "" {
		return
	}
	for k, v := range tr.Headers {
		if strings.EqualFold(k, "Authorization") && !strings.Contains(v, "$") {
			return // 真实凭证，不覆盖
		}
	}
	k, v, found := strings.Cut(cfg.AuthHeader, ":")
	if !found {
		return
	}
	tr.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
}

// Render 按格式渲染请求集合。纯函数。
func Render(reqs []TestRequest, format string) (string, error) {
	switch format {
	case "curl":
		return RenderCurl(reqs), nil
	case "python":
		return RenderPython(reqs), nil
	case "postman":
		return RenderPostman(reqs), nil
	case "http":
		return RenderHTTP(reqs), nil
	case "json":
		return RenderJSON(reqs)
	}
	return "", fmt.Errorf("testfill: 未知格式 %q（curl/python/postman/http/json）", format)
}

// RenderJSON 渲染为 JSONL（每行一条 TestRequest，含来源与填充说明）。
func RenderJSON(reqs []TestRequest) (string, error) {
	return encodeJSONL(reqs)
}

// encodeJSONL 把任意可 JSON 化切片编码为 JSONL。
func encodeJSONL(v any) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderCurl 渲染为 curl 脚本。单引号包裹，body 内含单引号时转义。
func RenderCurl(reqs []TestRequest) string {
	return renderCurlLines(reqs, nil)
}

// renderCurlLines 是 RenderCurl 的带注释变体：ann 为
// 「Method URL → 注释」映射，渲染时追加在行尾。
func renderCurlLines(reqs []TestRequest, ann map[string]string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# basecrawler 测试请求（凭证为占位符，执行前替换）\n\n")
	for i, r := range reqs {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "# %s\n", n)
		}
		u := r.URL
		if len(r.Query) > 0 {
			q := url.Values{}
			for k, v := range r.Query {
				q.Set(k, v)
			}
			u += "?" + q.Encode()
		}
		fmt.Fprintf(&b, "curl -X %s '%s'", r.Method, u)
		for _, k := range sortedKeys(r.Headers) {
			fmt.Fprintf(&b, " -H '%s: %s'", k, shellSingleQuote(r.Headers[k]))
		}
		if r.Body != "" {
			fmt.Fprintf(&b, " -d '%s'", shellSingleQuote(r.Body))
		}
		if note, ok := ann[annotateKey(r)]; ok {
			fmt.Fprintf(&b, "  # %s", note)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// RenderPython 渲染为 requests 脚本。
func RenderPython(reqs []TestRequest) string {
	return renderPythonLines(reqs, nil)
}

// renderPythonLines 是 RenderPython 的带注释变体。
func renderPythonLines(reqs []TestRequest, ann map[string]string) string {
	var b strings.Builder
	b.WriteString("import requests\n\n")
	for i, r := range reqs {
		if i > 0 {
			b.WriteString("\n")
		}
		u := r.URL
		if len(r.Query) > 0 {
			q := url.Values{}
			for k, v := range r.Query {
				q.Set(k, v)
			}
			u += "?" + q.Encode()
		}
		fmt.Fprintf(&b, "resp = requests.%s(\n    %q,\n", strings.ToLower(r.Method), u)
		if len(r.Headers) > 0 {
			b.WriteString("    headers={\n")
			for _, k := range sortedKeys(r.Headers) {
				fmt.Fprintf(&b, "        %q: %q,\n", k, r.Headers[k])
			}
			b.WriteString("    },\n")
		}
		if len(r.Query) > 0 {
			b.WriteString("    params={\n")
			for _, k := range sortedKeys(r.Query) {
				fmt.Fprintf(&b, "        %q: %q,\n", k, r.Query[k])
			}
			b.WriteString("    },\n")
		}
		if r.Body != "" {
			fmt.Fprintf(&b, "    data=%s,\n", r.Body)
		}
		b.WriteString(")\n")
		if note, ok := ann[annotateKey(r)]; ok {
			fmt.Fprintf(&b, "# %s\n", note)
		} else {
			b.WriteString("print(resp.status_code, resp.url)\n")
		}
	}
	return b.String()
}

// RenderPostman 渲染为 Postman Collection v2.1 JSON。
func RenderPostman(reqs []TestRequest) string {
	type pmURL struct {
		Raw  string   `json:"raw"`
		Host []string `json:"host"`
		Path []string `json:"path"`
	}
	type pmBody struct {
		Mode string `json:"mode"`
		Raw  string `json:"raw"`
	}
	type pmRequest struct {
		Method string              `json:"method"`
		Header []map[string]string `json:"header,omitempty"`
		Body   *pmBody             `json:"body,omitempty"`
		URL    pmURL               `json:"url"`
	}
	type pmItem struct {
		Name    string    `json:"name"`
		Request pmRequest `json:"request"`
	}
	items := make([]pmItem, 0, len(reqs))
	for _, r := range reqs {
		u, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		req := pmRequest{Method: r.Method, URL: pmURL{Raw: r.URL, Host: []string{u.Host}, Path: splitPath(u.Path)}}
		for _, k := range sortedKeys(r.Headers) {
			req.Header = append(req.Header, map[string]string{"key": k, "value": r.Headers[k]})
		}
		if r.Body != "" {
			req.Body = &pmBody{Mode: "raw", Raw: r.Body}
		}
		items = append(items, pmItem{Name: r.FromURL, Request: req})
	}
	collection := map[string]any{
		"info": map[string]any{
			"_postman_id": "basecrawler-testfill-0001",
			"name":        "basecrawler test requests",
			"schema":      "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		},
		"item": items,
	}
	out, _ := json.MarshalIndent(collection, "", "  ")
	return string(out)
}

// RenderHTTP 渲染为 VS Code REST Client 的 .http 格式。
func RenderHTTP(reqs []TestRequest) string {
	return renderHTTPLines(reqs, nil)
}

// renderHTTPLines 是 RenderHTTP 的带注释变体。
func renderHTTPLines(reqs []TestRequest, ann map[string]string) string {
	var b strings.Builder
	for _, r := range reqs {
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "# %s\n", n)
		}
		u := r.URL
		if len(r.Query) > 0 {
			q := url.Values{}
			for k, v := range r.Query {
				q.Set(k, v)
			}
			u += "?" + q.Encode()
		}
		fmt.Fprintf(&b, "%s %s\n", r.Method, u)
		for _, k := range sortedKeys(r.Headers) {
			fmt.Fprintf(&b, "%s: %s\n", k, r.Headers[k])
		}
		if r.Body != "" {
			fmt.Fprintf(&b, "\n%s\n", r.Body)
		}
		if note, ok := ann[annotateKey(r)]; ok {
			fmt.Fprintf(&b, "# %s\n", note)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func splitPath(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	return strings.Split(strings.Trim(p, "/"), "/")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// 小集合，插入排序足够；map 遍历顺序不稳定，排序保证输出确定
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// shellSingleQuote 转义单引号（shell 惯例 '\”）。
func shellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
