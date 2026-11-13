package safety

import (
	"fmt"
	"testing"
)

func TestSafetyDefaults(t *testing.T) {
	s, err := NewSafety(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{
		"http://t/logout", "http://t/signout?x=1", "http://t/sign-out",
		"http://t/user/exit", "http://t/app/quit", "http://t/用户/登出",
	} {
		if !s.SkipURL(u) {
			t.Fatalf("应跳过: %s", u)
		}
	}
	for _, u := range []string{"http://t/normal-page", "http://t/catalogue", "http://t/exhibit"} {
		if s.SkipURL(u) {
			t.Fatalf("不应误伤: %s", u)
		}
	}
	if !s.SkipForm("http://t/account", []string{"password_new", "password_confirm"}) {
		t.Fatal("改密码表单应跳过")
	}
	if !s.SkipForm("http://t/admin/delete-user", []string{"confirm"}) {
		t.Fatal("删除类 action 应跳过")
	}
	if !s.SkipForm("http://t/user/remove", []string{"remove_account"}) {
		t.Fatal("删除类字段应跳过")
	}
	// 普通登录表单：password 单独不触发（关键防误伤点）
	if s.SkipForm("http://t/login", []string{"username", "password"}) {
		t.Fatal("普通登录表单不应命中")
	}
	if s.SkipForm("http://t/search", []string{"q"}) {
		t.Fatal("搜索表单不应命中")
	}
}

func TestSafetySkipRequest(t *testing.T) {
	s, err := NewSafety(true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, url string
		fields      []string
		want        bool
	}{
		{"GET", "http://t/logout", nil, true},
		{"POST", "http://t/account", []string{"password_new"}, true},
		{"DELETE", "http://t/api/users/12", nil, false}, // 路径无动作词，不误拦
		{"DELETE", "http://t/api/delete-user/12", nil, true},
		{"POST", "http://t/api/remove", []string{"id"}, true},
		{"GET", "http://t/api/delete-user/12", nil, true}, // 表单词表与动作词表无关，仍命中
		{"GET", "http://t/api/orders", nil, false},
		{"POST", "http://t/api/orders", []string{"qty"}, false},
	}
	for _, c := range cases {
		got, _ := s.SkipRequest(c.method, c.url, c.fields)
		if got != c.want {
			t.Errorf("SkipRequest(%s %s, %v) = %v，期望 %v", c.method, c.url, c.fields, got, c.want)
		}
	}
}

func TestSafetyExtraRules(t *testing.T) {
	s, err := NewSafetyWithActions(true, []string{`/vip/secret`}, []string{`transfer_?money`}, []string{`wipe_?table`})
	if err != nil {
		t.Fatal(err)
	}
	if !s.SkipURL("http://t/vip/secret/panel") {
		t.Fatal("追加 URL 正则未生效")
	}
	if !s.SkipForm("http://t/pay", []string{"transfer_money"}) {
		t.Fatal("追加表单正则未生效")
	}
	if skip, _ := s.SkipRequest("POST", "http://t/db", []string{"wipe_table"}); !skip {
		t.Fatal("追加动作正则未生效")
	}
	for _, args := range [][3][]string{
		{{"((("}, nil, nil},
		{nil, {"*bad"}, nil},
		{nil, nil, {"[bad"}},
	} {
		if _, err := NewSafetyWithActions(true, args[0], args[1], args[2]); err == nil {
			t.Errorf("非法正则应报错: %v", args)
		}
	}
}

func TestSafetyDisabled(t *testing.T) {
	off, err := NewSafety(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled() || off.SkipURL("http://t/logout") ||
		off.SkipForm("http://t/x", []string{"password_new"}) {
		t.Fatal("关闭后不应拦截")
	}
	if skip, _ := off.SkipRequest("DELETE", "http://t/delete-user", nil); skip {
		t.Fatal("关闭后 SkipRequest 不应拦截")
	}
}

func TestIsSafeMethod(t *testing.T) {
	for _, m := range []string{"GET", "get", " HEAD ", "OPTIONS"} {
		if !IsSafeMethod(m) {
			t.Errorf("%q 应为幂等方法", m)
		}
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", ""} {
		if IsSafeMethod(m) {
			t.Errorf("%q 不应为幂等方法", m)
		}
	}
}

// ExampleNewSafety 展示浏览器爬虫（prcrawler）的两个拦截点。
func ExampleNewSafety() {
	s, _ := NewSafety(true, nil, nil)
	fmt.Println(s.SkipURL("https://example.com/logout"))
	// 普通登录表单的 password 不触发，改密表单才拦截
	fmt.Println(s.SkipForm("https://example.com/account", []string{"username", "password"}))
	fmt.Println(s.SkipForm("https://example.com/account", []string{"password_new", "password_confirm"}))
	// Output:
	// true
	// false
	// true
}

// ExampleSafety_SkipRequest 展示静态爬虫（basecrawler）在发送测试请求前的
// 三合一判定：动作词表只在非幂等方法上生效，GET 不误拦。
func ExampleSafety_SkipRequest() {
	s, _ := NewSafetyWithActions(true, nil, nil, nil)
	_, reason := s.SkipRequest("DELETE", "https://example.com/api/remove", []string{"id"})
	fmt.Println(reason)
	skip, _ := s.SkipRequest("GET", "https://example.com/api/remove", nil)
	fmt.Println(skip)
	// Output:
	// 非幂等方法命中动作词表
	// false
}
