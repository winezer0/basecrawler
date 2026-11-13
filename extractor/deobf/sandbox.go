package deobf

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// string-array 沙箱：javascript-obfuscator 家族的最后一档。
//
// 静态反解（Do）覆盖 packer / eval 字面量 / 转义，但 string-array 混淆
// （_0x4f2a 数组 + 位移解码函数，调用点形如 _0x1a2b(0x1f)）的解码逻辑
// 只有真跑才知道。本文件把源码放进 goja 沙箱执行一遍建立解码器运行时，
// 再把源码里的解码调用逐个在沙箱里求值，用结果字符串替换调用 token——
// 下游常规字面量提取（AST/正则）随后就能看到真实 URL。
//
// 默认开启，但有两道约束：
//   - 签名粗筛 LooksLikeStringArray：必须同时出现数组声明、解码函数定义、
//     解码调用点。普通 JS、packer/eval 类混淆都不进 VM。
//   - 端到端预算 sandboxTimeout 覆盖正则扫描、goja 解析、执行与逐调用求值，
//     不只是 VM 运行段；源码与展开结果也有字节上限。
//
// 安全边界：这是「不注入网络/文件 API 的进程内 VM」，不是强隔离沙箱。
// 目标代码仍可能消耗 CPU / 内存，超时靠 vm.Interrupt + 外层 wall-clock 返回。
// 显式关闭用 CLI --no-deobf-exec 或 extractor.Options.DeobfExec=false。

const (
	sandboxTimeout        = 2 * time.Second
	maxSandboxCalls       = 5000    // 单份源码最多求值/替换的解码调用数
	maxSandboxNames       = 32      // 最多验证多少个不同的解码器名
	maxSandboxSourceBytes = 2 << 20 // 超过这个体积不进 VM
	maxSandboxExpandBytes = 2 << 20 // 替换后的源码长度上限
)

// decoderCallRe 匹配解码调用：ident(数字) 或 ident(数字, 字符串字面量)。
var decoderCallRe = regexp.MustCompile(
	`(_0x[0-9a-fA-F]+)\(\s*(0x[0-9a-fA-F]+|\d+)\s*(?:,\s*('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"))?\s*\)`)

// stringArrayDeclRe / decoderDefRe 与 decoderCallRe 共同构成
// 「string-array 家族专属」粗筛，避免把普通 JS 送进执行路径。
var (
	stringArrayDeclRe = regexp.MustCompile(`(?:var|let|const)\s+_0x[0-9a-fA-F]{4,}\s*=\s*\[`)
	decoderDefRe      = regexp.MustCompile(`(?:function\s+_0x[0-9a-fA-F]{4,}|(?:var|let|const)\s+_0x[0-9a-fA-F]{4,}\s*=\s*function)`)
)

// LooksLikeStringArray 判断 src 是否值得跑 string-array 沙箱。
// 比 LooksObfuscated 严格得多：后者只用于静态反解，命中 eval("...") 就会为真。
func LooksLikeStringArray(src string) bool {
	if len(src) < 32 || len(src) > maxSandboxSourceBytes {
		return false
	}
	return stringArrayDeclRe.MatchString(src) &&
		decoderDefRe.MatchString(src) &&
		decoderCallRe.MatchString(src)
}

// sandboxPrelude 造一个「怎么调都不炸」的最小环境。string-array 混淆的
// 顶层代码通常只做数组定义与自校验，不需要真 DOM；个别站点会探测
// document/console——桩返回可链式调用的空函数，让 init 走完。
const sandboxPrelude = `
var __sf = function () { return __sf; };
globalThis.self = globalThis;
globalThis.window = globalThis;
globalThis.document = new Proxy(__sf, {
  get: function () { return __sf; },
  apply: function () { return __sf; }
});
globalThis.console = { log: function () {}, warn: function () {}, error: function () {}, info: function () {}, debug: function () {} };
globalThis.location = { href: "http://localhost/", hostname: "localhost", pathname: "/", search: "", hash: "" };
globalThis.navigator = { userAgent: "" };
globalThis.addEventListener = function () {};
globalThis.setTimeout = function () { return 0; };
globalThis.setInterval = function () { return 0; };
`

// SandboxRecover 在 goja 沙箱里执行 src 建立解码器，然后把源码中的
// 解码调用逐个求值替换为真实字符串。未命中专属签名、执行失败、超时或
// 没有任何成功替换时返回原串与 false，调用方沿用静态反解的结果。
func SandboxRecover(src string) (string, bool) {
	if !LooksLikeStringArray(src) {
		return src, false
	}
	type outcome struct {
		out string
		ok  bool
	}
	ch := make(chan outcome, 1)
	go func() {
		out, ok := sandboxRecoverBudgeted(src, time.Now().Add(sandboxTimeout))
		ch <- outcome{out, ok}
	}()
	// 外层 wall-clock 兜住正则扫描与 goja 解析（这两段不受 vm.Interrupt 约束）。
	select {
	case r := <-ch:
		return r.out, r.ok
	case <-time.After(sandboxTimeout):
		return src, false
	}
}

// sandboxRecoverBudgeted 是实际执行段，deadline 从入口起算，
// 解析、执行、求值各阶段共用同一个预算。
func sandboxRecoverBudgeted(src string, deadline time.Time) (string, bool) {
	vm := goja.New()
	var timedOut atomic.Bool
	rem := time.Until(deadline)
	if rem <= 0 {
		return src, false
	}
	timer := time.AfterFunc(rem, func() {
		timedOut.Store(true)
		vm.Interrupt("timeout")
	})
	defer func() {
		timer.Stop()
		vm.ClearInterrupt()
	}()

	if _, err := vm.RunString(sandboxPrelude); err != nil {
		return src, false
	}
	if _, err := vm.RunString(src); err != nil {
		if timedOut.Load() {
			return src, false
		}
		// 顶层抛异常（self-defending、反调试）不硬 abort：解码器可能
		// 已定义，继续尝试求值——求不出值的调用点会保持原样。
	}
	if time.Now().After(deadline) {
		return src, false
	}

	// 一次扫描同时得到「候选解码器名」与「待替换调用点」，
	// 数量上限直接约束前置扫描与后续求值的工作量。
	matches := decoderCallRe.FindAllStringSubmatchIndex(src, maxSandboxCalls)
	names := map[string]bool{}
	for _, m := range matches {
		if len(names) >= maxSandboxNames {
			break
		}
		names[src[m[2]:m[3]]] = true
	}
	callable := map[string]goja.Callable{}
	for name := range names {
		v := vm.Get(name)
		if v == nil {
			continue
		}
		if fn, ok := goja.AssertFunction(v); ok {
			callable[name] = fn
		}
	}
	if len(callable) == 0 {
		return src, false
	}

	var out strings.Builder
	last := 0
	changed := 0
	for _, m := range matches {
		if timedOut.Load() || time.Now().After(deadline) || out.Len() > maxSandboxExpandBytes {
			break
		}
		name := src[m[2]:m[3]]
		fn, ok := callable[name]
		if !ok {
			continue
		}
		args := make([]goja.Value, 0, 2)
		num, err := parseNumLiteral(src[m[4]:m[5]])
		if err != nil {
			continue
		}
		args = append(args, vm.ToValue(num))
		if m[6] >= 0 {
			if s, ok := unquoteJSLiteral(src[m[6]:m[7]]); ok {
				args = append(args, vm.ToValue(s))
			} else {
				continue
			}
		}
		rv, err := fn(goja.Undefined(), args...)
		if err != nil || timedOut.Load() {
			continue
		}
		s, ok := rv.Export().(string)
		if !ok || s == "" || len(s) > 4096 {
			continue
		}
		out.WriteString(src[last:m[0]])
		out.WriteString(strconv.Quote(s))
		last = m[1]
		changed++
	}
	if changed == 0 {
		return src, false
	}
	out.WriteString(src[last:])
	return out.String(), true
}

// parseNumLiteral 解析十进制/十六进制数字字面量。
func parseNumLiteral(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return strconv.ParseInt(s[2:], 16, 64)
	}
	return strconv.ParseInt(s, 10, 64)
}

// unquoteJSLiteral 解出 JS 单引号/双引号字符串字面量的值。
// 复用 unescapeJS 处理 \xNN/\uNNNN 转义；包裹引号由调用方保证存在。
func unquoteJSLiteral(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	q := s[0]
	if q != '\'' && q != '"' {
		return "", false
	}
	inner := s[1:]
	if !strings.HasSuffix(inner, string(q)) {
		return "", false
	}
	inner = inner[:len(inner)-1]
	// 内部不能出现未转义的同种引号——那是字面量截断，不是内容。
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' {
			i++
			continue
		}
		if inner[i] == q {
			return "", false
		}
	}
	return unescapeJS(inner), true
}
