package openapi

import (
	"strings"
	"testing"
)

// OpenAPI 提取（Q-4）：spec 是契约级信息，只在 opt-in 探测命中后使用。
func TestLooksLikeOpenAPI(t *testing.T) {
	cases := map[string]bool{
		`{"openapi":"3.0.0","paths":{"/a":{"get":{}}}}`: true,
		`{"swagger":"2.0","paths":{"/a":{"get":{}}}}`:   true,
		`{"openapi":"3.0.0","paths":{}}`:                false, // 空 paths 无接口信息
		`{"paths":{"/a":{}}}`:                           false, // 无版本键
		`{"name":"not a spec"}`:                         false,
		`not json`:                                      false,
		`[]`:                                            false,
	}
	for body, want := range cases {
		if got := LooksLikeOpenAPI([]byte(body)); got != want {
			t.Errorf("LooksLikeOpenAPI(%.40s) = %v, want %v", body, got, want)
		}
	}
}

// TestFromOpenAPI 锁定三条契约：方法大写、参数名收集、路径稳定排序。
func TestFromOpenAPI(t *testing.T) {
	body := []byte(`{
	  "openapi": "3.0.0",
	  "paths": {
	    "/orders/{id}": {
	      "get": {"parameters": [{"name":"id","in":"path"},{"name":"fields","in":"query"}]},
	      "delete": {},
	      "parameters": [{"name":"ignored","in":"header"}]
	    },
	    "/orders": {
	      "post": {"parameters": [{"name":"amount","in":"query"},{"name":"amount","in":"query"}]},
	      "get": {}
	    }
	  }
	}`)

	reqs := FromOpenAPI(body)
	if len(reqs) != 4 {
		t.Fatalf("期望 4 个操作（/orders GET/POST + /orders/{id} GET/DELETE），实际 %d: %+v", len(reqs), reqs)
	}
	// 路径字典序：/orders 先于 /orders/{id}
	if reqs[0].Raw != "/orders" || reqs[0].Method != "GET" {
		t.Errorf("首条应为 /orders GET，实际 %s %s", reqs[0].Method, reqs[0].Raw)
	}
	if reqs[1].Raw != "/orders" || reqs[1].Method != "POST" {
		t.Errorf("次条应为 /orders POST，实际 %s %s", reqs[1].Method, reqs[1].Raw)
	}
	if !strings.Contains(strings.Join(reqs[1].Params, ","), "amount") {
		t.Errorf("POST /orders 应带 amount 参数: %v", reqs[1].Params)
	}
	// 重复参数名去重
	if n := strings.Count(strings.Join(reqs[1].Params, ","), "amount"); n != 1 {
		t.Errorf("参数名应去重，amount 出现 %d 次: %v", n, reqs[1].Params)
	}
	// 方法也按字典序：DELETE 先于 GET
	// {id} 转成 :id（spec 模板 → 项目统一的 :param 约定）
	if reqs[2].Raw != "/orders/:id" || reqs[2].Method != "DELETE" {
		t.Errorf("第三条应为 /orders/:id DELETE，实际 %s %s", reqs[2].Method, reqs[2].Raw)
	}
	if reqs[3].Raw != "/orders/:id" || reqs[3].Method != "GET" {
		t.Errorf("第四条应为 /orders/:id GET，实际 %s %s", reqs[3].Method, reqs[3].Raw)
	}
	// path item 级 parameters 不是操作，不该单独产出或混入
	for _, r := range reqs {
		for _, p := range r.Params {
			if p == "ignored" {
				t.Errorf("path item 级 parameters 不应混入操作参数: %+v", r)
			}
		}
	}
	for _, r := range reqs {
		if r.Source != "openapi" || r.Tag != "openapi" {
			t.Errorf("来源标记错误: %+v", r)
		}
		// 全部是接口形状：emit-only，绝不入队抓取
		if r.Confidence != "template" {
			t.Errorf("Confidence 应为 template（emit-only）: %+v", r)
		}
	}
}

// TestFromOpenAPIPrefix basePath / servers 只在路径不是绝对时补前缀。
func TestFromOpenAPIPrefix(t *testing.T) {
	swagger := []byte(`{"swagger":"2.0","basePath":"/v2","paths":{"pets":{"get":{}}}}`)
	reqs := FromOpenAPI(swagger)
	if len(reqs) != 1 || reqs[0].Raw != "/v2/pets" {
		t.Fatalf("basePath 应补前缀: %+v", reqs)
	}

	// 已是绝对路径时不重复拼接
	abs := []byte(`{"swagger":"2.0","basePath":"/v2","paths":{"/pets":{"get":{}}}}`)
	reqs = FromOpenAPI(abs)
	if len(reqs) != 1 || reqs[0].Raw != "/pets" {
		t.Fatalf("绝对路径不应重复拼接 basePath: %+v", reqs)
	}

	// OpenAPI 3 servers
	v3 := []byte(`{"openapi":"3.0.0","servers":[{"url":"https://api.x.com/v1"}],"paths":{"items":{"get":{}}}}`)
	reqs = FromOpenAPI(v3)
	if len(reqs) != 1 || reqs[0].Raw != "/v1/items" {
		t.Fatalf("servers 路径应补前缀: %+v", reqs)
	}
}

// TestOpenAPIProbePathsCoverSwagger2 锁住 P-1 口径修正：候选表必须
// 含 Swagger 2.0 生态标准路径，否则真暴露会被误判为不暴露。
func TestOpenAPIProbePathsCoverSwagger2(t *testing.T) {
	for _, p := range []string{
		"/v2/swagger.json", "/swagger/v1/swagger.json", "/v3/api-docs",
	} {
		if !IsOpenAPIPath(p) {
			t.Errorf("候选表缺 %s", p)
		}
	}
	if IsOpenAPIPath("/not/a/spec") {
		t.Error("非候选路径不应命中")
	}
}

// TestFromOpenAPILimit 巨型 spec 的产出上限。
func TestFromOpenAPILimit(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"openapi":"3.0.0","paths":{`)
	for i := 0; i < maxOpenAPIPaths+50; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"/p`)
		b.WriteString(itoa(i))
		b.WriteString(`":{"get":{}}`)
	}
	b.WriteString("}}")

	reqs := FromOpenAPI([]byte(b.String()))
	if len(reqs) > maxOpenAPIPaths {
		t.Fatalf("产出应受 maxOpenAPIPaths 限制: %d", len(reqs))
	}
	if len(reqs) == 0 {
		t.Fatal("应有产出")
	}
}

// itoa 避免为测试引入 strconv（与本包风格一致，纯手写）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestPathTemplate 锁定 {param} → :param：不转会 URL 编码成 %7B 破坏模板。
func TestPathTemplate(t *testing.T) {
	cases := map[string]string{
		"/pets/{petId}":                  "/pets/:petId",
		"/pets/{petId}/uploadImage":      "/pets/:petId/uploadImage",
		"/users/{username}/repos/{repo}": "/users/:username/repos/:repo",
		"/plain/path":                    "/plain/path",
		"/no/closing/{broken":            "/no/closing/:broken",
	}
	for in, want := range cases {
		if got := pathTemplate(in); got != want {
			t.Errorf("pathTemplate(%q) = %q, want %q", in, got, want)
		}
	}
}
