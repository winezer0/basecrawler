package main

import "testing"

// TestDefaultMatchCodes 锁定 ffuf 默认白名单：含 405/500，不含线索(0)、404、其它 5xx。
func TestDefaultMatchCodes(t *testing.T) {
	f, err := newStatusFilter(defaultMatchCodes, "")
	if err != nil {
		t.Fatalf("默认码表解析失败: %v", err)
	}
	for _, c := range []struct {
		code int
		want bool
	}{
		{200, true}, {204, true}, {301, true}, {302, true}, {307, true},
		{401, true}, {403, true}, {405, true}, {500, true},
		{0, false}, {201, false}, {206, false}, {303, false},
		{404, false}, {410, false}, {502, false}, {503, false},
	} {
		if got := f.Allow(c.code); got != c.want {
			t.Errorf("默认 Allow(%d) = %v，期望 %v", c.code, got, c.want)
		}
	}
}

// TestStatusFilterRangesAndExclude：范围、0、all，以及黑名单优先于白名单。
func TestStatusFilterRangesAndExclude(t *testing.T) {
	f, err := newStatusFilter("200-399,401,403,0", "301,404")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for _, c := range []struct {
		code int
		want bool
	}{{200, true}, {302, true}, {401, true}, {0, true}, {301, false}, {404, false}, {500, false}} {
		if got := f.Allow(c.code); got != c.want {
			t.Errorf("Allow(%d) = %v，期望 %v", c.code, got, c.want)
		}
	}

	all, err := newStatusFilter("all", "404,500-599")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !all.Allow(0) || !all.Allow(200) || all.Allow(404) || all.Allow(503) {
		t.Error("-s all -S 404,500-599 判定错误")
	}
}

// TestStatusFilterEmptyInclude：兼容旧脚本 -s "" = 不过滤。
func TestStatusFilterEmptyInclude(t *testing.T) {
	f, err := newStatusFilter("", "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for _, code := range []int{0, 200, 404, 500} {
		if !f.Allow(code) {
			t.Errorf("-s 空串应放行 %d", code)
		}
	}
}

// TestStatusFilterInvalid：非法码、越界、倒序范围、黑名单 all 都要报错。
func TestStatusFilterInvalid(t *testing.T) {
	for _, c := range []struct{ inc, exc string }{
		{"2x0", ""}, {"99", ""}, {"600", ""}, {"200-100", ""}, {"", "all"},
	} {
		if _, err := newStatusFilter(c.inc, c.exc); err == nil {
			t.Errorf("newStatusFilter(%q, %q) 应报错", c.inc, c.exc)
		}
	}
}
