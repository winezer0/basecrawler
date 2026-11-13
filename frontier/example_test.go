package frontier

import (
	"fmt"
	"net/url"
)

// ExampleFrontier 演示外部调用方单独使用 URL 边界层：
// 规范化（相对路径按基准解析）、范围判定、去重。
//
// 这是 extractor 的天然搭档：extractor 吐原始字符串，
// frontier 决定「这个字符串是不是我的、要不要再见」。
func ExampleFrontier() {
	base, _ := url.Parse("https://docs.example.com/guide/")
	fr := New(Options{Scope: Subdomain})
	fr.AddSeed(base)

	// 站内相对路径：入队并拿到绝对地址
	inScope, ok1 := fr.Push("/api/orders", base)
	// 跨站地址：拒绝
	_, ok2 := fr.Push("https://other.example.org/x", base)
	// 已见地址：第二次拒绝
	_, ok3 := fr.Push("/api/orders", base)

	fmt.Println("in-scope:", inScope.Path, ok1)
	fmt.Println("cross-site accepted:", ok2)
	fmt.Println("duplicate accepted:", ok3)
	fmt.Println("InScope:", fr.InScope(inScope))
	// Output:
	// in-scope: /api/orders true
	// cross-site accepted: false
	// duplicate accepted: false
	// InScope: true
}

// ExampleNormalize 演示单发规范化：不依赖 Frontier 实例也能用。
func ExampleNormalize() {
	base, _ := url.Parse("https://a.example.com/x/y.html")
	for _, raw := range []string{"../z", "/abs", "https://b.com/p", "mailto:a@b.c"} {
		u, ok := Normalize(raw, base)
		if !ok {
			fmt.Printf("%-18s -> 拒绝\n", raw)
			continue
		}
		fmt.Printf("%-18s -> %s\n", raw, u.String())
	}
	// Output:
	// ../z               -> https://a.example.com/z
	// /abs               -> https://a.example.com/abs
	// https://b.com/p    -> https://b.com/p
	// mailto:a@b.c       -> 拒绝
}
