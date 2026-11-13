package extractor

import (
	"strings"
	"testing"
)

// TestFromJSDeobfEvalString 集成：eval 藏的接口路径经反混淆后被提取。
func TestFromJSDeobfEvalString(t *testing.T) {
	// \x2f = /；混淆内容常规提取拿不到（转义+eval 包裹）
	src := `eval("fetch('\x2fapi\x2fv3\x2fsecret-endpoint')")`
	reqs := FromJS(src, Options{Deobfuscate: true})
	if len(reqs) == 0 {
		t.Fatal("反混淆后未提取到结果")
	}
	found := false
	for _, r := range reqs {
		if strings.Contains(r.Raw, "/api/v3/secret-endpoint") {
			found = true
		}
	}
	if !found {
		t.Errorf("结果缺 /api/v3/secret-endpoint：%v", reqs)
	}
}

// TestFromJSDeobfOff 验证关闭 Deobfuscate 时保持旧行为（提取不到）。
func TestFromJSDeobfOff(t *testing.T) {
	src := `eval("fetch('\x2fapi\x2fv3\x2fsecret-endpoint')")`
	reqs := FromJS(src, Options{})
	for _, r := range reqs {
		if strings.Contains(r.Raw, "secret-endpoint") {
			t.Errorf("未开启反混淆不应提取到：%q", r.Raw)
		}
	}
}

// TestFromJSDeobfNormalUnaffected 验证正常代码反混淆不改变提取结果。
func TestFromJSDeobfNormalUnaffected(t *testing.T) {
	src := `fetch('/api/users');fetch('/api/orders/list');`
	base := FromJS(src, Options{})
	with := FromJS(src, Options{Deobfuscate: true})
	if len(base) != len(with) {
		t.Errorf("正常代码结果数变化：%d -> %d", len(base), len(with))
	}
}

// TestFromJSDeobfExec：string-array 沙箱开关语义。
// Options 零值关闭时提取不到（解码器不真跑）；显式打开后 URL 释放。
// 编排层 DefaultOptions 默认打开，但先过 string-array 专属签名粗筛。
func TestFromJSDeobfExec(t *testing.T) {
	// 数组用 charCode 构造：静态提取对它零产出，只有真跑解码器才释放。
	src := "var _0x4f2a=[String.fromCharCode(0x47,0x45,0x54)," +
		"String.fromCharCode(0x2f,0x61,0x70,0x69,0x2f,0x76,0x33,0x2f,0x69,0x6e,0x74,0x65,0x72,0x6e,0x61,0x6c,0x2f,0x72,0x65,0x70,0x6f,0x72,0x74)];\n" +
		"function _0x1a2b(_0x1){return _0x4f2a[_0x1-0x0];}\n" +
		"fetch(_0x1a2b(0x1),{method:_0x1a2b(0x0)});"

	off := FromJS(src, Options{Deobfuscate: true})
	for _, r := range off {
		if strings.Contains(r.Raw, "internal/report") {
			t.Errorf("未开启 DeobfExec 不应提取到：%q", r.Raw)
		}
	}
	on := FromJS(src, Options{Deobfuscate: true, DeobfExec: true})
	found := false
	for _, r := range on {
		if strings.Contains(r.Raw, "internal/report") {
			found = true
		}
	}
	if !found {
		t.Errorf("开启 DeobfExec 后仍提取不到：%v", on)
	}
}
