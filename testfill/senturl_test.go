package testfill

import "testing"

// TestRequestSentURL：SentURL 必须与实际发送地址一致，尤其是填充后的
// query；确认结果落盘用的就是这个函数，不能与 SendRequests 出现两个口径。
func TestRequestSentURL(t *testing.T) {
	cases := []struct {
		name string
		req  TestRequest
		want string
	}{
		{
			name: "无 query 原样返回",
			req:  TestRequest{URL: "https://x/api/users"},
			want: "https://x/api/users",
		},
		{
			name: "合并填充参数",
			req:  TestRequest{URL: "https://x/api/users", Query: map[string]string{"id": "1", "q": "a b"}},
			want: "https://x/api/users?id=1&q=a+b",
		},
		{
			name: "同名参数覆盖而非追加",
			req:  TestRequest{URL: "https://x/api/users?id=0&page=2", Query: map[string]string{"id": "1"}},
			want: "https://x/api/users?id=1&page=2",
		},
		{
			name: "非法 URL 回退原文",
			req:  TestRequest{URL: "://bad", Query: map[string]string{"a": "b"}},
			want: "://bad",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.req.SentURL(); got != c.want {
				t.Errorf("SentURL() = %q，期望 %q", got, c.want)
			}
		})
	}
}
