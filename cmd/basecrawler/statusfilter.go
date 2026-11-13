package main

import (
	"fmt"
	"strconv"
	"strings"
)

// 状态码过滤照 ffuf 的 matcher/filter 模型：一白一黑两张码表。
//   - -s / --status       白名单（包含），默认 defaultMatchCodes
//   - -S / --filter-status 黑名单（排除），默认空
//
// 判定顺序固定：先黑名单命中则丢弃，再看白名单。支持逗号分隔的精确码、
// 范围（200-299）、all（全放行）以及 0（StatusCode==0 的线索/未访问结果）。
// 状态 0 在这里只是普通码，不再有「线索不受 -s 影响」这类例外。
const defaultMatchCodes = "200,204,301,302,307,401,403,405,500"

type statusSet struct {
	codes map[int]bool
	all   bool
}

func (s statusSet) has(code int) bool {
	return s.all || s.codes[code]
}

type statusFilter struct {
	include statusSet
	exclude statusSet
}

// Allow 判定某个状态码是否写入结果文件。
func (f statusFilter) Allow(code int) bool {
	if f.exclude.has(code) {
		return false
	}
	return f.include.has(code)
}

// parseStatusSet 解析状态码表。allowAll 控制是否接受 all 关键字与空串
// （白名单接受；黑名单不允许 all，避免「排除一切」的静默误用）。
func parseStatusSet(raw string, allowAll bool) (statusSet, error) {
	set := statusSet{codes: make(map[int]bool)}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if allowAll {
			set.all = true // 兼容旧脚本：-s "" 表示不过滤
		}
		return set, nil
	}
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" {
			continue
		}
		if tok == "all" {
			if !allowAll {
				return set, fmt.Errorf("黑名单不支持 all，请写具体状态码")
			}
			set.all = true
			continue
		}
		if strings.Contains(tok, "-") {
			parts := strings.SplitN(tok, "-", 2)
			lo, err1 := strconv.Atoi(parts[0])
			hi, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil || lo > hi || lo < 0 || hi > 599 {
				return set, fmt.Errorf("非法状态码范围 %q（示例 200-299）", tok)
			}
			for c := lo; c <= hi; c++ {
				set.codes[c] = true
			}
			continue
		}
		v, err := strconv.Atoi(tok)
		if err != nil || v < 0 || v > 599 || (v > 0 && v < 100) {
			return set, fmt.Errorf("非法状态码 %q（允许 0 或 100-599）", tok)
		}
		set.codes[v] = true
	}
	if len(set.codes) == 0 && !set.all {
		return set, fmt.Errorf("状态码表为空")
	}
	return set, nil
}

// newStatusFilter 组装白/黑名单，解析错误带参数名，便于 CLI 直接报给用户。
func newStatusFilter(matchRaw, filterRaw string) (statusFilter, error) {
	inc, err := parseStatusSet(matchRaw, true)
	if err != nil {
		return statusFilter{}, fmt.Errorf("invalid -s/--status: %w", err)
	}
	exc, err := parseStatusSet(filterRaw, false)
	if err != nil {
		return statusFilter{}, fmt.Errorf("invalid -S/--filter-status: %w", err)
	}
	return statusFilter{include: inc, exclude: exc}, nil
}
