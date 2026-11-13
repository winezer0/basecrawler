package frontier

import (
	"net/url"
	"regexp"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return u
}

func TestNormalize(t *testing.T) {
	base := mustURL(t, "https://Example.com:443/a/b/index.html")

	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/x/y", "https://example.com/x/y", true},
		{"../c", "https://example.com/a/c", true},
		{"./d", "https://example.com/a/b/d", true},
		{"https://Other.com/z#frag", "https://other.com/z", true},
		{"http://a.com:80/p", "http://a.com/p", true},
		{"#anchor", "", false},
		{"javascript:void(0)", "", false},
		{"mailto:a@b.com", "", false},
		{"data:image/png;base64,AAA", "", false},
		{"tel:123456", "", false},
		{"", "", false},
		// 相对引用先按 RFC 3986 与 base 合并，再消 dot-segment：
		// /a/b/ + a//b/../c => /a/b/a/b/../c => /a/b/a/c
		{"a//b/../c", "https://example.com/a/b/a/c", true},
	}
	for _, c := range cases {
		got, ok := Normalize(c.in, base)
		if ok != c.ok {
			t.Errorf("Normalize(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got.String() != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got.String(), c.want)
		}
	}
}

func TestNormalizeEmptyPath(t *testing.T) {
	u, ok := Normalize("https://example.com", nil)
	if !ok || u.Path != "/" {
		t.Errorf("got %v, %v; want path=/", u, ok)
	}
}

func TestCleanPath(t *testing.T) {
	cases := map[string]string{
		"/a/./b":    "/a/b",
		"/a/b/../c": "/a/c",
		"/a//b":     "/a/b",
		"/a/b/":     "/a/b/",
		"/":         "/",
		"/../../x":  "/x",
	}
	for in, want := range cases {
		if got := CleanPath(in); got != want {
			t.Errorf("CleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRootDomain(t *testing.T) {
	cases := map[string]string{
		"www.example.com":   "example.com",
		"a.b.example.co.uk": "example.co.uk",
		"example.com:8080":  "example.com",
		"127.0.0.1":         "127.0.0.1",
		"localhost":         "localhost",
	}
	for in, want := range cases {
		if got := RootDomain(in); got != want {
			t.Errorf("RootDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPushDeduplicates(t *testing.T) {
	f := New(Options{Scope: Any})

	if _, ok := f.Push("https://example.com/a", nil); !ok {
		t.Fatal("first push should be accepted")
	}
	if _, ok := f.Push("https://Example.com:443/a#frag", nil); ok {
		t.Error("normalized duplicate should be rejected")
	}
	if _, ok := f.Push("https://example.com/a?x=1", nil); !ok {
		t.Error("different query should be a new resource")
	}
	if f.Len() != 2 {
		t.Errorf("Len() = %d, want 2", f.Len())
	}
}

func TestPushRelative(t *testing.T) {
	f := New(Options{Scope: Any})
	base := mustURL(t, "https://example.com/dir/page.html")

	u, ok := f.Push("../api/user", base)
	if !ok {
		t.Fatal("relative path should resolve")
	}
	if want := "https://example.com/api/user"; u.String() != want {
		t.Errorf("got %q, want %q", u.String(), want)
	}
}

func TestScopeSameHost(t *testing.T) {
	f := New(Options{Scope: SameHost})
	f.AddSeed(mustURL(t, "https://example.com/"))

	if f.InScope(mustURL(t, "https://sub.example.com/x")) {
		t.Error("sub.example.com should be out of scope for same-host")
	}
	if !f.InScope(mustURL(t, "https://example.com/y")) {
		t.Error("example.com should be in scope")
	}
}

func TestScopeSubdomain(t *testing.T) {
	f := New(Options{Scope: Subdomain})
	f.AddSeed(mustURL(t, "https://www.example.com/"))

	if !f.InScope(mustURL(t, "https://api.example.com/x")) {
		t.Error("api.example.com should be in scope for subdomain")
	}
	if f.InScope(mustURL(t, "https://evil.com/x")) {
		t.Error("evil.com should be out of scope")
	}
}

func TestScopeAny(t *testing.T) {
	f := New(Options{Scope: Any})
	f.AddSeed(mustURL(t, "https://example.com/"))

	if !f.InScope(mustURL(t, "https://totally-different.com/x")) {
		t.Error("Any scope should allow everything")
	}
}

func TestPushFilteredByScope(t *testing.T) {
	f := New(Options{Scope: SameHost})
	f.AddSeed(mustURL(t, "https://example.com/"))

	if _, ok := f.Push("https://other.com/x", nil); ok {
		t.Error("out-of-scope URL should be rejected")
	}
}

// TestNormalizeRelativeToModuleBase 锁定「相对模块路径」的解析契约：
// routegraph 对 Nuxt 的 "./x.js" 保留原样，由这里以当前 JS 的 URL 解析。
func TestNormalizeRelativeToModuleBase(t *testing.T) {
	base := mustURL(t, "https://nuxt.com/_nuxt/CfoxLZfe.js")
	for raw, want := range map[string]string{
		"./DPFkMy5C.js":     "https://nuxt.com/_nuxt/DPFkMy5C.js",
		"../shared/D4e5.js": "https://nuxt.com/shared/D4e5.js",
		"/assets/x.js":      "https://nuxt.com/assets/x.js",
	} {
		u, ok := Normalize(raw, base)
		if !ok || u.String() != want {
			t.Errorf("Normalize(%q) = (%v, %v)，期望 %s", raw, u, ok, want)
		}
	}
}

func TestInsidePathScope(t *testing.T) {
	seed, _ := url.Parse("https://example.com/en-US/")
	fr := New(Options{Scope: Subdomain, InsidePath: true})
	fr.AddSeed(seed)

	cases := []struct {
		raw  string
		want bool
	}{
		{"https://example.com/en-US/docs", true},   // 前缀内
		{"https://example.com/en-US", true},        // 精确等于种子路径
		{"https://example.com/api/orders", false},  // 同站但路径外
		{"https://example.com/en-USX/evil", false}, // 不能只按字符串前缀
	}
	for _, c := range cases {
		u, ok := fr.Push(c.raw, nil)
		if ok != c.want {
			t.Errorf("Push(%q) ok = %v，期望 %v", c.raw, ok, c.want)
		}
		if u == nil && c.want {
			t.Errorf("Push(%q) 返回 nil", c.raw)
		}
	}
}

func TestInsidePathRootSeedUnrestricted(t *testing.T) {
	seed, _ := url.Parse("https://example.com/")
	fr := New(Options{Scope: Subdomain, InsidePath: true})
	fr.AddSeed(seed)
	if _, ok := fr.Push("https://example.com/anything/else", nil); !ok {
		t.Errorf("根种子下 InsidePath 应等于无限制")
	}
}

func TestPathAllowedExported(t *testing.T) {
	seed, _ := url.Parse("https://example.com/docs/")
	fr := New(Options{InsidePath: true})
	fr.AddSeed(seed)

	u1, _ := url.Parse("https://example.com/docs/x")
	if !fr.PathAllowed(u1) {
		t.Errorf("PathAllowed(%q) = false，期望 true", u1)
	}
	u2, _ := url.Parse("https://example.com/other")
	if fr.PathAllowed(u2) {
		t.Errorf("PathAllowed(%q) = true，期望 false", u2)
	}
}

func TestScopeDN(t *testing.T) {
	seed, _ := url.Parse("https://tesla.com/")
	fr := New(Options{Scope: DN})
	fr.AddSeed(seed)

	cases := []struct {
		raw  string
		want bool
	}{
		{"https://tesla.com/order", true},
		{"https://blog.tesla.com/post", true},
		// Contains 语义的已知陷阱：子串命中即放行，"not-tesla.com"
		// 与 "tesla.com.evil.io" 都包含 "tesla.com"，均命中。
		// 需要精确控制时用 -scope regex。
		{"https://not-tesla.com/", true},
		{"https://tesla.com.evil.io/", true},
		{"https://example.com/tesla.com", false}, // host 不含关键字，path 不影响
	}
	for _, c := range cases {
		if _, ok := fr.Push(c.raw, nil); ok != c.want {
			t.Errorf("Push(%q) ok = %v，期望 %v", c.raw, ok, c.want)
		}
	}
}

func TestScopeRegex(t *testing.T) {
	seed, _ := url.Parse("https://company.com/")
	fr := New(Options{Scope: URLRegex,
		ScopeRegex: regexp.MustCompile(`company-staging\.io|company\.com`)})
	fr.AddSeed(seed)

	cases := []struct {
		raw  string
		want bool
	}{
		{"https://company.com/a", true},
		{"https://company-staging.io/b", true},
		{"https://other.com/c", false},
	}
	for _, c := range cases {
		if _, ok := fr.Push(c.raw, nil); ok != c.want {
			t.Errorf("Push(%q) ok = %v，期望 %v", c.raw, ok, c.want)
		}
	}
}
