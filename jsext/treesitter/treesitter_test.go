package treesitter

import (
	"strings"
	"testing"
)

// TestExtractBoundaries 锁死 treesitter 引擎的边界行为。
//
// 与 goja 的差异是**设计**而非缺陷：treesitter 是宽容的词法层
// （语法错误容错恢复、ESM 不拒绝），goja 是严格的 AST 层（报错
// 拒绝）。auto 链上 goja 先跑、失败才到 treesitter——本测试锁定
// 的是链尾引擎的语义，改动它会改变整条链的兜底行为。
func TestExtractBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
		wantN   int // -1 表示不关心
	}{
		{
			name:  "合法 fetch 调用",
			src:   `fetch("/api/orders?limit=20")`,
			wantN: 1,
		},
		{
			name:  "空产出（纯计算无请求）",
			src:   `var a=1;function f(x){return x*2}console.log(f(a))`,
			wantN: 0,
		},
		{
			name: "语法错误容错不报错",
			src:  `function broken( { return`,
			// 与 goja 的关键差异：err=nil、零产出（错误恢复）
			wantErr: false,
			wantN:   0,
		},
		{
			name: "ESM 不拒绝",
			src:  "import x from \"./mod.js\";\nfetch(\"/api/y\")",
			// 与 goja 的关键差异：import 语句本身也作为 URL 产出
			wantErr: false,
			wantN:   2,
		},
		{
			name:  "空输入",
			src:   ``,
			wantN: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs, err := Extract([]byte(tc.src))
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantN >= 0 && len(reqs) != tc.wantN {
				t.Errorf("产出 %d 条, want %d: %+v", len(reqs), tc.wantN, reqs)
			}
		})
	}
}

// TestExtractInfersParams 锁定参数推断与 goja 对齐。
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
