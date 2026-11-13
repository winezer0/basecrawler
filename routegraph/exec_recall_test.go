package routegraph

import (
	"strings"
	"testing"
	"time"
)

// TestExecWebpackChildrenAndAlias：执行路径不能只看顶层 path，
// children 要递归拼接、alias 要产出可抓地址。
func TestExecWebpackChildrenAndAlias(t *testing.T) {
	src := strings.Join([]string{
		`(self.webpackChunkapp = self.webpackChunkapp || []).push([["routes"], {`,
		`  500: function (m, e, r) {`,
		`    e.routes = [{`,
		`      path: "/admin", alias: "/manage",`,
		`      children: [{ path: "users/:id", component: function () { return r.e(101); } }]`,
		`    }];`,
		`  }`,
		`}]);`,
	}, "\n")
	res, err := ExtractByExec([]byte(src), ExecOptions{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("webpack 执行失败: %v", err)
	}
	paths := map[string]bool{}
	for _, r := range res.Routes {
		paths[r.Path] = true
	}
	for _, want := range []string{"/admin", "/manage", "/admin/users/:id"} {
		if !paths[want] {
			t.Errorf("缺 %s，得到 %v", want, paths)
		}
	}
}

// TestExecESMChildrenAliasAndModulePath：ESM 路径同样递归 children，
// 且动态 import 的模块 id 要转成可抓的相对 chunk 路径。
func TestExecESMChildrenAliasAndModulePath(t *testing.T) {
	src := strings.Join([]string{
		`export const routes = [{`,
		`  path: "/admin", alias: ["/manage", "panel"],`,
		`  children: [{`,
		`    path: "users", component: function () { return import("./user.js"); }`,
		`  }]`,
		`}];`,
	}, "\n")
	res, err := ExtractByExec([]byte(src), ExecOptions{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("ESM 执行失败: %v", err)
	}
	paths := map[string]bool{}
	for _, r := range res.Routes {
		paths[r.Path] = true
	}
	for _, want := range []string{"/admin", "/manage", "/panel", "/admin/users"} {
		if !paths[want] {
			t.Errorf("缺 %s，得到 %v", want, paths)
		}
	}
	foundChunk := false
	for _, c := range res.Chunks {
		if c.Path == "./user.js" && c.Source == "exec.import" {
			foundChunk = true
		}
	}
	if !foundChunk {
		t.Errorf("动态 import 的模块 id 未转成可抓路径，得到 %v", res.Chunks)
	}
}

// TestModuleIDToPath：裸模块名、webpack 数字 ID、外链都不该变成 chunk 路径。
func TestModuleIDToPath(t *testing.T) {
	cases := map[string]string{
		"vue-router":           "",
		"react":                "",
		"101":                  "",
		"https://cdn/x.js":     "",
		"./user.js":            "./user.js",
		"/assets/detail-a1.js": "/assets/detail-a1.js",
		"../shared/panel.css":  "../shared/panel.css",
	}
	for in, want := range cases {
		if got := moduleIDToPath(in); got != want {
			t.Errorf("moduleIDToPath(%q) = %q，期望 %q", in, got, want)
		}
	}
}
