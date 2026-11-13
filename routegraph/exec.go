package routegraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// 执行兜底：把 webpack 的懒加载 chunk 真跑一遍，从模块导出的路由对象里取路由。
//
// 默认编入，没有构建标签。代价是链接 goja 的完整 VM，实测让二进制
// 从 15.2MB 涨到 23.7MB（+8.5MB）；换来的是「能力与二进制」唯一对应，
// 不再出现「这个二进制没有执行兜底」的口径分裂。
// 是否真的执行由 Options.RouteExec 决定，默认开启；调用方还应先过
// LooksExecutable 粗筛，避免把普通脚本送进 VM。
//
// 只解决静态解析拿不到的那一种情况：路由表由运行时 map/concat 生成，
// 源码里根本没有完整的 path 字面量。
//
// 明确的能力边界（实测）：
//   - 只对「chunk 通过 self.webpackChunkX.push([[...], {模块表}]) 自注册」的产物有效。
//     入口 bundle 把模块表关在 IIFE 闭包里，外部无法枚举，这类拿不到。
//   - ESM / Vite 产物已支持：goja 拒绝解析时经 esbuild 转译为 CJS 再执行，
//     动态 import() 由词法器改写为桩；依赖模块不递归加载，仍是单模块执行。
//   - 会执行目标站的代码，因此必须有 2MB 体积上限与端到端超时
//     （解析、转译、VM 运行共用一个 deadline）。

// ErrSkipExec 表示这份源码不具备执行条件（非自注册 chunk、ESM 转译失败等）。
// 默认执行兜底是「尽力而为」，调用方应静默忽略这类错误而不是刷屏。
var ErrSkipExec = errors.New("跳过执行兜底")

// ExecOptions 控制执行兜底的行为。
type ExecOptions struct {
	// Timeout 单次执行的时间上限，默认 2 秒。
	Timeout time.Duration
	// MaxSourceBytes 超过这个体积不执行，默认 2MB。
	MaxSourceBytes int
}

func (o ExecOptions) withDefaults() ExecOptions {
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Second
	}
	if o.MaxSourceBytes <= 0 {
		o.MaxSourceBytes = 2 << 20
	}
	return o
}

// prelude 造一个足够让模块初始化跑起来的最小浏览器环境。
//
// document/window 的桩按「怎么调都不炸」设计：
// 读任何属性返回自身，调用返回自身，避免模块在 init 阶段就因为
// 拿不到 API 而中断——我们只要它把路由表构造出来。
const execPrelude = `
globalThis.__payloads = [];
var __stub = new Proxy(function () {}, {
  get: function (t, k) {
    if (k === "then") return undefined;          // 不要被当成 Promise
    if (k === Symbol.toPrimitive) return function () { return ""; };
    if (k === "length") return 0;
    return __stub;
  },
  apply: function (t, self, args) { return __stub; },
  construct: function () { return __stub; }
});
var __self = {
  webpackChunkapp: {
    push: function (item) { globalThis.__payloads.push(item); return 0; }
  },
  location: { href: "http://localhost/", pathname: "/", search: "", hash: "" },
  navigator: { userAgent: "" },
  addEventListener: function () {},
  removeEventListener: function () {},
  setTimeout: function () { return 0; },
  clearTimeout: function () {},
  localStorage: __stub,
  sessionStorage: __stub
};
globalThis.self = __self;
globalThis.window = __self;
globalThis.document = __stub;
globalThis.location = __self.location;
globalThis.navigator = __self.navigator;
globalThis.XMLHttpRequest = __stub;
globalThis.fetch = function () { return __stub; };
globalThis.Image = __stub;
globalThis.__routers = [];
globalThis.__exportedRoutes = [];
` + fwStubSrc

// routeCollectSrc 是两条执行路径共用的路由摊平逻辑：
// children 递归拼接、alias 展开、懒加载字段触发（记 chunk id）。
// 只用 ES5 语法，goja 可直接跑。
const routeCollectSrc = `
var __out = [];
var __seenPath = {};
var __lazyKeys = ["component", "element", "lazy", "loadChildren"];
function __joinPath(base, p) {
  if (typeof p !== "string" || p === "") return base || "";
  if (p.charAt(0) === "/" || p.charAt(0) === "*") return p;
  if (!base || base === "/") return "/" + p;
  return base.replace(/\/$/, "") + "/" + p;
}
function __emitPath(p, it) {
  if (typeof p !== "string" || p === "") return;
  if (__seenPath[p]) return;
  __seenPath[p] = 1;
  __out.push({ path: p, name: typeof it.name === "string" ? it.name : "" });
}
function __pushRoute(it, base) {
  if (!it || typeof it !== "object") return;
  var p = __joinPath(base || "", it.path);
  if (typeof it.path === "string") __emitPath(p, it);
  var al = it.alias;
  if (typeof al === "string") __emitPath(__joinPath(base || "", al), it);
  else if (Object.prototype.toString.call(al) === "[object Array]") {
    for (var a = 0; a < al.length; a++) __emitPath(__joinPath(base || "", al[a]), it);
  }
  for (var k = 0; k < __lazyKeys.length; k++) {
    var fn = it[__lazyKeys[k]];
    if (typeof fn === "function") { try { fn(); } catch (e) {} }
  }
  var kids = it.children;
  if (Object.prototype.toString.call(kids) === "[object Array]") {
    for (var c = 0; c < kids.length; c++) __pushRoute(kids[c], p);
  }
}
`

// execLoader 在 chunk 执行完之后跑：建 require、遍历模块表、抓路由。
//
// 关键是桩函数的行为：当它被传入一个带 routes 数组的对象时（createRouter/createWebHistory
// 这类框架 API），把这个对象记下来并原样返回。否则路由表会被桩吞掉，什么都拿不到。
const execLoader = routeCollectSrc + `
// 载荷可能来自两条路径：
//   1) (self.webpackChunkX = self.webpackChunkX || []).push(...) —— 被注入的 push 捕获
//   2) self.webpackChunkX = []; ...push(...) —— 直接覆盖成真数组，拦截失效，
//      只能事后从数组里读回来
var __payloads2 = __payloads.slice();
try {
  var __arr = globalThis.self && globalThis.self.webpackChunkapp;
  if (__arr && typeof __arr.length === "number") {
    for (var a = 0; a < __arr.length; a++) { __payloads2.push(__arr[a]); }
  }
} catch (e) {}

var __mods = {};
for (var i = 0; i < __payloads2.length; i++) {
  var p = __payloads2[i];
  if (p && p[1] && typeof p[1] === "object") {
    for (var k in p[1]) { if (typeof p[1][k] === "function") __mods[k] = p[1][k]; }
  }
}

var __cache = {};
var __chunkIds = [];

function __webpack_require__(id) {
  if (__cache[id]) return __cache[id].exports;
  var m = __cache[id] = { id: id, exports: {} };
  var f = __mods[id];
  if (typeof f !== "function") {                     // 依赖模块不在本 chunk 里，给桩
    var stub = __fwStub();                           // 注意要把桩缓存住：
    __cache[id].exports = stub;                      // 否则第二次 require 会拿到空对象
    return stub;
  }
  try {
    f(m, m.exports, __webpack_require__);
  } catch (e) {
    m.__err = String(e);
  }
  return m.exports;
}
__webpack_require__.p = "/";
__webpack_require__.u = function (e) { return String(e); };
__webpack_require__.e = function (e) { __chunkIds.push(String(e)); return Promise.resolve(); };
__webpack_require__.d = function (exports, def) {
  for (var k in def) { try { exports[k] = def[k]; } catch (e) {} }
};
__webpack_require__.r = function (exports) {
  try { exports.__esModule = true; } catch (e) {}
};
__webpack_require__.n = function (m) { return function () { return m; }; };

var __moduleIds = [];
for (var id in __mods) { __moduleIds.push(id); }
__moduleIds.sort();

for (var j = 0; j < __moduleIds.length; j++) {
  var ex;
  try { ex = __webpack_require__(__moduleIds[j]); } catch (e) { continue; }
  if (!ex || typeof ex !== "object") continue;
  // 直接导出 routes 数组
  if (Object.prototype.toString.call(ex.routes) === "[object Array]") {
    globalThis.__routers.push(ex);
  }
  // 导出默认值本身是路由数组
  var d = ex.default;
  if (Object.prototype.toString.call(d) === "[object Array]" && d.length &&
      d[0] && typeof d[0] === "object" && ("path" in d[0])) {
    globalThis.__exportedRoutes.push({ routes: d });
  }
}

// 把各路路由表摊平：children 递归、alias 展开、懒加载 thunk 触发记 chunk id。
for (var r = 0; r < __routers.length; r++) {
  var rs = __routers[r].routes || [];
  for (var q = 0; q < rs.length; q++) { __pushRoute(rs[q], ""); }
}
for (var x = 0; x < __exportedRoutes.length; x++) {
  var arr = __exportedRoutes[x].routes || [];
  for (var y = 0; y < arr.length; y++) { __pushRoute(arr[y], ""); }
}

JSON.stringify({ routes: __out, chunkIds: __chunkIds });
`

// ExtractByExec 执行一段 bundle（webpack 自注册 chunk 或 ESM/Vite 模块），
// 从模块导出里恢复路由与懒加载 chunk id。
//
// 安全性：会运行目标站的 JS。默认开启（Options.RouteExec），调用方应先用
// LooksExecutable 粗筛。2MB 体积上限 + 2 秒**端到端**预算：解析、esbuild
// 转译、VM 运行、结果提取全在同一 deadline 内；外层 wall-clock 保证
// 连不受 vm.Interrupt 约束的解析/转译阶段也不会拖住调用方。
func ExtractByExec(src []byte, opts ExecOptions) (Result, error) {
	opts = opts.withDefaults()
	if len(src) > opts.MaxSourceBytes {
		return Result{}, fmt.Errorf("源码 %d 字节超过执行上限 %d", len(src), opts.MaxSourceBytes)
	}
	type outcome struct {
		res Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, e := extractByExec(src, opts, time.Now().Add(opts.Timeout))
		ch <- outcome{r, e}
	}()
	select {
	case o := <-ch:
		return o.res, o.err
	case <-time.After(opts.Timeout):
		return Result{}, fmt.Errorf("执行超时（%v，含解析与转译）", opts.Timeout)
	}
}

func execRemaining(deadline time.Time) time.Duration {
	return time.Until(deadline)
}

func execTimeoutErr(opts ExecOptions) error {
	return fmt.Errorf("执行超时（%v，含解析与转译）", opts.Timeout)
}

// extractByExec 是实际执行段，deadline 覆盖从 goja.Parse 到结果反序列化的全过程。
func extractByExec(src []byte, opts ExecOptions, deadline time.Time) (Result, error) {
	// 先按脚本模式解析：webpack 自注册 chunk 是普通脚本；
	// ESM（import/export/import()）在这里就报语法错误，转走转译路径。
	// 解析一次的成本远小于两次执行，且避免对 ESM 源码跑错误重试。
	if _, perr := goja.Parse("chunk.js", string(src)); perr != nil {
		if execRemaining(deadline) <= 0 {
			return Result{}, execTimeoutErr(opts)
		}
		cjs, terr := esmToCJS(src)
		if terr != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrSkipExec, perr)
		}
		if execRemaining(deadline) <= 0 {
			return Result{}, execTimeoutErr(opts)
		}
		return extractByExecESM(cjs, opts, deadline)
	}

	if !strings.Contains(string(src), "push") {
		return Result{}, fmt.Errorf("%w: 不是自注册 chunk", ErrSkipExec)
	}
	if execRemaining(deadline) <= 0 {
		return Result{}, execTimeoutErr(opts)
	}

	vm := goja.New()
	var timedOut atomic.Bool
	timer := time.AfterFunc(execRemaining(deadline), func() {
		timedOut.Store(true)
		vm.Interrupt("timeout")
	})
	defer func() {
		timer.Stop()
		vm.ClearInterrupt()
	}()

	if _, err := vm.RunString(execPrelude); err != nil {
		return Result{}, fmt.Errorf("准备执行环境: %w", err)
	}
	if _, err := vm.RunString(string(src)); err != nil {
		if timedOut.Load() {
			return Result{}, execTimeoutErr(opts)
		}
		return Result{}, fmt.Errorf("执行 chunk: %w", err)
	}
	v, err := vm.RunString(execLoader)
	if err != nil {
		if timedOut.Load() {
			// loader 里的 try/catch 会吞掉 interrupt，不能只靠返回的 error 判断
			return Result{}, execTimeoutErr(opts)
		}
		return Result{}, fmt.Errorf("读取导出: %w", err)
	}
	if timedOut.Load() {
		return Result{}, execTimeoutErr(opts)
	}

	var out struct {
		Routes []struct {
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"routes"`
		ChunkIDs []string `json:"chunkIds"`
	}
	if err := json.Unmarshal([]byte(v.String()), &out); err != nil {
		return Result{}, fmt.Errorf("解析执行结果: %w", err)
	}
	return buildExecResult(out.Routes, out.ChunkIDs), nil
}

// buildExecResult 把 loader 返回的 JSON 摊平成 Result，webpack 与 ESM 两条
// 执行路径共用：路由过 validRoutePath 去重，chunk id 保留编号待静态层对齐。
func buildExecResult(routes []struct {
	Path string `json:"path"`
	Name string `json:"name"`
}, chunkIDs []string) Result {
	var res Result
	seen := map[string]struct{}{}
	for _, r := range routes {
		if !validRoutePath(r.Path) {
			continue
		}
		if _, dup := seen[r.Path]; dup {
			continue
		}
		seen[r.Path] = struct{}{}
		res.Routes = append(res.Routes, Route{Path: r.Path, Name: r.Name, Source: "exec"})
	}
	// webpack 数字 ID 只能给出编号，文件名靠静态 u 函数表在编排层对齐；
	// ESM 动态 import 的模块 id 若是相对/绝对资源路径，直接转成可抓候选，
	// 由 frontier 按当前 JS 的 URL 解析成完整地址。
	sid := map[string]struct{}{}
	for _, id := range chunkIDs {
		if _, dup := sid[id]; dup {
			continue
		}
		sid[id] = struct{}{}
		ch := Chunk{ID: id, Source: "exec.e"}
		if p := moduleIDToPath(id); p != "" {
			ch.Path = p
			ch.Source = "exec.import"
		}
		res.Chunks = append(res.Chunks, ch)
	}
	return res
}

// execAssetExts 是模块 id 被视为可抓资源的扩展白名单。
var execAssetExts = []string{
	".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".css", ".json", ".html", ".vue", ".svg",
}

// moduleIDToPath 把 ESM 动态 import 的模块 id 转成候选路径。
// 裸模块名（vue-router / react）与 webpack 数字 ID 都不是地址，直接跳过；
// 只有带路径分隔符且带前端资源扩展的 id 才值得入队。
func moduleIDToPath(id string) string {
	if id == "" || len(id) > 200 || strings.ContainsAny(id, " \t\n\r\"'<>\\") {
		return ""
	}
	lower := strings.ToLower(id)
	if strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") || strings.HasPrefix(id, "//") {
		return ""
	}
	if !strings.Contains(id, "/") {
		return ""
	}
	for _, e := range execAssetExts {
		if strings.Contains(lower, e) {
			return id
		}
	}
	return ""
}
