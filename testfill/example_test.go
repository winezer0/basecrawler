package testfill

import (
	"fmt"

	"github.com/winezer0/basecrawler"
)

// ExampleGenerateRequests 演示完整链路：采集结果（含 curl 代码块与
// 普通接口）→ 填充测试值 → 渲染成可执行脚本。
func ExampleGenerateRequests() {
	results := []basecrawler.Result{
		{
			URL: "https://api.openai.com/v1/chat/completions", Method: "POST",
			Kind: "curl", StatusCode: 0,
			Raw: `curl https://api.openai.com/v1/chat/completions -H "Content-Type: application/json" -H "Authorization: Bearer $OPENAI_API_KEY" -d '{"model": "gpt-4o-mini", "temperature": 0.7}'`,
		},
		{
			URL: "https://target.example.com/api/users/:id/orders", Method: "GET",
			StatusCode: 200, Params: []string{"status", "page"},
		},
	}
	reqs := GenerateRequests(results, Config{
		AuthHeader: "Authorization: Bearer ${TOKEN}",
	})
	script, err := Render(reqs, "curl")
	if err != nil {
		fmt.Println("render:", err)
		return
	}
	fmt.Print(script)
	// Output:
	// #!/bin/sh
	// # basecrawler 测试请求（凭证为占位符，执行前替换）
	//
	// # body 已按 key 填充测试值
	// # 头含 $VAR，执行前需设置环境变量
	// curl -X POST 'https://api.openai.com/v1/chat/completions' -H 'Authorization: Bearer ${TOKEN}' -H 'Content-Type: application/json' -d '{"model":"sample-1","temperature":0.7}'
	//
	// # 查询参数按参数名推断填充
	// curl -X GET 'https://target.example.com/api/users/1/orders?page=2&status=sample-1' -H 'Authorization: Bearer ${TOKEN}'
}

// ExampleFillTemplate 演示 URL 模板填充：`:id`/`{id}`/`${id}` 三种占位符。
func ExampleFillTemplate() {
	for _, raw := range []string{
		"/api/users/:id",
		"/api/orders/{orderId}",
		"/api/users/${userId}/items",
	} {
		// 每条独立源：同 seed 必然同输出
		fmt.Println(raw, "->", FillTemplate(raw, NewSeededSource(1)))
	}
	// Output:
	// /api/users/:id -> /api/users/1
	// /api/orders/{orderId} -> /api/orders/1
	// /api/users/${userId}/items -> /api/users/1/items
}
