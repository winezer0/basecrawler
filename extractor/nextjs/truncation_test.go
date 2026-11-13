package nextjs

import (
	"fmt"
	"strings"
	"testing"

	"github.com/winezer0/basecrawler/extractor/base"
)

func TestNextFlightTruncationStats(t *testing.T) {
	var b strings.Builder
	b.WriteString(`self.__next_f.push([1,"`)
	for i := 0; i < maxFlightPaths+10; i++ {
		fmt.Fprintf(&b, `\"\/page\/%d\",`, i)
	}
	b.WriteString(`"])`)

	r := &base.LimitReport{}
	reqs := FromNextFlightWithLimits(b.String(), r)
	if len(reqs) != maxFlightPaths {
		t.Fatalf("产出 %d 条，期望 %d", len(reqs), maxFlightPaths)
	}
	assertOnlyEvent(t, r, base.LimitNextFlight, maxFlightPaths+10, maxFlightPaths)
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
