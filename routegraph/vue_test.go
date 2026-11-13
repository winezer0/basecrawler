package routegraph

import "testing"

// TestVueRouteTable：Vue Router 路由表恢复。
// alias 是 Vue Router 特有键；vue-router 标记用于纯 chunk
// （无 webpack/vite 运行时特征）时兜底触发 LooksLikeBundle。
func TestVueRouteTable(t *testing.T) {
	src := `import{createRouter,createWebHistory}from"vue-router";const r=createRouter({history:createWebHistory(),routes:[{path:"/",component:Home,alias:"/home"},{path:"/admin",children:[{path:"users/:id",component:User}]}]})`
	if !LooksLikeBundle([]byte(src)) {
		t.Fatal("vue-router 标记未触发 LooksLikeBundle")
	}
	res := Extract([]byte(src))
	if len(res.Routes) == 0 {
		t.Fatal("路由表未恢复")
	}
	paths := map[string]bool{}
	for _, g := range res.Routes {
		paths[g.Path] = true
	}
	if !paths["/"] {
		t.Errorf("缺 / 路由，得到 %v", paths)
	}
	if !paths["/admin"] {
		t.Errorf("缺 /admin 路由，得到 %v", paths)
	}
	// 嵌套子路由拼接：children 的相对 path 与父 path 拼成真实地址。
	if !paths["/admin/users/:id"] {
		t.Errorf("缺拼接后的嵌套路由 /admin/users/:id，得到 %v", paths)
	}
}

// TestVueMarkerWithoutRuntime：纯路由 chunk——没有 webpack/vite 特征，
// 也没有 createRouter 调用，只剩模块引用；靠 vue-router 字符串触发粗筛。
func TestVueMarkerWithoutRuntime(t *testing.T) {
	src := `var routes=[{path:"/settings",component:o}],m=Object.assign({},(0,r.unref)(n));import"vue-router"`
	if !LooksLikeBundle([]byte(src)) {
		t.Fatal("vue-router 标记未在纯 chunk 上触发 LooksLikeBundle")
	}
}

// TestNestedLayoutWrapper：无 path 的布局包装（只有 children），
// 子路由前缀应跳过包装层继承祖父。
func TestNestedLayoutWrapper(t *testing.T) {
	src := `var routes=[{path:"/app",component:Layout,children:[{component:Panel,children:[{path:"detail/:id",component:D}]}]}]`
	res := Extract([]byte(src))
	paths := map[string]bool{}
	for _, g := range res.Routes {
		paths[g.Path] = true
	}
	if !paths["/app/detail/:id"] {
		t.Errorf("缺隔层拼接 /app/detail/:id，得到 %v", paths)
	}
}

// TestChildAbsolutePath：子路由 path 以 / 开头视为绝对路径，不拼。
func TestChildAbsolutePath(t *testing.T) {
	src := `var routes=[{path:"/admin",component:L,children:[{path:"/login",component:X},{path:"users",component:U}]}]`
	res := Extract([]byte(src))
	paths := map[string]bool{}
	for _, g := range res.Routes {
		paths[g.Path] = true
	}
	if !paths["/login"] {
		t.Errorf("绝对子路径不应被拼接，得到 %v", paths)
	}
	if !paths["/admin/users"] {
		t.Errorf("缺相对子路径拼接 /admin/users，得到 %v", paths)
	}
}
