package json

import (
	stdjson "encoding/json"
	"net/url"
	"strconv"

	"github.com/winezer0/basecrawler/extractor/base"
)

// FromJSONPagination 从 JSON 响应体里构造「计算型分页」的下一页地址。
//
// 显式字段（{"next": "..."}）与 Link 头由 FromJSON / FromHeaders 覆盖；
// 这里只处理「响应体里没有下一页地址、必须由当前 URL 构造」的形态。
// 规则集与 tools/pagination.py 的闸门测量保持一致——闸门先证明有漏检，
// 实现的规则不超出闸门验证过的形态。
//
// 纯函数：不请求、不落盘；构造出的地址交给编排层入队，
// 链式翻页由 frontier 的去重与 -max-urls 自然封顶。
func FromJSONPagination(body []byte, b *url.URL) []base.Request {
	if b == nil || len(body) == 0 {
		return nil
	}
	var doc map[string]any
	if err := stdjson.Unmarshal(body, &doc); err != nil {
		return nil
	}
	out := make([]base.Request, 0, 4)
	for _, rule := range paginationRules {
		if raw, ok := rule(doc, b); ok {
			out = append(out, base.Request{
				Raw: raw, Method: "GET", Source: "json-pagination",
				Attribute: "next-page",
			})
		}
	}
	return out
}

func asInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

func asStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func withQuery(b *url.URL, updates url.Values) string {
	q := b.Query()
	for k, vs := range updates {
		q[k] = vs
	}
	c := *b
	c.RawQuery = q.Encode()
	return c.String()
}

func pagePlusOne(b *url.URL, page int) string {
	return withQuery(b, url.Values{"page": {strconv.Itoa(page + 1)}})
}

// paginationRules 是表驱动的规则集：每条规则拿 (doc, b) 构造下一页。
// 顺序即优先级；同一次响应最多产出 len(paginationRules) 条。
var paginationRules = []func(doc map[string]any, b *url.URL) (string, bool){
	// page/nbPages（Algolia 形态）：还有页才构造。
	func(doc map[string]any, b *url.URL) (string, bool) {
		p, ok1 := asInt(doc["page"])
		n, ok2 := asInt(doc["nbPages"])
		if !ok1 || !ok2 || p >= n-1 {
			return "", false
		}
		return pagePlusOne(b, p), true
	},
	// has_more（Stack Exchange 形态）：URL 必须带 page，否则构造不出。
	func(doc map[string]any, b *url.URL) (string, bool) {
		if doc["has_more"] != true {
			return "", false
		}
		s, ok := asStr(b.Query().Get("page"))
		if !ok || s == "" {
			return "", false
		}
		p, err := strconv.Atoi(s)
		if err != nil {
			return "", false
		}
		return pagePlusOne(b, p), true
	},
	// continue（MediaWiki 形态）：把 continue 对象合并回查询参数。
	func(doc map[string]any, b *url.URL) (string, bool) {
		cont, ok := doc["continue"].(map[string]any)
		if !ok || len(cont) == 0 {
			return "", false
		}
		updates := url.Values{}
		for k, v := range cont {
			if s, ok := asStr(v); ok {
				updates.Set(k, s)
			}
		}
		if len(updates) == 0 {
			return "", false
		}
		return withQuery(b, updates), true
	},
	// skip/limit/total（dummyjson 形态）：offset 型分页。
	func(doc map[string]any, b *url.URL) (string, bool) {
		skip, ok1 := asInt(doc["skip"])
		limit, ok2 := asInt(doc["limit"])
		total, ok3 := asInt(doc["total"])
		if !ok1 || !ok2 || !ok3 || skip+limit >= total {
			return "", false
		}
		return withQuery(b, url.Values{
			"skip":  {strconv.Itoa(skip + limit)},
			"limit": {strconv.Itoa(limit)},
		}), true
	},
}
