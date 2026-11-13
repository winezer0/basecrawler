// fuzzpaths.go 提供路径 fuzz 字典的加载与解析（批次 W-1）。
//
// 目的：爬虫顺 a[href]/JS 提取找不到的隐藏入口（管理后台、API 网关、
// 遗留系统、备份文件），用一份精选字典对每个 origin 探测一遍。
// 内置字典是面向 API 发现的高价值子集（fuzzdict-core.txt，约 49 条）。
// 完整 154 条在 fuzzdict.txt，可用 --fuzz-dict 追加（AppendFuzzDict 会自动去重）。
package basecrawler

import (
	_ "embed"
	"strings"
)

//go:embed fuzzdict-core.txt
var builtinFuzzDict string

// ParseFuzzDict 解析字典内容：每行一个路径，# 开头为注释，
// 空行跳过，强制以 / 开头并去重（保序）。
func ParseFuzzDict(content string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "/") {
			line = "/" + line
		}
		if _, dup := seen[line]; dup {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

// BuiltinFuzzDict 返回内置字典的路径列表。
func BuiltinFuzzDict() []string {
	return ParseFuzzDict(builtinFuzzDict)
}

// AppendFuzzDict 把自定义路径追加到 base（通常是内置字典）之后，
// 统一补前导斜杠、保序去重。CLI 的 --fuzz-dict 用它合并两份字典，
// 避免自定义路径与内置条目重复请求。
func AppendFuzzDict(base, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	add := func(p string) {
		p = strings.TrimSpace(strings.TrimSuffix(p, "\r"))
		if p == "" {
			return
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range base {
		add(p)
	}
	for _, p := range extra {
		add(p)
	}
	return out
}
