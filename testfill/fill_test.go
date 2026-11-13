package testfill

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/winezer0/basecrawler/extractor"
)

// 表驱动：参数名 → 取值形态。精确表优先于包含表。
func TestValueKind(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"id", "int"},
		{"user_id", "int"},
		{"userId", "int"},
		{"uuid", "uuid"},
		{"guid", "uuid"},
		{"email", "email"},
		{"user_email", "email"},
		{"password", "password"},
		{"pwd", "password"},
		{"token", "token"},
		{"api_key", "token"},
		{"secret", "token"},
		{"username", "name"},
		{"page", "int"},
		{"limit", "int"},
		{"per_page", "int"},
		{"offset", "int"},
		{"skip", "int"},
		{"phone", "phone"},
		{"mobile", "phone"},
		{"url", "url"},
		{"callback", "url"},
		{"redirect_uri", "url"},
		{"q", "word"},
		{"query", "word"},
		{"keyword", "word"},
		{"date", "date"},
		{"model", "word"},
		{"content", "word"},
		{"messages", "word"},
		{"", "word"},
		{"unknown_field", "word"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := valueKind(tc.name); got != tc.want {
				t.Errorf("valueKind(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// 归一化：大小写、连字符、下划线、空格不敏感。
func TestValueKindNormalize(t *testing.T) {
	for _, n := range []string{"User-Id", "user_id", "USERID", "user id"} {
		if got := valueKind(n); got != "int" {
			t.Errorf("valueKind(%q) = %q, want int", n, got)
		}
	}
}

func TestFillTemplate(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"路径参数冒号", "/api/users/:id/posts/:post_id", "/api/users/1/posts/2"},
		{"花括号占位", "/api/v1/orders/{orderId}", "/api/v1/orders/1"},
		{"模板字符串占位", "/api/users/${userId}/items", "/api/users/1/items"},
		{"无占位符原样", "/api/v1/models", "/api/v1/models"},
		{"带正则约束的参数", "/:lang(.*)/x", "/sample-1/x"},
		{"绝对 URL", "https://a.com/api/x/:id", "https://a.com/api/x/1"},
		// 同 kind 独立计数：两个 int 参数取不同值，便于测试
		{"混合占位", "/u/{id}/p/:page", "/u/2/p/1"},
		{"未知花括号字段", "/x/{whatever}", "/x/sample-1"},
		{"已知与未知混合", "/u/{id}/x/{zzz}", "/u/1/x/sample-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 每例独立源：填充结果不依赖调用顺序
			if got := FillTemplate(tc.raw, NewSeededSource(1)); got != tc.want {
				t.Errorf("FillTemplate(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// 确定性：同 seed 同输出（测试可复现，不引 math/rand 全局态）。
func TestSeededSourceDeterministic(t *testing.T) {
	a := NewSeededSource(42)
	b := NewSeededSource(42)
	for i := 0; i < 5; i++ {
		if a.Next("uuid") != b.Next("uuid") {
			t.Fatal("同 seed 应产生同序列")
		}
	}
}

// 确定性源的取值必须「看起来像那么回事」——填充值要能被对端接受。
func TestSeededSourceShapes(t *testing.T) {
	src := NewSeededSource(1)
	checks := []struct {
		kind    string
		pattern string
	}{
		{"int", `^\d+$`},
		{"uuid", `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`},
		{"email", `^[^@]+@[^@]+\.[^@]+$`},
		{"url", `^https?://`},
		{"phone", `^\+?\d[\d-]{6,}$`},
		{"date", `^\d{4}-\d{2}-\d{2}$`},
		{"token", `^[0-9a-f]{32}$`},
		{"password", `^.{8,}$`},
	}
	for _, c := range checks {
		got := src.Next(c.kind)
		if !reMatch(c.pattern, got) {
			t.Errorf("%s 形态不符: %q 不匹配 %s", c.kind, got, c.pattern)
		}
	}
}

func reMatch(pattern, s string) bool {
	return regexp.MustCompile(pattern).MatchString(s)
}

func TestFillParams(t *testing.T) {
	src := NewSeededSource(1)
	// 有静态候选的（select/radio）取第一个；其余按名字推断
	values := []extractor.ParamValue{
		{Name: "status", Values: []string{"active", "closed"}, Kind: "select"},
		{Name: "csrf", Values: []string{"tok-abc"}, Kind: "hidden"},
	}
	names := []string{"status", "csrf", "page", "email", "q"}
	got := FillParams(names, values, src)
	if got["status"] != "active" {
		t.Errorf("select 应取第一个候选: %q", got["status"])
	}
	if got["csrf"] != "tok-abc" {
		t.Errorf("hidden 应取原值: %q", got["csrf"])
	}
	if got["page"] != "1" {
		t.Errorf("page 应填 int: %q", got["page"])
	}
	if got["email"] != "user1@example.com" {
		t.Errorf("email 形态: %q", got["email"])
	}
	if got["q"] != "sample-1" {
		t.Errorf("q 应填 word: %q", got["q"])
	}
}

func TestFillJSON(t *testing.T) {
	src := NewSeededSource(1)
	body := `{"model": "gpt-4o-mini", "temperature": 0.7, "stream": false, "user": {"id": 1, "email": "a@b.c"}, "tags": ["x", "y"]}`
	got, err := FillJSON(body, src)
	if err != nil {
		t.Fatalf("FillJSON: %v", err)
	}
	// 结构不变：数字/布尔/数组原样，字符串值按 key 形态填充
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("结果不是合法 JSON: %v (%s)", err, got)
	}
	if m["model"] == "gpt-4o-mini" || m["model"] == "" {
		t.Errorf("model 应被填充: %v", m["model"])
	}
	if m["temperature"] != 0.7 {
		t.Errorf("数字不应改动: %v", m["temperature"])
	}
	if m["stream"] != false {
		t.Errorf("布尔不应改动: %v", m["stream"])
	}
	user := m["user"].(map[string]any)
	if user["email"] == "a@b.c" {
		t.Errorf("嵌套 email 应被填充: %v", user["email"])
	}
	if user["id"] != float64(1) {
		t.Errorf("嵌套数字不应改动: %v", user["id"])
	}
	tags := m["tags"].([]any)
	if tags[0] != "x" {
		t.Errorf("数组元素不应改动: %v", tags)
	}
}

func TestFillJSONInvalid(t *testing.T) {
	src := NewSeededSource(1)
	// 非 JSON body 原样返回、不报错（表单编码等场景）
	got, err := FillJSON("k=v&x=1", src)
	if err != nil || got != "k=v&x=1" {
		t.Errorf("非 JSON 应原样: got=%q err=%v", got, err)
	}
}

func TestFillJSONEmpty(t *testing.T) {
	if got, err := FillJSON("", NewSeededSource(1)); err != nil || got != "" {
		t.Errorf("空 body: got=%q err=%v", got, err)
	}
}

// O-1 真实数据（GitHub REST 文档）：全大写占位符段必须填充。
func TestFillTemplateUppercasePlaceholders(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"/users/USERNAME/repos", "/users/user1/repos"},
		{"/repos/OWNER/REPO", "/repos/user1/sample-1"},
		{"/orgs/ORG/repos", "/orgs/user1/repos"},
		{"/repos/TEMPLATE_OWNER/TEMPLATE_REPO/generate", "/repos/user1/sample-1/generate"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := FillTemplate(tc.raw, NewSeededSource(1)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// 普通全大写词（URL 路径里的缩写目录）不应被误填。
func TestFillTemplateUppercaseNotOverreach(t *testing.T) {
	for _, raw := range []string{"/API/users", "/V1/orders", "/HTTP/methods"} {
		if got := FillTemplate(raw, NewSeededSource(1)); got != raw {
			t.Errorf("%q 不应被填充, got %q", raw, got)
		}
	}
}

// homepage 等 body key 归 url 形态（O-1 数据）。
func TestValueKindHomepage(t *testing.T) {
	if got := valueKind("homepage"); got != "url" {
		t.Errorf("homepage = %q, want url", got)
	}
}

// P-2：文本类控件按 input type 选值形态，type 优先于名字。
func TestValueKindFromInputType(t *testing.T) {
	cases := map[string]string{
		"email":          "email",
		"password":       "password",
		"number":         "int",
		"range":          "int",
		"tel":            "phone",
		"url":            "url",
		"date":           "date",
		"datetime-local": "date",
		"time":           "date",
		"search":         "", // 回退名字推断
		"text":           "", // 回退名字推断
		"":               "", // 非 input 来源
		"submit":         "", // 不该出现，防御
	}
	for typ, want := range cases {
		if got := valueKindFromInputType(typ); got != want {
			t.Errorf("valueKindFromInputType(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestFillParamsInputType(t *testing.T) {
	src := NewSeededSource(1)
	values := []extractor.ParamValue{
		{Name: "email", Kind: "input:email", Type: "email"},    // type 决定形态
		{Name: "whatever", Kind: "input:email", Type: "email"}, // 名字无信息也按 type
		{Name: "age", Kind: "input:number", Type: "number"},
		{Name: "homepage", Kind: "input:url", Type: "url"}, // type=url 压过名字规则
		{Name: "q", Kind: "input:search"},                  // search 回退名字推断
		{Name: "csrf", Kind: "hidden", Values: []string{"tok-abc"}},
	}
	got := FillParams(nil, values, src)
	// 每 kind 独立计数：同源两个 input:email 取不同值（更有区分度）
	if got["whatever"] != "user2@example.com" {
		t.Errorf("input:email 应按 type 填邮箱: %q", got["whatever"])
	}
	if got["age"] != "1" {
		t.Errorf("input:number 应填 int: %q", got["age"])
	}
	// homepage 名字本身也是 url 形态；同 kind 第二个取值
	if got["homepage"] != "https://example.com/callback" {
		t.Errorf("input:url 应按 type 填 URL: %q", got["homepage"])
	}
	if got["q"] != "sample-1" {
		t.Errorf("input:search 应回退名字推断: %q", got["q"])
	}
	if got["csrf"] != "tok-abc" {
		t.Errorf("静态候选仍优先: %q", got["csrf"])
	}
}
