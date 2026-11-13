package form

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/winezer0/basecrawler/extractor/base"
)

func parseHTML(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	return doc
}

func raws(reqs []base.Request) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Raw)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func parseFormHTMLRaw(s string) (*html.Node, error) {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil, err
	}
	var form *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			form = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if form == nil {
		return nil, fmt.Errorf("no form")
	}
	return form, nil
}

func parseFormHTML(t *testing.T, s string) *html.Node {
	t.Helper()
	form, err := parseFormHTMLRaw(s)
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	return form
}
