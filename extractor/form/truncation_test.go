package form

import (
	"fmt"
	"strings"
	"testing"

	"github.com/winezer0/basecrawler/extractor/base"
)

func TestFormValuesTruncationStats(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><body><form action="/submit">`)
	for i := 0; i < maxFormParams+8; i++ {
		fmt.Fprintf(&b, `<input type="hidden" name="p%d" value="v%d">`, i, i)
	}
	b.WriteString(`</form></body></html>`)
	doc, err := parseFormHTMLRaw(b.String())
	if err != nil {
		t.Fatal(err)
	}

	r := &base.LimitReport{}
	vals := FormParamValues(doc, r)
	if len(vals) != maxFormParams {
		t.Fatalf("产出 %d 条，期望 %d", len(vals), maxFormParams)
	}
	assertOnlyEvent(t, r, base.LimitFormParams, maxFormParams+8, maxFormParams)
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
