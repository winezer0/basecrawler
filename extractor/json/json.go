package json

import (
	stdjson "encoding/json"
	"net/http"

	"github.com/winezer0/basecrawler/extractor/base"
)

// maxJSONURLs 限制单份响应体产出的候选数量，避免大列表接口把队列灌满。
const maxJSONURLs = 200

// FromJSON 从 JSON 响应体里提取「像地址」的字符串值。
//
// 现代前端大量接口返回的是「下一跳」而不是页面链接，例如
// {"next":"/api/orders?page=2"}、{"links":{"self":"/api/items/1"}}。
// 这类地址既不在 HTML 里，也不在 JS 源码里，只出现在响应体中，
// 静态提取抓不到——这是之前最明显的召回缺口之一。
//
// 判定刻意保守：只认以 "/" 或 "http(s)://" 开头的字符串，
// 避免把普通文案、ID、枚举值当成地址。
func FromJSON(body []byte) []base.Request {
	if len(body) == 0 {
		return nil
	}
	var v any
	if err := stdjson.Unmarshal(body, &v); err != nil {
		return nil
	}
	var out []base.Request
	walkJSON(v, &out, 0)
	return out
}

func walkJSON(v any, out *[]base.Request, depth int) {
	if depth > 24 || len(*out) >= maxJSONURLs {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for _, val := range t {
			walkJSON(val, out, depth+1)
			if len(*out) >= maxJSONURLs {
				return
			}
		}
	case []any:
		for _, val := range t {
			walkJSON(val, out, depth+1)
			if len(*out) >= maxJSONURLs {
				return
			}
		}
	case string:
		if base.JSONURL(t) {
			*out = append(*out, base.Request{Raw: t, Method: http.MethodGet, Source: "json"})
		}
	}
}
