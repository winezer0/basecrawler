package routegraph

import "testing"

func routePaths(t *testing.T, src string) map[string]string {
	t.Helper()
	res := Extract([]byte(src))
	out := map[string]string{}
	for _, r := range res.Routes {
		out[r.Path] = r.Source
	}
	return out
}

// TestRouteChildrenBeforePath：键顺序不能影响拼接。
// children 写在父 path 之前时，旧版流式算法在子帧关闭时还看不到 /admin。
func TestRouteChildrenBeforePath(t *testing.T) {
	src := `var routes=[{children:[{path:"users",component:U}],path:"/admin",component:L}]`
	got := routePaths(t, src)
	for _, want := range []string{"/admin", "/admin/users"} {
		if _, ok := got[want]; !ok {
			t.Errorf("缺 %s，得到 %v", want, got)
		}
	}
}

// TestRouteThreeLevelNesting：三层相对路径必须继承完整祖先前缀。
func TestRouteThreeLevelNesting(t *testing.T) {
	src := `var routes=[{path:"/admin",component:L,children:[` +
		`{path:"users",component:U,children:[{path:"detail/:id",component:D}]}` +
		`]}]`
	got := routePaths(t, src)
	for _, want := range []string{"/admin", "/admin/users", "/admin/users/detail/:id"} {
		if _, ok := got[want]; !ok {
			t.Errorf("缺 %s，得到 %v", want, got)
		}
	}
}

// TestRouteEmptyPathChild：path:"" 的默认子路由继承父地址，
// 且其子路由应以父地址为前缀，而不是丢掉 /admin。
func TestRouteEmptyPathChild(t *testing.T) {
	src := `var routes=[{path:"/admin",component:L,children:[` +
		`{path:"",component:Home,children:[{path:"profile",component:P}]}` +
		`]}]`
	got := routePaths(t, src)
	if _, ok := got["/admin/profile"]; !ok {
		t.Errorf("缺 /admin/profile，得到 %v", got)
	}
	if _, ok := got["/profile"]; ok {
		t.Errorf("根相对 /profile 说明丢了祖先前缀，得到 %v", got)
	}
}

// TestRouteAliasExtracted：alias 从「只是识别键」升级为可抓路径，
// 字符串与数组两种写法都覆盖。
func TestRouteAliasExtracted(t *testing.T) {
	src := `var routes=[` +
		`{path:"/",component:Home,alias:"/home"},` +
		`{path:"/admin",component:A,alias:["/manage","panel"]}` +
		`]`
	got := routePaths(t, src)
	for _, want := range []string{"/home", "/manage", "/panel"} {
		if _, ok := got[want]; !ok {
			t.Errorf("缺 alias %s，得到 %v", want, got)
		}
	}
	for _, p := range []string{"/home", "/manage"} {
		if got[p] != "route-alias" {
			t.Errorf("%s 的 Source=%q，期望 route-alias", p, got[p])
		}
	}
}

// TestRouteNestedChildrenContextStack：内层 children 数组关闭后，
// 外层数组后续元素仍应被识别为子路由。
func TestRouteNestedChildrenContextStack(t *testing.T) {
	src := `var routes=[{path:"/app",component:L,children:[` +
		`{path:"a",component:A,children:[{path:"deep",component:D}]},` +
		`{path:"b",component:B}` +
		`]}]`
	got := routePaths(t, src)
	for _, want := range []string{"/app/a/deep", "/app/b"} {
		if _, ok := got[want]; !ok {
			t.Errorf("缺 %s，得到 %v", want, got)
		}
	}
}
