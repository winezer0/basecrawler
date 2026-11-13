package form

import (
	"strings"
	"testing"

	"github.com/winezer0/basecrawler/extractor/base"
)

// TestFormParamValues 锁定「值可静态确定」的取值收集口径。
//
// hidden 的 value 是确定默认值，select/radio/checkbox 的候选集合写在 HTML 里，
// 这些不需要发请求就能得到。而文本框、空值、浏览器默认值（on）、提交按钮
// 都不携带信息，必须排除。
func TestFormParamValues(t *testing.T) {
	const body = `<html><body>
<form action="/search" method="get">
  <input name="q">
  <input type="hidden" name="cat" value="12">
  <input type="hidden" name="csrf" value="">
  <select name="sort">
    <option value="price_asc">价格</option>
    <option value="newest">最新</option>
    <option value="">全部</option>
  </select>
  <input type="radio" name="type" value="sale">
  <input type="radio" name="type" value="rent">
  <input type="checkbox" name="flags" value="on">
  <input type="checkbox" name="tags" value="a">
  <input type="checkbox" name="tags" value="b">
  <input type="submit" name="go" value="Go">
</form>
</body></html>`

	var got []base.ParamValue
	if pv := FormParamValues(parseFormHTML(t, body), nil); len(pv) > 0 {
		got = pv
	}
	if len(got) == 0 {
		t.Fatal("未收集到任何参数取值")
	}

	// P-2 口径变化：文本框（无 type = text）现在也收集（name+type
	// 是填充信息），Kind=input:text、无静态候选。submit 仍排除。
	want := map[string]struct {
		kind   string
		values []string
	}{
		"q":    {"input:text", nil},
		"cat":  {"hidden", []string{"12"}},
		"sort": {"select", []string{"price_asc", "newest"}},
		"type": {"radio", []string{"sale", "rent"}},
		"tags": {"checkbox", []string{"a", "b"}},
	}
	if len(got) != len(want) {
		t.Errorf("期望 %d 个参数，实际 %d 个：%+v", len(want), len(got), got)
	}
	for _, p := range got {
		w, ok := want[p.Name]
		if !ok {
			t.Errorf("不该收集参数 %q：%+v", p.Name, p)
			continue
		}
		if p.Kind != w.kind {
			t.Errorf("%s 的来源期望 %s，实际 %s", p.Name, w.kind, p.Kind)
		}
		if len(p.Values) != len(w.values) {
			t.Errorf("%s 的取值期望 %v，实际 %v", p.Name, w.values, p.Values)
			continue
		}
		for i := range w.values {
			if p.Values[i] != w.values[i] {
				t.Errorf("%s 的取值期望 %v，实际 %v", p.Name, w.values, p.Values)
			}
		}
	}
}

// TestFormParamValuesTextInputs 锁定 P-2 新增口径：文本类 input
// （text/email/password/number/date/tel/url/search）没有静态候选，
// 但「name + type」本身就是填充信息——testfill 按 type 生成测试值。
// 此前这类控件被整体跳过（M 批次的「自动填充输入框」只覆盖了有
// 静态候选的控件，这是缺口）。
func TestFormParamValuesTextInputs(t *testing.T) {
	const body = `<html><body>
<form action="/login" method="post">
  <input name="email" type="email">
  <input name="pass" type="password">
  <input name="age" type="number">
  <input name="q" type="search">
  <input name="nickname">                  <!-- 无 type = text -->
  <input name="phone" type="tel" value="">
  <input name="go" type="submit" value="Go">
  <input name="hidden1" type="hidden" value="tok">
</form>
</body></html>`

	var got []base.ParamValue
	if pv := FormParamValues(parseFormHTML(t, body), nil); len(pv) > 0 {
		got = pv
	}

	want := map[string]struct {
		kind string
		typ  string
	}{
		"email":    {"input:email", "email"},
		"pass":     {"input:password", "password"},
		"age":      {"input:number", "number"},
		"q":        {"input:search", "search"},
		"nickname": {"input:text", "text"}, // 无 type 按 HTML 默认 text
		"phone":    {"input:tel", "tel"},
		"hidden1":  {"hidden", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("期望 %d 个参数，实际 %d：%+v", len(want), len(got), got)
	}
	for _, p := range got {
		w := want[p.Name]
		if p.Kind != w.kind {
			t.Errorf("%s 的来源期望 %s，实际 %s", p.Name, w.kind, p.Kind)
		}
		if p.Type != w.typ {
			t.Errorf("%s 的 type 期望 %q，实际 %q", p.Name, w.typ, p.Type)
		}
		// 文本类无静态候选
		if strings.HasPrefix(p.Kind, "input:") && len(p.Values) != 0 {
			t.Errorf("%s 不应有静态候选：%v", p.Name, p.Values)
		}
	}
}
