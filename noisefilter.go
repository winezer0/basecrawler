package basecrawler

import "strings"

// noisyJSPath 判定 js-ast 提取候选里的「高噪音路径」。
//
// 依据批次 I-1 的实测（tools/samples/noise-2026-11-08.json）：
// remix.run 的 404 有 40.2%，其中——
//   - 74 条是 .pnpm / node_modules 虚拟包路径：构建产物里残留的
//     依赖包内部路径，部署后必然 404；
//   - 33 条是 .ts/.tsx 等源码模块名被当成资产路径；
//   - 7 条是 /albums/:albumId 这类带路由参数的模板。
//
// 三类合计 114/118。Kind=route 的参数模板已有「只报告不请求」保护，
// 这里补的是 js-ast 来源的同类形态。纯函数，只看路径不看来源——
// 来源限定由调用方（toTasks）负责，避免误伤 HTML 链接来源的合法地址。
func noisyJSPath(raw string) bool {
	path := raw
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if strings.Contains(path, "node_modules/") || strings.Contains(path, "/.pnpm/") {
		return true
	}
	if sourceCodeExt(path) {
		return true
	}
	return hasRouteParam(path)
}

// sourceCodeExt 判定源码扩展。表驱动，新增框架源码扩展只加一行。
func sourceCodeExt(path string) bool {
	last := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		last = path[i+1:]
	}
	switch {
	case strings.HasSuffix(last, ".ts"),
		strings.HasSuffix(last, ".tsx"),
		strings.HasSuffix(last, ".jsx"),
		strings.HasSuffix(last, ".vue"),
		strings.HasSuffix(last, ".svelte"),
		strings.HasSuffix(last, ".coffee"),
		strings.HasSuffix(last, ".elm"):
		return true
	}
	return false
}

// hasRouteParam 判定 /:name 形态的路由参数段。
// 冒号必须出现在段首（/user/:id），排除时间字符串与查询串。
func hasRouteParam(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if len(seg) > 1 && seg[0] == ':' && isParamName(seg[1]) {
			return true
		}
	}
	return false
}

func isParamName(b byte) bool {
	return b == '_' || b == '$' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
