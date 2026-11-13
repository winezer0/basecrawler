package goja

import (
	"strings"
	"testing"
)

// TestExtractBoundaries 锁死 goja 引擎的六类边界行为。
// 这是 Q-1 补强的核心：引擎层此前零单测，回归只能靠端到端探针间接发现。
func TestExtractBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool   // 是否应当报错（拒绝解析）
		wantURL string // 产出的首个 URL（空表示不关心/无产出）
	}{
		{
			name: "合法 fetch 调用",
			// URL 保留 query 原样（设计：完整调用地址；参数名从调用
			// 上下文推断，不从 query 串解析）
			src:     `fetch("/api/orders?limit=20").then(r=>r.json())`,
			wantURL: "/api/orders?limit=20",
		},
		{
			name:    "合法 axios 带方法",
			src:     `axios.post("/api/items", {name:"x"})`,
			wantURL: "/api/items",
		},
		{
			name:    "空产出（纯计算无请求）",
			src:     `var a=1;function f(x){return x*2}console.log(f(a))`,
			wantURL: "",
		},
		{
			name:    "语法错误应拒绝",
			src:     `function broken( { return`,
			wantErr: true,
		},
		{
			name:    "ESM import 应拒绝",
			src:     "import x from \"./mod.js\";\nfetch(\"/api/y\")",
			wantErr: true,
		},
		{
			name:    "空输入",
			src:     ``,
			wantURL: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs, err := Extract([]byte(tc.src))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应报错，实际产出 %d 条", len(reqs))
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if tc.wantURL == "" {
				if len(reqs) != 0 {
					t.Errorf("应零产出，实际 %d 条: %+v", len(reqs), reqs)
				}
				return
			}
			if len(reqs) == 0 {
				t.Fatal("应有产出，实际 0 条")
			}
			if reqs[0].URL != tc.wantURL {
				t.Errorf("首个 URL = %q, want %q", reqs[0].URL, tc.wantURL)
			}
		})
	}
}

// TestExtractInfersParams 锁定调用参数推断：query 参数名与 body 字段名
// 从调用上下文（{params:{...}} / {data:{...}}）的对象键推断。
func TestExtractInfersParams(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantURL    string
		wantQuery  []string
		wantBody   []string
		wantMethod string
	}{
		{
			name:    "axios params 对象推断 query",
			src:     `axios.get("/api/search", {params: {q: "go", page: 2}})`,
			wantURL: "/api/search", wantQuery: []string{"q", "page"},
			wantMethod: "GET",
		},
		{
			name:    "axios post data 对象推断 body",
			src:     `axios.post("/api/items", {name: "x", email: "a@b.c"})`,
			wantURL: "/api/items", wantBody: []string{"name", "email"},
			wantMethod: "POST",
		},
		{
			name:    "jQuery ajax data 字段推断 body",
			src:     `$.ajax({url: "/api/sub", type: "PUT", data: {token: "t", note: "n"}})`,
			wantURL: "/api/sub", wantBody: []string{"token", "note"},
			wantMethod: "PUT",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs, err := Extract([]byte(tc.src))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if len(reqs) == 0 {
				t.Fatal("应有产出")
			}
			r := reqs[0]
			if r.URL != tc.wantURL {
				t.Errorf("URL = %q, want %q", r.URL, tc.wantURL)
			}
			if tc.wantMethod != "" && r.Method != tc.wantMethod {
				t.Errorf("Method = %q, want %q", r.Method, tc.wantMethod)
			}
			if strings.Join(r.QueryParams, ",") != strings.Join(tc.wantQuery, ",") {
				t.Errorf("QueryParams = %v, want %v", r.QueryParams, tc.wantQuery)
			}
			if strings.Join(r.BodyParams, ",") != strings.Join(tc.wantBody, ",") {
				t.Errorf("BodyParams = %v, want %v", r.BodyParams, tc.wantBody)
			}
		})
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
