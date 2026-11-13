package main

import (
	"github.com/winezer0/basecrawler"
	"github.com/winezer0/basecrawler/testfill"
)

// confirmResults 把发送确认结论转成可写入主结果文件的 Result。
//
// 只保留「真正发出且拿到响应状态码」的条目；状态码是否属于有效结果由
// 统一的 writeResult（-s 白名单 / -S 黑名单）判定，这里不重复过滤。
// Source=testfill 是确认记录的标识：status 是测试请求的响应状态而非发现
// 时的状态，method 是实际发送方法（可能是 POST/PUT/PATCH/DELETE），
// URL 是 SentURL()——含填充后的 query，与真正发出的地址一致。
func confirmResults(cfs []testfill.Confirmation) []basecrawler.Result {
	out := make([]basecrawler.Result, 0, len(cfs))
	for _, c := range cfs {
		if !c.Sent || c.StatusCode <= 0 {
			continue
		}
		out = append(out, basecrawler.Result{
			URL:        c.SentURL(),
			Method:     c.Method,
			Source:     "testfill",
			StatusCode: c.StatusCode,
		})
	}
	return out
}
