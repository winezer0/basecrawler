package openapi

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/winezer0/basecrawler/extractor/base"
)

// OpenAPI / Swagger spec 提取（Q-4）。
//
// 价值边界：spec 是**契约级**信息——它给出接口的真实路径、方法、参数名，
// 比从 HTML/JS 里猜路径精确一个量级。但实测暴露率低（P-1：主流站 0%，
// API 专属子集 12.5%），所以只作 opt-in 能力：--probe-openapi 打开后
// 才探测候选路径，默认不浪费任何配额。
//
// 输出是接口形状（Method + Params），与表单 action 同一语义：交给
// testfill 填充发送，而不是入队抓取（GET 之外的方法不该被自动请求）。

// maxOpenAPIPaths 限制单个 spec 的产出条数，防止巨型 spec 灌满队列。
const maxOpenAPIPaths = 2000

// openAPIDoc 只声明提取需要的字段：版本、servers/basePath、paths。
type openAPIDoc struct {
	OpenAPI  string                     `json:"openapi"`
	Swagger  string                     `json:"swagger"`
	BasePath string                     `json:"basePath"` // Swagger 2.0
	Servers  []struct{ URL string }     `json:"servers"`
	Paths    map[string]json.RawMessage `json:"paths"`
}

// LooksLikeOpenAPI 判定一段 JSON 响应是否是 OpenAPI/Swagger spec。
// 纯函数。判据与 tools/openapi.py 的 looks_like_spec 一致：
// 有 openapi/swagger 版本键，且 paths 是非空对象。
func LooksLikeOpenAPI(body []byte) bool {
	var doc openAPIDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return false
	}
	if doc.OpenAPI == "" && doc.Swagger == "" {
		return false
	}
	return len(doc.Paths) > 0
}

// openAPIOperation 是 path item 里一个 HTTP 方法条目。
type openAPIOperation struct {
	Parameters []struct {
		Name string `json:"name"`
		In   string `json:"in"`
	} `json:"parameters"`
}

// httpMethods 是 spec 里合法的操作键（其余如 parameters/summary 是元信息）。
var httpMethods = map[string]struct{}{
	"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {},
	"head": {}, "options": {},
}

// FromOpenAPI 从 spec 正文提取接口形状。
//
//   - 每个 path + 方法的组合产出一条 base.Request，Method 为大写；
//   - 参数名收进 Params（query/path/header 都收：它们都是调用时需要
//     提供的东西，交给 testfill 按名字或 type 填充）；
//   - basePath / servers 只在路径不是以 / 开头时作为前缀补上——路径
//     本身通常已是完整相对路径，重复拼接会造出不存在的地址。
func FromOpenAPI(body []byte) []base.Request {
	var doc openAPIDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	if len(doc.Paths) == 0 {
		return nil
	}
	prefix := openAPIPrefix(&doc)

	out := make([]base.Request, 0, len(doc.Paths))
	// 按路径字典序输出，保证同一 spec 的结果稳定（map 遍历无序）。
	paths := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		if len(out) >= maxOpenAPIPaths {
			break
		}
		full := p
		if !strings.HasPrefix(full, "/") && prefix != "" {
			full = prefix + "/" + strings.TrimPrefix(full, "/")
		}
		var item map[string]json.RawMessage
		if err := json.Unmarshal(doc.Paths[p], &item); err != nil {
			continue
		}
		methods := make([]string, 0, len(item))
		for k := range item {
			if _, ok := httpMethods[strings.ToLower(k)]; ok {
				methods = append(methods, k)
			}
		}
		sort.Strings(methods)
		for _, m := range methods {
			if len(out) >= maxOpenAPIPaths {
				break
			}
			var op openAPIOperation
			_ = json.Unmarshal(item[m], &op)
			out = append(out, base.Request{
				Raw:        pathTemplate(full),
				Method:     strings.ToUpper(m),
				Source:     "openapi",
				Attribute:  "paths",
				Tag:        "openapi",
				Confidence: "template", // 接口形状，不直接抓取
				Params:     paramNames(op),
			})
		}
	}
	return out
}

// openAPIPrefix 取 spec 声明的路径前缀（Swagger 的 basePath 优先，
// 其次是 OpenAPI 3 的第一个 servers[].url 的路径部分）。纯函数。
func openAPIPrefix(doc *openAPIDoc) string {
	if bp := strings.TrimSpace(doc.BasePath); bp != "" && bp != "/" {
		return "/" + strings.Trim(bp, "/")
	}
	for _, s := range doc.Servers {
		u := strings.TrimSpace(s.URL)
		if u == "" {
			continue
		}
		// 只取路径部分：`https://api.x.com/v1` -> `/v1`
		if i := strings.Index(u, "://"); i >= 0 {
			u = u[i+3:]
			if j := strings.Index(u, "/"); j >= 0 {
				u = u[j:]
			} else {
				u = ""
			}
		}
		u = strings.TrimRight(u, "/")
		if u != "" && u != "/" {
			return u
		}
	}
	return ""
}

// pathTemplate 把 spec 路径模板的 {param} 转成 :param。纯函数。
//
// 必须转：原样保留 `{petId}` 会让 url.Parse + String() 把它转义成
// `%7BpetId%7D`（实测 petstore），模板语义丢失且与 routegraph 的
// `:param` 约定不一致——下游 testfill 与 route 展开都按 `:param` 识别。
func pathTemplate(p string) string {
	if !strings.ContainsAny(p, "{}") {
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '{':
			b.WriteByte(':')
			// 跳过 { 与其内容直到 }
			if j := strings.IndexByte(p[i+1:], '}'); j >= 0 {
				b.WriteString(p[i+1 : i+1+j])
				i += j + 1
			}
		case '}':
			// 孤立的 } 直接丢弃
		default:
			b.WriteByte(p[i])
		}
	}
	return b.String()
}

// paramNames 收集操作的参数名（去重、保序）。纯函数。
func paramNames(op openAPIOperation) []string {
	if len(op.Parameters) == 0 {
		return nil
	}
	out := make([]string, 0, len(op.Parameters))
	seen := make(map[string]struct{}, len(op.Parameters))
	for _, p := range op.Parameters {
		n := strings.TrimSpace(p.Name)
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// OpenAPIProbePaths 是 opt-in 探测的候选路径表（Q-4）。
//
// 与 tools/openapi.py 的 SPEC_CANDIDATES 同源，含 P-1 口径修正补入的
// Swagger 2.0 生态路径（/v2/swagger.json 等）——petstore 实测证明漏掉
// 它会把「暴露」误判成「不暴露」。
var OpenAPIProbePaths = []string{
	"/openapi.json",
	"/swagger.json",
	"/v2/swagger.json",
	"/api/v2/swagger.json",
	"/rest/v2/swagger.json",
	"/swagger/v1/swagger.json",
	"/swagger/v2/swagger.json",
	"/api-docs",
	"/api-docs/swagger.json",
	"/v3/api-docs",
	"/v2/api-docs",
	"/docs/openapi.json",
	"/api/openapi.json",
	"/spec/openapi.json",
}

// IsOpenAPIPath 判定路径是否是 spec 候选路径（探测前先去重）。纯函数。
func IsOpenAPIPath(p string) bool {
	p = strings.TrimSpace(p)
	for _, c := range OpenAPIProbePaths {
		if p == c {
			return true
		}
	}
	return false
}
