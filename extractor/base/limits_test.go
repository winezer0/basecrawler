package base

import (
	"testing"
)

func TestNewLimitEvent(t *testing.T) {
	e := NewLimitEvent(LimitInlineJSON, 12, 5)
	if e.Candidates != 12 || e.Emitted != 5 || e.Truncated != 7 {
		t.Fatalf("newLimitEvent = %+v", e)
	}
	// 调用方计数写反时不应产出负的截断数
	if got := NewLimitEvent(LimitInlineJSON, 3, 9); got.Truncated != 0 || got.Candidates != 9 {
		t.Fatalf("倒挂输入 = %+v", got)
	}
}

func TestLimitReportAggregatesByKind(t *testing.T) {
	r := &LimitReport{}
	r.Record(NewLimitEvent(LimitInlineJSON, 10, 8))
	r.Record(NewLimitEvent(LimitInlineJSON, 10, 7))
	r.Record(NewLimitEvent(LimitSourceMap, 6, 5))
	r.Record(NewLimitEvent(LimitNextFlight, 4, 4)) // 未截断，应被忽略

	events := r.Events()
	if len(events) != 2 {
		t.Fatalf("Events = %+v，期望 2 个 kind", events)
	}
	if events[0].Kind != LimitInlineJSON || events[1].Kind != LimitSourceMap {
		t.Fatalf("Events 未按 kind 排序: %+v", events)
	}
	if events[0].Candidates != 20 || events[0].Emitted != 15 || events[0].Truncated != 5 {
		t.Fatalf("inline-json 聚合 = %+v", events[0])
	}
}

func TestLimitReportNilSafe(t *testing.T) {
	var r *LimitReport
	r.Record(NewLimitEvent(LimitInlineJSON, 9, 3))
	if got := r.Events(); got != nil {
		t.Fatalf("nil report Events = %+v，期望 nil", got)
	}
}
