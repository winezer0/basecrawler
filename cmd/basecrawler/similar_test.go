package main

import "testing"

// TestSimilarFilterCollapsesPathIDs 验证同形状动态段被归并。
func TestSimilarFilterCollapsesPathIDs(t *testing.T) {
	f, err := newSimilarFilter()
	if err != nil {
		t.Fatalf("newSimilarFilter: %v", err)
	}
	defer f.Close()

	// 未达泛化阈值前全部放行
	for i := 0; i < pathSegmentThreshold; i++ {
		if !f.Allow("https://x/users/" + itoa(i)) {
			t.Fatalf("第 %d 个未达阈值不应被过滤", i)
		}
	}
	// 达阈值后位置折叠为 {dyn}，同位置新值立即被覆盖过滤
	if f.Allow("https://x/users/999999") {
		t.Error("泛化后同形状 URL 应被过滤")
	}
	if f.dropped == 0 {
		t.Error("应产生丢弃计数")
	}
	// 不同路径形状不受影响
	if !f.Allow("https://x/orders/42") {
		t.Error("不同路径形状应放行")
	}
}

// TestSimilarFilterFailOpen 验证非法 URL fail-open 放行。
func TestSimilarFilterFailOpen(t *testing.T) {
	f, err := newSimilarFilter()
	if err != nil {
		t.Fatalf("newSimilarFilter: %v", err)
	}
	defer f.Close()
	if !f.Allow("://bad url") {
		t.Error("非法 URL 应 fail-open 放行")
	}
	// 非法 URL 不产生丢弃（fail-open 语义）
	if f.dropped != 0 {
		t.Errorf("非法 URL 不应计入丢弃，dropped=%d", f.dropped)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
