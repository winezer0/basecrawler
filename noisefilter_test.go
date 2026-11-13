package basecrawler

import "testing"

// 表驱动：I-1 实测的噪音形态 -> 应过滤；正常资产 -> 应保留。
func TestNoisyJSPath(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"pnpm 虚拟路径", "/assets/npm/.pnpm/@remix-run+route-pattern@1.0.0/node_modules/@remix-run/route-pattern/dist/lib/href.js", true},
		{"node_modules 路径", "/node_modules/vue/dist/vue.js", true},
		{"源码扩展 ts", "/app/router.ts", true},
		{"源码扩展 tsx", "/assets/app/ui/public/icon.tsx", true},
		{"源码扩展 vue", "/src/components/App.vue", true},
		{"路由参数模板", "/albums/:albumId", true},
		{"路由参数模板2", "/user/:id/posts", true},
		{"正常 js 资产", "/assets/app.9f2a.js", false},
		{"正常 css", "/reset.css", false},
		{"无扩展页面", "/docs/guide", false},
		{"绝对地址正常", "https://cdn.a.com/lib.js", false},
		{"时间协议字符串", "12:30:45", false},
		{"查询串含冒号", "/api/items?sort=desc:alpha", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := noisyJSPath(tc.raw); got != tc.want {
				t.Errorf("noisyJSPath(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
