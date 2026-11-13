package routegraph

import "testing"

// TestAngularRouteTable：Angular forRoot/loadChildren 路由表恢复。
// 产物无 routes: 字样（数组以变量传入），此前 LooksLikeBundle 不命中。
func TestAngularRouteTable(t *testing.T) {
	src := `var e=RouterModule.forRoot([{path:"admin",loadChildren:()=>import("./admin/admin.module").then(m=>m.AdminModule)},{path:"user/:id",component:r}])`
	if !LooksLikeBundle([]byte(src)) {
		t.Fatal("forRoot 标记未触发 LooksLikeBundle")
	}
	res := Extract([]byte(src))
	if len(res.Routes) == 0 {
		t.Fatal("路由表未恢复")
	}
	paths := map[string]bool{}
	for _, g := range res.Routes {
		paths[g.Path] = true
	}
	if !paths["/admin"] {
		t.Errorf("缺 /admin 路由，得到 %v", paths)
	}
	if !paths["/user/:id"] {
		t.Errorf("缺 /user/:id 路由，得到 %v", paths)
	}
}
