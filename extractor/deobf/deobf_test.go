package deobf

import (
	"strings"
	"testing"
	"time"
)

// TestLooksObfuscated 验证各混淆族签名命中与正常代码不误报。
func TestLooksObfuscated(t *testing.T) {
	obfuscated := []string{
		"eval(function(p,a,c,k,e,d){while(c--)if(k[c])p=p.replace(new RegExp('%'+c,'g'),k[c]);return p}('50 51=52',62,1,''.split('|')))",
		"var _0x3f2a=function(){var _0x1b2c=['/api/users','/api/orders'];return _0x1b2c}",
		"var _0xabc1=['hello','world'];console.log(_0xabc1.join('-'));document.title=_0xabc1[0];",
		"$=~[];$={___:++$,$$$$:(![]+'')[$],__:++$,$_$_:(![]+'')[$],_$_:++$};console.log($);",
		"j=-~[];k={j:++j,k:++j,q:++j};console.log(k.j+k.k+k.q);var s='\x2fapi';",
	}
	for _, src := range obfuscated {
		if !LooksObfuscated(src) {
			t.Errorf("应判为混淆：%q", trunc(src))
		}
	}
	normal := []string{
		"fetch('/api/users').then(r=>r.json());var total=0;for(let i=0;i<10;i++)total+=i;",
		"const routes=[{path:'/about',component:About}];",
	}
	for _, src := range normal {
		if LooksObfuscated(src) {
			t.Errorf("正常代码误报：%q", trunc(src))
		}
	}
}

// TestRecoverPacker 验证 Dean Edwards packer 解包。
// 手工构造：eval(function(p,a,c,k,e,d){...}('0/1/2',10,3,'api|v1|users'.split('|')))
// 期望还原出 api/v1/users 路径。
func TestRecoverPacker(t *testing.T) {
	payload := `eval(function(p,a,c,k,e,d){while(c--)if(k[c])p=p.replace(new RegExp('\\b'+e(c,a)+'\\b','g'),k[c]);return p}('0/1/2',10,3,'api|v1|users'.split('|')))`
	out, ok := Recover(payload)
	if !ok {
		t.Fatal("packer 未解包")
	}
	if !strings.Contains(out, "api/v1/users") {
		t.Errorf("解包结果缺 api/v1/users：%q", out)
	}
	if LooksObfuscated(out) {
		t.Error("解包后不应再判为混淆（eval 特征已消除）")
	}
}

// TestUnwrapEvalHexEscape 验证 eval 内转义字符串的释放：
// fetch("\x2fapi\x2fv3\x2fusers") 藏在 eval 里，常规提取看不到。
func TestUnwrapEvalHexEscape(t *testing.T) {
	src := `eval("fetch('\x2fapi\x2fv3\x2fusers')")`
	out, ok := Recover(src)
	if !ok {
		t.Fatal("eval 未解包")
	}
	if !strings.Contains(out, "/api/v3/users") {
		t.Errorf("解包结果缺 /api/v3/users：%q", out)
	}
	if LooksObfuscated(out) {
		t.Error("解包后不应再判为混淆")
	}
}

// TestDoRounds 验证嵌套 eval 多轮展开与轮数上限。
func TestDoRounds(t *testing.T) {
	src := `eval("eval(\"var x='\x2fadmin\x2fpanel'\")")`
	out, ok := Do(src, false)
	if !ok {
		t.Fatal("嵌套 eval 未展开")
	}
	if !strings.Contains(out, "/admin/panel") {
		t.Errorf("结果缺 /admin/panel：%q", out)
	}
}

// TestDoNotObfuscated 验证未混淆输入原样返回不变换。
func TestDoNotObfuscated(t *testing.T) {
	src := "fetch('/api/users');"
	out, ok := Do(src, false)
	if ok || out != src {
		t.Errorf("未混淆输入不应变换（ok=%v）", ok)
	}
}

// TestDoForced 验证 force 开关对无特征输入也生效。
func TestDoForced(t *testing.T) {
	src := `eval("var u='\x2fhidden\x2fpath'")`
	out, ok := Do(src, true)
	if !ok {
		t.Fatal("force 模式应尝试解包")
	}
	if !strings.Contains(out, "/hidden/path") {
		t.Errorf("结果缺 /hidden/path：%q", out)
	}
}

// TestUnescapeJSAllEscapes 覆盖 JS 字符串转义解码的全部分支：
// \xNN / \uNNNN、控制转义、引号、非法或截断的转义、未知转义与尾随反斜杠。
// 这些分支只影响「能否把字面量释放出来」，解码错误必须原样保留而不是吞掉。
func TestUnescapeJSAllEscapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain/path`, `plain/path`}, // 无反斜杠快速路径
		{`\x2fapi`, `/api`},          // \xNN
		{`\u002fapi`, `/api`},        // \uNNNN
		{`a\nb\tc\rd`, "a\nb\tc\rd"}, // 控制转义
		{`a\\b`, `a\b`},              // 转义反斜杠
		{`a\"b\'c`, `a"b'c`},         // 转义引号
		{`\xZZ`, `\xZZ`},             // 非法十六进制
		{`\x2`, `\x2`},               // 截断的 \xNN
		{`\uZZZZ`, `\uZZZZ`},         // 非法 Unicode
		{`\u002`, `\u002`},           // 截断的 \uNNNN
		{`\q`, `\q`},                 // 未知转义保留
		{`tail\`, `tail\`},           // 尾随反斜杠
	}
	for _, c := range cases {
		if got := unescapeJS(c.in); got != c.want {
			t.Errorf("unescapeJS(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestUnescapePackerStringBranches 覆盖 payload 里 \' 转义的有无两条路径。
func TestUnescapePackerStringBranches(t *testing.T) {
	if got := unescapePackerString(`a/b`); got != `a/b` {
		t.Errorf("无转义应原样返回，得到 %q", got)
	}
	if got := unescapePackerString(`a\'b\'c`); got != `a'b'c` {
		t.Errorf(`\' 未还原，得到 %q`, got)
	}
}

// TestUnwrapEvalGuards 覆盖展开守卫：解出来不是代码、展开个数触顶、
// 展开总量触顶三种情况都必须保持原串，不能把炸弹放大到内存里。
func TestUnwrapEvalGuards(t *testing.T) {
	plain := `eval("just plain text")`
	if out, ok := unwrapEval(plain); ok || out != plain {
		t.Errorf("非代码内容不应展开（ok=%v, out=%q）", ok, out)
	}

	var b strings.Builder
	for i := 0; i < maxEvalExpansions+5; i++ {
		b.WriteString(`eval("x.y()")`)
	}
	out, ok := unwrapEval(b.String())
	if !ok {
		t.Fatal("前几项应展开")
	}
	if !strings.Contains(out, `eval("x.y()")`) {
		t.Error("超过展开个数上限后，剩余 eval 应原样保留")
	}

	huge := `eval("` + strings.Repeat("a", maxRecoverBytes) + `.x()")`
	if out, ok := unwrapEval(huge); ok || out != huge {
		t.Errorf("超过总字节预算不应展开（ok=%v）", ok)
	}
}

// TestDoSizeBudget 覆盖 Do 的总长预算：超大输入即便 force 也不进入展开循环。
func TestDoSizeBudget(t *testing.T) {
	src := strings.Repeat("a", maxRecoverBytes+1)
	out, ok := Do(src, true)
	if ok || out != src {
		t.Errorf("超预算输入应原样返回（ok=%v）", ok)
	}
}

// TestSandboxExpiredDeadline 覆盖沙箱执行段在 deadline 已过时立即返回。
func TestSandboxExpiredDeadline(t *testing.T) {
	if _, ok := sandboxRecoverBudgeted("var x = 1;", time.Now().Add(-time.Second)); ok {
		t.Error("已过期的 deadline 不应再进 VM")
	}
}

// trunc 截断到 40 字节用于报错信息。
func trunc(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}
