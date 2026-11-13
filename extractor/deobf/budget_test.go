package deobf

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPackerHugeCountIsClamped：count 来自目标源码，必须钳到字典长度，
// 否则一个 9 位数字就能让纯 Go 路径空转上亿次。
func TestPackerHugeCountIsClamped(t *testing.T) {
	src := `eval(function(p,a,c,k,e,d){while(c--)if(k[c])p=p.replace(new RegExp('\\b'+e(c)+'\\b','g'),k[c]);return p}` +
		`('1 0',62,999999999,'/api/x|/api/y'.split('|')))`
	start := time.Now()
	out, ok := Recover(src)
	if !ok {
		t.Fatal("packer 未解包")
	}
	if !strings.Contains(out, "/api/x") || !strings.Contains(out, "/api/y") {
		t.Errorf("字典替换不完整: %s", out)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("count 未被钳制，解包耗时 %v", d)
	}
}

// TestStringArraySignatureGate：沙箱签名必须比静态反混淆签名严格，
// 普通 JS 与 eval 字符串混淆都不该进 VM。
func TestStringArraySignatureGate(t *testing.T) {
	plain := `fetch("/api/orders"); var x = 1; document.title = "hello";`
	evalOnly := `eval("fetch('\x2fapi\x2fv3\x2fsecret-endpoint')")`
	if LooksLikeStringArray(plain) || LooksLikeStringArray(evalOnly) {
		t.Error("普通 JS / eval 混淆不应命中 string-array 专属签名")
	}
	if !LooksLikeStringArray(obfuscatorFixture) {
		t.Error("标准 string-array 夹具应命中签名")
	}
	if LooksObfuscated(evalOnly) && LooksLikeStringArray(evalOnly) {
		t.Error("静态签名命中不应自动放行沙箱执行")
	}
}

// TestSandboxSourceSizeLimit：超过体积上限的源码直接跳过 VM。
func TestSandboxSourceSizeLimit(t *testing.T) {
	src := obfuscatorFixture + strings.Repeat(" ", maxSandboxSourceBytes)
	if _, ok := SandboxRecover(src); ok {
		t.Error("超体积源码不应被执行")
	}
}

// packerSource 拼一条 Dean Edwards packer 调用，用于构造预算边界输入。
func packerSource(payload string, radix, count int, dict string) string {
	return `eval(function(p,a,c,k,e,d){while(c--)if(k[c])p=p.replace(new RegExp('\\b'+e(c)+'\\b','g'),k[c]);return p}('` +
		payload + `',` + strconv.Itoa(radix) + `,` + strconv.Itoa(count) + `,'` + dict + `'.split('|')))`
}

// TestUnpackPackerRejectsBadParams：radix / count 非法时不应发生变换，
// 超大 payload 直接放弃，避免解包阶段吃掉无上限内存。
func TestUnpackPackerRejectsBadParams(t *testing.T) {
	bad := []string{
		packerSource("0/1", 1, 2, "api|v1"),  // radix < 2
		packerSource("0/1", 63, 2, "api|v1"), // radix > 62
		packerSource("0/1", 10, 0, "api|v1"), // count <= 0
		packerSource(strings.Repeat("a", maxRecoverBytes+1), 10, 1, "api"),
	}
	for _, src := range bad {
		out, ok := Recover(src)
		if ok || out != src {
			t.Errorf("非法参数/超预算 payload 应原样返回（ok=%v, len=%d）", ok, len(out))
		}
	}
}

// TestUnpackPackerExpandLimit：字典词过长导致展开结果触顶时必须停止放大，
// 保留已展开的部分而不是继续叠加。
func TestUnpackPackerExpandLimit(t *testing.T) {
	dict := strings.Repeat("B", maxPackerExpandBytes+1)
	out, ok := Recover(packerSource("0", 10, 1, dict))
	if !ok {
		t.Fatal("packer 应发生一次替换")
	}
	if len(out) <= maxPackerExpandBytes {
		t.Fatalf("预期展开结果触顶，实际长度 %d", len(out))
	}
}

// TestUnpackPackerCountHardCap：字典很长时 count 除钳到字典长度外，
// 还要再受 maxPackerCount 硬上限约束，不能被目标站点拖进超长循环。
func TestUnpackPackerCountHardCap(t *testing.T) {
	dict := make([]string, maxPackerCount+8)
	for i := range dict {
		dict[i] = "w" + strconv.Itoa(i)
	}
	start := time.Now()
	out, ok := Recover(packerSource("0", 10, 999999999, strings.Join(dict, "|")))
	if !ok {
		t.Fatal("packer 未解包")
	}
	if !strings.Contains(out, "w0") {
		t.Errorf("字典第 0 项应被替换，得到 %q", trunc(out))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("count 硬上限未生效，解包耗时 %v", d)
	}
}

// TestEncodeWordRadixBoundaries 覆盖进制编码的 0 值与最高位字母。
func TestEncodeWordRadixBoundaries(t *testing.T) {
	for _, c := range []struct {
		n, radix int
		want     string
	}{
		{0, 2, "0"},
		{0, 62, "0"},
		{61, 62, "Z"},
		{62, 62, "10"},
		{7, 8, "7"},
	} {
		if got := encodeWord(c.n, c.radix); got != c.want {
			t.Errorf("encodeWord(%d, %d) = %q，期望 %q", c.n, c.radix, got, c.want)
		}
	}
}
