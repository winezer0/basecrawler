package routegraph

import "bytes"

// Result 是一次静态恢复的产出。
type Result struct {
	Chunks []Chunk
	Routes []Route
	// Truncated 表示源码超过词法上限，后半部分没有参与扫描，
	// 因此 Chunks/Routes 可能不完整。调用方应当据此提示用户，
	// 而不是把「没找到」当成「不存在」。
	Truncated bool
}

// Empty 表示没有任何产出。
func (r Result) Empty() bool { return len(r.Chunks) == 0 && len(r.Routes) == 0 }

// Extract 从一份前端产物里静态恢复 chunk 与路由。
//
// 纯静态：不执行任何代码，对 ESM / Vite 产物同样有效。
// 输入应当是 JS 文件内容（内联脚本也可以）。
func Extract(src []byte) Result {
	if len(src) == 0 {
		return Result{}
	}
	tl := lexAll(src)

	var res Result
	publicPath := tl.webpackPublicPath()

	// chunk 来源按可靠性排序：webpack 运行时清单 > vite 依赖表 > 动态 import
	res.Chunks = append(res.Chunks, tl.webpackChunks(publicPath)...)
	res.Chunks = append(res.Chunks, tl.viteChunks()...)
	res.Chunks = append(res.Chunks, tl.dynamicImports()...)
	res.Chunks = dedupeChunks(res.Chunks)

	res.Routes = tl.routeTables()
	res.Truncated = tl.truncated
	return res
}

// dedupeChunks 按路径去重，保留先出现的来源（越靠前越可靠）。
func dedupeChunks(in []Chunk) []Chunk {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, c := range in {
		key := c.Path
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

// bundleMarkers 是「值得跑一遍词法恢复」的粗筛标记，按产物来源分组。
// 命中任一即可；这里只判必要性，精确性交给后面的规则。
var bundleMarkers = [][]byte{
	// webpack 4/5
	[]byte("__webpack_require__"), []byte("webpackJsonp"), []byte("webpackChunk"), []byte("miniCssF"),
	// Vite / Rollup（`__vite__mapDeps` 已被 `__vite__` 覆盖）
	[]byte("__vite__"), []byte("__vitePreload"),
	// 显式路由表
	[]byte("createRouter"), []byte("createWebHistory"), []byte("createBrowserRouter"),
	[]byte("routes:"), []byte("routes="),
	// Angular：RouterModule.forRoot(routes) / loadChildren 懒加载。
	// 产物里常没有 routes: 字样（数组以变量传入 forRoot），漏标记
	// 会让 Angular 站点的路由恢复整段不执行。
	[]byte("forRoot"), []byte("loadChildren"), []byte("RouterModule"),
	// 文件路由框架：这些入口不一定带上面两类运行时标记，
	// 漏掉会让 chunk/路由恢复整段不执行（2026-11-07 在 Nuxt 上确认）。
	// Vue Router：import 被打包器内联后 createRouter 通常仍在，
	// 但纯 chunk 里可能只剩模块路径，补标记兜底（2026-11-11 对齐 Angular 做法）。
	[]byte("vue-router"),
	[]byte("__NUXT__"), []byte("_nuxt/"), []byte("_app/immutable/"),
	[]byte("buildManifest"), []byte("__data.json"),
}

// execMarkers 是「值得真跑一遍」的第二道粗筛。
// LooksLikeBundle 只判是否打包产物；执行目标站代码要更严：
// 必须同时出现「运行时/路由构造」特征，普通业务 JS 不进 VM。
var execMarkers = [][]byte{
	[]byte("webpackChunk"), []byte("webpackJsonp"), []byte("__webpack_require__"),
	[]byte("createRouter"), []byte("createWebHistory"), []byte("createBrowserRouter"),
	[]byte("vue-router"), []byte("RouterModule"), []byte("forRoot"),
	[]byte("routes:"), []byte("routes="), []byte("children:"),
	[]byte("__vite__mapDeps"), []byte("import("), []byte("export "),
}

// LooksExecutable 粗判一段脚本是否值得放进 goja 执行兜底。
//
// 两道门槛：先过 LooksLikeBundle（确认是打包/路由产物），再命中
// execMarkers（确认存在运行时路由构造或模块语法）。长度下限挡掉
// 内联小脚本。这里只判必要性，不保证执行一定能拿到路由。
func LooksExecutable(src []byte) bool {
	if len(src) < 128 || !LooksLikeBundle(src) {
		return false
	}
	for _, mark := range execMarkers {
		if bytes.Contains(src, mark) {
			return true
		}
	}
	return false
}

// LooksLikeBundle 粗判一段脚本是否值得做 chunk/路由恢复。
//
// 只做「有没有必要」的判断，不做精确性判断：
// 内容太短、或既没有运行时清单特征也没有路由特征时直接跳过，
// 避免在每个小脚本上跑一遍词法分析。
func LooksLikeBundle(src []byte) bool {
	if len(src) < 64 {
		return false
	}
	// 用 bytes.Contains 而不是先转成 string：body 最大 2MB，
	// 每个脚本多一次整包拷贝没有意义。
	for _, mark := range bundleMarkers {
		if bytes.Contains(src, mark) {
			return true
		}
	}
	return false
}
