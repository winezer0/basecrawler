package basecrawler

import (
	"os"
	"strings"
	"testing"
)

// TestBuiltinFuzzDict 验证内置字典非空且全部以 / 开头。
func TestBuiltinFuzzDict(t *testing.T) {
	dict := BuiltinFuzzDict()
	// 内置是面向 API 发现的高价值子集（30~60 条），完整字典另行分发。
	if len(dict) < 30 || len(dict) > 60 {
		t.Fatalf("内置字典 %d 条，超出高价值子集预期范围 30~60", len(dict))
	}
	seen := map[string]bool{}
	for _, p := range dict {
		if !strings.HasPrefix(p, "/") {
			t.Errorf("路径 %q 未以 / 开头", p)
		}
		if seen[p] {
			t.Errorf("路径 %q 重复", p)
		}
		seen[p] = true
	}
	// P0-1：不允许 /x 与 /x/ 同时存在（多数站点只差一次 301，重复请求）。
	for p := range seen {
		if strings.HasSuffix(p, "/") && seen[strings.TrimSuffix(p, "/")] {
			t.Errorf("尾斜杠冗余：%q 与去斜杠版本并存", p)
		}
	}
}

// TestCoreDictIsSubsetOfFull：内置高价值子集必须完全来自完整字典，
// 这样 --fuzz-dict fuzzdict.txt 追加后才能还原 154 条且不产生新路径。
func TestCoreDictIsSubsetOfFull(t *testing.T) {
	raw, err := os.ReadFile("fuzzdict.txt")
	if err != nil {
		t.Fatalf("读取完整字典: %v", err)
	}
	full := map[string]bool{}
	for _, p := range ParseFuzzDict(string(raw)) {
		full[p] = true
	}
	if len(full) < 100 {
		t.Fatalf("完整字典仅 %d 条，疑似被误精简", len(full))
	}
	for _, p := range BuiltinFuzzDict() {
		if !full[p] {
			t.Errorf("内置条目 %q 不在完整字典中", p)
		}
	}
}

// TestParseFuzzDict 验证解析规则：注释、空行、去重、斜杠补全。
func TestParseFuzzDict(t *testing.T) {
	got := ParseFuzzDict("# 注释\n/admin\n\nadmin\r\n/admin\n/api\n")
	want := []string{"/admin", "/api"}
	if len(got) != len(want) {
		t.Fatalf("解析出 %d 条（%v），期望 %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 条 = %q，期望 %q", i, got[i], want[i])
		}
	}
	if got := ParseFuzzDict("# 只有注释\n\n"); len(got) != 0 {
		t.Errorf("全注释应解析出 0 条，得到 %v", got)
	}
}

// TestAppendFuzzDict：--fuzz-dict 的追加语义——内置条目保留、新增条目在后、
// 补前导斜杠、跨列表去重且保序。
func TestAppendFuzzDict(t *testing.T) {
	got := AppendFuzzDict([]string{"/admin", "/api"}, []string{"/admin", "custom", "/api/", ""})
	want := []string{"/admin", "/api", "/custom", "/api/"}
	if len(got) != len(want) {
		t.Fatalf("合并结果 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 条 = %q，期望 %q", i, got[i], want[i])
		}
	}
}
