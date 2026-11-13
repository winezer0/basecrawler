package json

import (
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u
}

// 表驱动：每条规则一组输入输出，新增形态只加一行。
func TestFromJSONPagination(t *testing.T) {
	cases := []struct {
		name string
		body string
		base string
		want []string
	}{
		{
			name: "algolia page/nbPages",
			body: `{"page":0,"nbPages":3,"hits":[]}`,
			base: "https://hn.algolia.com/api/v1/search?query=go&page=0",
			// url.Values.Encode 按字母序输出参数
			want: []string{"https://hn.algolia.com/api/v1/search?page=1&query=go"},
		},
		{
			name: "algolia 最后一页不产出",
			body: `{"page":2,"nbPages":3,"hits":[]}`,
			base: "https://hn.algolia.com/api/v1/search?query=go&page=2",
			want: nil,
		},
		{
			name: "has_more 且 URL 带 page",
			body: `{"has_more":true,"items":[1]}`,
			base: "https://api.stackexchange.com/2.3/tags?site=stackoverflow&pagesize=10&page=1",
			want: []string{"https://api.stackexchange.com/2.3/tags?page=2&pagesize=10&site=stackoverflow"},
		},
		{
			name: "has_more 但 URL 无 page 参数不产出",
			body: `{"has_more":true}`,
			base: "https://a.com/items",
			want: nil,
		},
		{
			name: "has_more 为 false 不产出",
			body: `{"has_more":false,"items":[]}`,
			base: "https://a.com/items?page=1",
			want: nil,
		},
		{
			name: "wikipedia continue 合并",
			// MediaWiki 的 continue 对象里自带一个 "continue" 键，也必须合并回去
			body: `{"continue":{"apcontinue":"Aaron","continue":"gapcontinue||"},"query":{}}`,
			base: "https://en.wikipedia.org/w/api.php?action=query&list=allpages&aplimit=5&format=json",
			want: []string{"https://en.wikipedia.org/w/api.php?action=query&apcontinue=Aaron&aplimit=5&continue=gapcontinue%7C%7C&format=json&list=allpages"},
		},
		{
			name: "skip/limit/total",
			body: `{"total":30,"skip":0,"limit":10,"items":[]}`,
			base: "https://dummyjson.com/products?limit=10&skip=0",
			want: []string{"https://dummyjson.com/products?limit=10&skip=10"},
		},
		{
			name: "skip/limit 到最后一页不产出",
			body: `{"total":30,"skip":20,"limit":10,"items":[]}`,
			base: "https://dummyjson.com/products?limit=10&skip=20",
			want: nil,
		},
		{
			name: "非对象不产出",
			body: `[1,2,3]`,
			base: "https://a.com/x",
			want: nil,
		},
		{
			name: "坏 JSON 不产出",
			body: `not json`,
			base: "https://a.com/x",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FromJSONPagination([]byte(tc.body), mustURL(t, tc.base))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d 条, want %d 条: %+v", len(got), len(tc.want), got)
			}
			for i, r := range got {
				if r.Raw != tc.want[i] {
					t.Errorf("第 %d 条: got %s, want %s", i, r.Raw, tc.want[i])
				}
				if r.Source != "json-pagination" {
					t.Errorf("source: got %s, want json-pagination", r.Source)
				}
			}
		})
	}
}

func TestFromJSONPaginationNilBase(t *testing.T) {
	if got := FromJSONPagination([]byte(`{"page":0,"nbPages":3}`), nil); len(got) != 0 {
		t.Fatalf("nil base 应返回空, got %+v", got)
	}
}

func TestFromJSONPaginationNumbersAreFloats(t *testing.T) {
	// encoding/json 把数字解析成 float64；规则必须正确处理
	got := FromJSONPagination(
		[]byte(`{"page":1,"nbPages":5}`),
		mustURL(t, "https://a.com/list?page=1&size=10"))
	if len(got) != 1 {
		t.Fatalf("got %d 条, want 1", len(got))
	}
	if got[0].Raw != "https://a.com/list?page=2&size=10" {
		t.Errorf("got %s", got[0].Raw)
	}
}
