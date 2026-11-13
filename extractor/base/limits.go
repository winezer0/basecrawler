package base

import (
	"sort"
	"sync"
)

// 四处提取上限（inlinejson / nextjs / sourcemap / formvalues）原先都是
// `if len(out) >= max { return }`：静默少收，调用方看不出结果被截断过。
// 这个文件只做一件事：把「本该产出多少 / 实际产出多少」变成可观测数据。
//
// 设计约束：
//   - 解析器保持纯函数，统计由返回值带出，不写全局状态；
//   - 聚合器是唯一有状态的地方，并发安全、nil 安全；
//   - 只有真发生截断才记录，避免报告被大量「未截断」事件填满。

// LimitKind 标识一处带上限的提取路径，也是报告里的来源名。
type LimitKind string

const (
	LimitInlineJSON      LimitKind = "inline-json"
	LimitNextFlight      LimitKind = "next-flight"
	LimitSourceMap       LimitKind = "sourcemap"
	LimitFormParams      LimitKind = "form-values"
	LimitRobotsWildcards LimitKind = "robots-wildcards"
)

// LimitEvent 是一次提取的截断统计。
//
// Candidates 是「通过形状判定、去重后本可产出」的条数，Emitted 是实际产出，
// Truncated 为两者之差。它只描述这一处上限，不是全局去重后的 URL 数。
type LimitEvent struct {
	Kind       LimitKind `json:"kind"`
	Candidates int       `json:"candidates"`
	Emitted    int       `json:"emitted"`
	Truncated  int       `json:"truncated"`
}

// newLimitEvent 把算术收在一处，四个解析器不各写一遍。
func NewLimitEvent(kind LimitKind, candidates, emitted int) LimitEvent {
	if candidates < emitted {
		candidates = emitted
	}
	return LimitEvent{
		Kind:       kind,
		Candidates: candidates,
		Emitted:    emitted,
		Truncated:  candidates - emitted,
	}
}

// LimitReport 按 kind 聚合事件。零值可用，nil 指针调用 Record / Events 也安全。
type LimitReport struct {
	mu     sync.Mutex
	byKind map[LimitKind]LimitEvent
}

// Record 累加一次截断；未截断的事件直接忽略。
func (r *LimitReport) Record(e LimitEvent) {
	if r == nil || e.Truncated <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKind == nil {
		r.byKind = make(map[LimitKind]LimitEvent)
	}
	old := r.byKind[e.Kind]
	r.byKind[e.Kind] = LimitEvent{
		Kind:       e.Kind,
		Candidates: old.Candidates + e.Candidates,
		Emitted:    old.Emitted + e.Emitted,
		Truncated:  old.Truncated + e.Truncated,
	}
}

// Events 返回按 kind 排序的快照，调用方拿到的是副本。
func (r *LimitReport) Events() []LimitEvent {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LimitEvent, 0, len(r.byKind))
	for _, e := range r.byKind {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
