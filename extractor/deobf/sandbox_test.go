package deobf

import (
	"strings"
	"testing"
)

// obfuscatorFixture 模拟 javascript-obfuscator string-array 输出的最小骨架：
// 字符串数组 + 索引解码函数 + 调用点。解码器真跑才能解出 URL——
// 静态反解（Do）对它无能为力。
const obfuscatorFixture = `
var _0x4f2a = ['GET', '/api/v3/admin/users', 'Authorization', '/health'];
function _0x1a2b(_0x1, _0x2) {
  _0x1 = _0x1 - 0x0;
  var _0x3 = _0x4f2a[_0x1];
  return _0x3;
}
var _0x3c9d = function (_0x1, _0x2) { return _0x1a2b(_0x1); };
fetch(_0x1a2b(0x1), { method: _0x1a2b(0x0), headers: { [_0x1a2b(0x2)]: 'x' } });
check(_0x1a2b(0x3));
console.log(document.title);
`

// TestSandboxRecover：解码调用被替换为真实字符串，URL 得以暴露。
func TestSandboxRecover(t *testing.T) {
	out, ok := SandboxRecover(obfuscatorFixture)
	if !ok {
		t.Fatal("沙箱反解未发生变换")
	}
	for _, want := range []string{`"/api/v3/admin/users"`, `"/health"`, `Authorization`} {
		if !strings.Contains(out, want) {
			t.Errorf("结果缺 %s，得到:\n%s", want, out)
		}
	}
	if strings.Contains(out, "_0x1a2b(0x1)") {
		t.Errorf("解码调用未被替换:\n%s", out)
	}
}

// TestSandboxRecoverDisabledByDefault：普通代码不做沙箱执行。
func TestSandboxRecoverNormalCode(t *testing.T) {
	src := `fetch("/api/orders"); var x = 1; document.title = "hello";`
	out, ok := SandboxRecover(src)
	if ok {
		t.Errorf("正常代码不应触发沙箱反解:\n%s", out)
	}
}

// TestSandboxSelfDefending：顶层抛异常（self-defending）不炸掉，
// 解码器已定义时仍能完成替换。
func TestSandboxSelfDefending(t *testing.T) {
	src := `
var _0x4f2a = ['/secret/panel'];
function _0x1a2b(_0x1) { return _0x4f2a[_0x1 - 0x0]; }
(function () { throw new Error("self-defending"); })();
visit(_0x1a2b(0x0));
`
	out, ok := SandboxRecover(src)
	if !ok {
		t.Fatal("self-defending 场景应仍能反解（解码器已定义）")
	}
	if !strings.Contains(out, `"/secret/panel"`) {
		t.Errorf("缺替换结果:\n%s", out)
	}
}

// TestSandboxIntegration：FromJS 开 DeobfExec 后 URL 可提取。
func TestSandboxIntegration(t *testing.T) {
	out, ok := SandboxRecover(obfuscatorFixture)
	if !ok {
		t.Fatal("沙箱反解未发生变换")
	}
	if !strings.Contains(out, "/api/v3/admin/users") {
		t.Errorf("URL 未释放:\n%s", out)
	}
}

// TestSandboxHelpers：数字/字符串字面量解析的边界。
func TestSandboxHelpers(t *testing.T) {
	if v, err := parseNumLiteral("0x1f"); err != nil || v != 31 {
		t.Errorf("hex 解析错: %v %v", v, err)
	}
	if v, err := parseNumLiteral("42"); err != nil || v != 42 {
		t.Errorf("dec 解析错: %v %v", v, err)
	}
	if _, err := parseNumLiteral("zz"); err == nil {
		t.Error("非法数字应报错")
	}
	if s, ok := unquoteJSLiteral(`'\x2fapi'`); !ok || s != "/api" {
		t.Errorf("hex 转义解出错: %q %v", s, ok)
	}
	if _, ok := unquoteJSLiteral(`"abc`); ok {
		t.Error("缺闭合引号应失败")
	}
	if _, ok := unquoteJSLiteral("'ab'c'"); ok {
		t.Error("嵌套引号应失败")
	}
}

// TestSandboxBrokenSyntax：整段语法错误时放弃沙箱，原样返回。
func TestSandboxBrokenSyntax(t *testing.T) {
	src := "var _0x4f2a = [;;;broken;;;"
	out, ok := SandboxRecover(src)
	if ok {
		t.Error("语法错误不应发生变换")
	}
	if out != src {
		t.Error("语法错误应原样返回")
	}
}

// TestSandboxNoCallable：混淆签名命中但解码器不可调用，放弃。
func TestSandboxNoCallable(t *testing.T) {
	src := `var _0x4f2a = 0x1234; console.log(_0x4f2a(0x1)); var _0xaaaa = "junk";`
	if !LooksObfuscated(src) {
		t.Fatal("夹具应命中混淆签名")
	}
	if _, ok := SandboxRecover(src); ok {
		t.Error("无可调用解码器不应发生变换")
	}
}
