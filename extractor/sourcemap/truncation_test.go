package sourcemap

import (
	"fmt"
	"strings"
	"testing"

	"github.com/winezer0/basecrawler/extractor/base"
)

func TestSourceMapTruncationStats(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"version":3,"sources":[`)
	for i := 0; i < maxSourceMapEntries+10; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"webpack:///src/f%d.js"`, i)
	}
	b.WriteString(`]}`)

	r := &base.LimitReport{}
	reqs := FromSourceMapWithLimits([]byte(b.String()), r)
	if len(reqs) != maxSourceMapEntries {
		t.Fatalf("产出 %d 条，期望 %d", len(reqs), maxSourceMapEntries)
	}
	assertOnlyEvent(t, r, base.LimitSourceMap, maxSourceMapEntries+10, maxSourceMapEntries)
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
