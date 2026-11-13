package routegraph

import (
	"fmt"
	"sort"
)

// ExampleExtract 展示静态恢复的两种产出：懒加载 chunk 的真实文件名
// 与前端路由表（含 :id 参数占位）。输入是典型的 webpack 运行时片段：
// `o.u` 是 chunk 文件名模板，路由表带 name/component。
func ExampleExtract() {
	src := `o.p="/static/js/",o.u=function(e){return"chunk-"+e+"."+{101:"a1b2c3",102:"d4e5f6"}[e]+".js"}
var routes=[
  {path:"/",name:"home",component:function(){return __webpack_require__.e(101)}},
  {path:"/admin/users/:id",name:"user",component:()=>import("./U.js")}
];`
	res := Extract([]byte(src))

	// chunk 按路径排序，保证输出确定（Extract 的返回顺序是扫描顺序，
	// Example 需要跨版本稳定的期望值）。
	chunks := make([]string, 0, len(res.Chunks))
	for _, c := range res.Chunks {
		chunks = append(chunks, c.Path)
	}
	sort.Strings(chunks)
	for _, p := range chunks {
		fmt.Println("chunk:", p)
	}
	for _, r := range res.Routes {
		fmt.Printf("route: %s (name=%s)\n", r.Path, r.Name)
	}
	// Output:
	// chunk: ./U.js
	// chunk: /static/js/chunk-101.a1b2c3.js
	// chunk: /static/js/chunk-102.d4e5f6.js
	// route: / (name=home)
	// route: /admin/users/:id (name=user)
}

// ExampleLooksLikeBundle 展示便宜的前置判定：普通脚本不必进词法扫描，
// 带打包器运行时特征的才是 bundle。省下的是整页 JS 的扫描开销。
func ExampleLooksLikeBundle() {
	plain := []byte("var a=1;console.log(a);")
	runtime := []byte(`o.u=function(e){return"c-"+e+"."+{1:"a"}[e]+".js"};__webpack_require__`)
	fmt.Println("plain:", LooksLikeBundle(plain))
	fmt.Println("runtime:", LooksLikeBundle(runtime))
	// Output:
	// plain: false
	// runtime: true
}
