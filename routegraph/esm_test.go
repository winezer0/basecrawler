package routegraph

import (
	"strings"
	"testing"
	"time"
)

// TestExecESMModule：Vite 风格 ESM chunk——goja 脚本模式解析必挂，
// 走 esbuild 转译路径后应能恢复运行时生成（map）的路由与动态 import。
// 这是此前「ESM/Vite 结构性失效」的回归测试。
func TestExecESMModule(t *testing.T) {
	src := strings.Join([]string{
		`import { ref } from "vue";`,
		`const sections = ["billing", "audit", "settings"];`,
		`export const routes = sections.map(s => ({ path: "/" + s, name: s }));`,
		`export default [`,
		`  { path: "/admin/users/:id", name: "user", component: () => import("./user.js") },`,
		`  { path: "/report", name: "report", component: () => import("./report.js") },`,
		`];`,
	}, "\n")

	res, err := ExtractByExec([]byte(src), ExecOptions{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("ESM 执行失败: %v", err)
	}
	paths := map[string]bool{}
	for _, r := range res.Routes {
		paths[r.Path] = true
	}
	// 静态拿不到的 map 生成路由必须出现
	for _, want := range []string{"/billing", "/audit", "/settings", "/admin/users/:id", "/report"} {
		if !paths[want] {
			t.Errorf("缺路由 %s，得到 %v", want, paths)
		}
	}
	if len(res.Chunks) == 0 {
		t.Errorf("动态 import 的模块 id 未捕获")
	}
}

// TestExecESMCreateRouter：Vue Router 初始化走 stub 捕获的场景。
func TestExecESMCreateRouter(t *testing.T) {
	src := strings.Join([]string{
		`import { createRouter, createWebHistory } from "vue-router";`,
		`export const router = createRouter({`,
		`  history: createWebHistory(),`,
		`  routes: [{ path: "/dashboard", name: "dash" }],`,
		`});`,
	}, "\n")

	res, err := ExtractByExec([]byte(src), ExecOptions{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("ESM 执行失败: %v", err)
	}
	found := false
	for _, r := range res.Routes {
		if r.Path == "/dashboard" {
			found = true
		}
	}
	if !found {
		t.Errorf("createRouter 路由未捕获，得到 %v", res.Routes)
	}
}

// TestRewriteDynamicImport：代码区的 import( 被改写，字符串里的不被碰。
func TestRewriteDynamicImport(t *testing.T) {
	src := []byte(`var s = "call import( 'x' ) here"; export {a}; load(import("./y.js"));`)
	out := rewriteDynamicImport(src)
	if !strings.Contains(string(out), `import( 'x' )`) {
		t.Errorf("字符串字面量被误改:\n%s", out)
	}
	if !strings.Contains(string(out), `__import("./y.js")`) {
		t.Errorf("动态 import 未被改写:\n%s", out)
	}
	// import.meta 不在改写范围（esbuild 已处理）；裸 import 后无 ( 不动
	if strings.Contains(string(out), `__import {`) {
		t.Errorf("静态 import 被误改:\n%s", out)
	}
}
