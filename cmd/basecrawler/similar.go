// similar.go 实现相似 URL 过滤（V-9）：复用 winezer0/requestfilter 的
// 覆盖参数策略 + 动态路径段泛化，把 /users/123、/users/456 这类
// 「同形状、不同 ID」的 URL 归并为一条，防爬虫产出无限膨胀。
package main

import (
	"fmt"
	"os"

	"github.com/winezer0/requestfilter"
)

// pathSegmentThreshold 与 katana -fsu 的默认口径一致（10）：
// 同一路径位置累计 10 个不同值后才泛化为参数并纳入覆盖判定，
// 阈值太小会把正常的多级路径误折叠。
const pathSegmentThreshold = 10

// similarFilter 包装 requestfilter.Session，输出层语义只有 allow/drop。
type similarFilter struct {
	session *requestfilter.Session
	allowed int
	dropped int
}

// newSimilarFilter 建立覆盖参数策略会话。
func newSimilarFilter() (*similarFilter, error) {
	s, err := requestfilter.NewSession(requestfilter.Options{
		DedupStrategy:        requestfilter.DedupStrategyCoverageParameters,
		PathSegmentThreshold: pathSegmentThreshold,
	})
	if err != nil {
		return nil, err
	}
	return &similarFilter{session: s}, nil
}

// Allow 判定 URL 是否放行。策略语义（精确重复 / 参数覆盖 / 路径泛化
// 覆盖）由 session 决定，这里只做动作映射。
//
// 错误 fail-open：非法 URL 等解析失败一律放行并计入 allowed——
// 过滤器自身故障绝不能吞掉爬虫产出。
func (f *similarFilter) Allow(rawURL string) bool {
	decision, reservation, err := f.session.Reserve(requestfilter.Request{
		Method: "GET",
		URL:    rawURL,
		// 爬虫输出的是无 body 的 GET 线索；不标记 BodyCaptured 会让
		// 参数状态永远 Partial，覆盖判定与路径泛化整体失效。
		BodyCaptured: true,
	})
	if err != nil {
		f.allowed++
		return true
	}
	switch decision.Action {
	case requestfilter.ActionAllow:
		// 本地场景下游确认即时完成：放行即 Commit，锁住该形状的首条。
		if err := f.session.Commit(reservation); err != nil {
			_ = f.session.Release(reservation)
		}
		f.allowed++
		return true
	case requestfilter.ActionRetry:
		// 有未完成预留：等价于重复，丢弃（无可释放的预留）。
		f.dropped++
		return false
	default:
		// ActionDrop / ActionReject：重复或容量不足，均丢弃。
		f.dropped++
		return false
	}
}

// Close 释放会话资源。
func (f *similarFilter) Close() {
	if f.session != nil {
		f.session.Close()
	}
}

// Summary 输出一行统计到 stderr（silent 模式由调用方决定是否打印）。
func (f *similarFilter) Summary() {
	fmt.Fprintf(os.Stderr, "[fsu] 保留 %d / 过滤 %d（路径形状归并，阈值 %d）\n",
		f.allowed, f.dropped, pathSegmentThreshold)
}
