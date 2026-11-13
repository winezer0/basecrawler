package inlinejson

import (
	"fmt"
	"strings"
	"testing"

	"github.com/winezer0/basecrawler/extractor/base"
)

func TestInlineJSONTruncationStats(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < maxInlineJSONURLs+10; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q", fmt.Sprintf("/page/%d", i))
	}
	b.WriteByte(']')

	r := &base.LimitReport{}
	reqs := FromInlineJSONWithLimits(b.String(), "nuxt-data", r)
	if len(reqs) != maxInlineJSONURLs {
		t.Fatalf("产出 %d 条，期望 %d", len(reqs), maxInlineJSONURLs)
	}
	assertOnlyEvent(t, r, base.LimitInlineJSON, maxInlineJSONURLs+10, maxInlineJSONURLs)
}

func TestInlineJSONFallbackTruncationStats(t *testing.T) {
	var b strings.Builder
	b.WriteString("window.__STATE__ = {")
	for i := 0; i < maxInlineJSONURLs+7; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%q", fmt.Sprintf("k%d", i), fmt.Sprintf("/fallback/%d", i))
	}
	b.WriteString("};")

	r := &base.LimitReport{}
	reqs := FromInlineJSONWithLimits(b.String(), "initial-state", r)
	if len(reqs) != maxInlineJSONURLs {
		t.Fatalf("产出 %d 条，期望 %d", len(reqs), maxInlineJSONURLs)
	}
	assertOnlyEvent(t, r, base.LimitInlineJSON, maxInlineJSONURLs+7, maxInlineJSONURLs)
}

func assertOnlyEvent(t *testing.T, r *base.LimitReport, kind base.LimitKind, candidates, emitted int) {
	t.Helper()
	events := r.Events()
	if len(events) != 1 {
		t.Fatalf("Events = %+v，期望恰好 1 个", events)
	}
	e := events[0]
	if e.Kind != kind || e.Candidates != candidates || e.Emitted != emitted ||
		e.Truncated != candidates-emitted {
		t.Fatalf("Event = %+v，期望 kind=%s candidates=%d emitted=%d", e, kind, candidates, emitted)
	}
}
