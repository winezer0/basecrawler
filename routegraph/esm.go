package routegraph

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// ESM / Vite 产物的执行兜底。
//
// 之前这条路线是结构性失效的：goja 不认 import/export，连动态 import()
// 都报语法错误，而 Vue 3 等现代生态的入口 chunk 就是 ESM。
// 解法是 esbuild 的 Transform API（这是 esbuild pkg/api 唯一暴露的
// 纯转译入口）把 ESM 转成 CJS，再喂给 goja：
//
//   - 静态 import 变成 require(...) —— 桩返回路由感知 stub；
//   - 动态 import() 转译后变成 Promise.resolve().then(() => require(...))
//     形态，require 桩同时记下请求的模块 id，作为懒加载 chunk 候选；
//   - import.meta 在 CJS 输出里被 esbuild 替换为 {}（带 warning，可忽略）。
//
// 执行环境与 webpack 路径共用 execPrelude（含路由感知 stub），
// 模块以 (function(exports, module, require){...}) 包一层执行，
// 跑完后从 exports 与 stub 捕获里摊平路由。

// esmToCJS 把 ESM 源码转成 CJS。转译失败（源码本身不是合法 JS）返回错误。
//
// esbuild 的 CJS 输出会原样保留动态 import()（platform=browser 时不去重写），
// goja 仍然不认——用词法器把代码区的 `import(` 改写为 `__import(`（字符串、
// 模板、注释、正则区不受影响），桩在 extractByExecESM 里定义。
func esmToCJS(src []byte) ([]byte, error) {
	res := api.Transform(string(src), api.TransformOptions{
		Loader:   api.LoaderJS,
		Format:   api.FormatCommonJS,
		Platform: api.PlatformBrowser,
		// 目标取保守值：goja 支持到 ES2020 量级，不追求新语法降级到位，
		// 转译的目的只是去掉模块语法，不是做语法兼容。
		Target:           api.ES2020,
		MinifyWhitespace: false,
	})
	if len(res.Errors) > 0 {
		return nil, fmt.Errorf("ESM 转译失败: %s", res.Errors[0].Text)
	}
	return rewriteDynamicImport(res.Code), nil
}

// rewriteDynamicImport 把代码区的动态 import( 调用改写为 __import(。
// 走词法器而不是正则：字符串字面量里的 "import(" 不能被误改。
func rewriteDynamicImport(src []byte) []byte {
	l := NewLexer(src)
	type span struct{ start, end int32 }
	var hits []span
	for {
		t := l.Next()
		if t.Kind == TokEOF {
			break
		}
		if t.Kind == TokIdent && t.Value(src) == "import" {
			// 跳过空白后必须是 ( 才是动态 import；import.meta 已被 esbuild 处理
			l.skipSpace()
			if l.peek(0) == '(' {
				hits = append(hits, span{t.Start, t.End})
			}
		}
	}
	if len(hits) == 0 {
		return src
	}
	out := make([]byte, 0, len(src))
	last := int32(0)
	for _, h := range hits {
		out = append(out, src[last:h.start]...)
		out = append(out, []byte("__import")...)
		last = h.end
	}
	out = append(out, src[last:]...)
	return out
}

// esmLoader 在转译后的 CJS 模块跑完后收集路由与动态 import 的模块 id。
// 与 webpack 版 loader 的差别：没有 chunk payload / 模块表遍历，
// 只看两处——stub 捕获的 __routers（createRouter 这类框架 API 的入参）
// 和模块导出对象上的 routes / default。
const esmLoader = routeCollectSrc + `
for (var r = 0; r < __routers.length; r++) {
  var rs = __routers[r].routes || [];
  for (var q = 0; q < rs.length; q++) { __pushRoute(rs[q], ""); }
}
try {
  var ex = __module.exports;
  if (ex && typeof ex === "object") {
    if (Object.prototype.toString.call(ex.routes) === "[object Array]") {
      for (var i = 0; i < ex.routes.length; i++) { __pushRoute(ex.routes[i], ""); }
    }
    var d = ex.default;
    if (Object.prototype.toString.call(d) === "[object Array]" && d.length &&
        d[0] && typeof d[0] === "object" && ("path" in d[0])) {
      for (var j = 0; j < d.length; j++) { __pushRoute(d[j], ""); }
    }
  }
} catch (e) {}
JSON.stringify({ routes: __out, chunkIds: __imports });
`

// extractByExecESM 执行转译后的 ESM 模块，恢复路由与动态 import 的模块 id。
// deadline 与 webpack 路径共用同一个端到端预算（含 goja.Parse + esbuild 转译）。
func extractByExecESM(cjs []byte, opts ExecOptions, deadline time.Time) (Result, error) {
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
	// fwStubSrc 必须先于 setup 定义：setup 里的 require 桩会调它。
	if _, err := vm.RunString(fwStubSrc); err != nil {
		return Result{}, fmt.Errorf("准备路由 stub: %w", err)
	}
	setup := `
globalThis.__imports = [];
globalThis.__module = { exports: {} };
globalThis.__import = function (id) {
  try { __imports.push(String(id)); } catch (e) {}
  return Promise.resolve(__fwStub());
};
globalThis.__require = function (id) {
  try { __imports.push(String(id)); } catch (e) {}
  return __fwStub();
};
`
	if _, err := vm.RunString(setup); err != nil {
		return Result{}, fmt.Errorf("准备 require 桩: %w", err)
	}
	wrapped := "(function (exports, module, require) {" + string(cjs) + "\n}).call(globalThis.__module.exports, globalThis.__module.exports, globalThis.__module, globalThis.__require);"
	if _, err := vm.RunString(wrapped); err != nil {
		if timedOut.Load() {
			return Result{}, execTimeoutErr(opts)
		}
		return Result{}, fmt.Errorf("执行 ESM 模块: %w", err)
	}
	v, err := vm.RunString(esmLoader)
	if err != nil {
		if timedOut.Load() {
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

// fwStubSrc 是路由感知 stub 的顶层定义，从 execLoader 的 JS 里提出来，
// 供 webpack 与 ESM 两条执行路径共用。
const fwStubSrc = `
function __fwStub() {
  return new Proxy(function () {}, {
    get: function (t, k) {
      if (k === "then") return undefined;
      return __fwStub();
    },
    apply: function (t, self, args) {
      for (var i = 0; i < args.length; i++) {
        var a = args[i];
        if (a && typeof a === "object" && Object.prototype.toString.call(a.routes) === "[object Array]") {
          globalThis.__routers.push(a);
          return a;
        }
      }
      return __fwStub();
    },
    construct: function () { return __fwStub(); }
  });
}
`
