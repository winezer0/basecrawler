// helpers.go —— 各子解析器共用的小工具函数。
//
// 这批函数都是纯字符串/纯 DOM 操作，无状态、无 IO：
//   - PathShaped：地址形态判定（carriers / modulemap 共用）；
//   - AttrVal：HTML 属性读取（extractor / form / modulemap 共用）；
//   - IsStaticAsset：静态资源后缀判定（detect / inlinejson / json 共用）；
//   - JSONURL：JSON 值里的站点地址判定（json / inlinejson 共用）；
//   - UnescapeJSString：JS 字符串字面量转义还原（nextjs / inlinejson 共用）。
package base

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// PathShaped 判断字符串是否具备地址形态。
//
// 只接受有明确起点的写法（`/`、`./`、`../`、`//`、`http(s)://`）。
// 裸相对路径（`img/a.png`）故意不收：data-* 里这类值多半是配置键或文案，
// 收益远小于误报成本。
func PathShaped(s string) bool {
	if s == "" || len(s) > 2048 {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n<>") {
		return false
	}
	switch {
	case strings.HasPrefix(s, "//"),
		strings.HasPrefix(s, "/"),
		strings.HasPrefix(s, "./"),
		strings.HasPrefix(s, "../"),
		strings.HasPrefix(s, "http://"),
		strings.HasPrefix(s, "https://"):
		return true
	}
	return false
}

// AttrVal 取属性值，兼容大小写与 xlink 命名空间。
func AttrVal(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
		// xlink:href 在 x/net/html 中表现为 Namespace=xlink, Key=href
		if a.Namespace == "xlink" && strings.EqualFold(key, "href") && strings.EqualFold(a.Key, "href") {
			return a.Val, true
		}
	}
	return "", false
}

// staticAssetRe 识别「大概率不是接口」的静态资源。
// 资产收集场景下这些 URL 本身仍有价值（可能泄露信息），
// 这里只用于避免 JS/JSON 提取把大量图片路径灌进结果。
var staticAssetRe = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|svg|ico|bmp|tiff|woff2?|ttf|otf|eot|mp4|webm|avi|mov|mp3|wav|ogg|zip|rar|7z|gz|tar|pdf|docx?|xlsx?|pptx?|exe|dll|apk|ipa)$`)

// IsStaticAsset 判断原始地址是否指向静态资源（忽略 query）。
func IsStaticAsset(raw string) bool {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return staticAssetRe.MatchString(raw)
}

// JSONURL 判断字符串是否像站点内的地址。
func JSONURL(s string) bool {
	if len(s) < 2 || len(s) > 2048 {
		return false
	}
	if strings.ContainsAny(s, " \t\n\r\"<>") {
		return false
	}
	switch {
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		return true
	case strings.HasPrefix(s, "/"):
		// 排除 "#"、"//" 这类协议相对或纯锚点
		return len(s) > 1 && s[1] != '/' && s[1] != '#'
	}
	return false
}

// UnescapeJSString 还原 JS 字符串字面量的转义。
//
// 不直接用 strconv.Unquote：JS 允许 `\/` 这种 Go 不认的转义，
// 混进一个就会让整段解析失败，而 payload 里 URL 前缀大量出现 `\/`。
func UnescapeJSString(s string) (string, bool) {
	if !strings.ContainsRune(s, '\\') {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", false
		}
		switch s[i] {
		case '"', '\'', '\\', '/':
			b.WriteByte(s[i])
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b', 'f', 'v', '0':
			// 对这几种控制符我们不关心内容，占位成空格避免粘连
			b.WriteByte(' ')
		case 'u':
			if i+4 >= len(s) {
				return "", false
			}
			r, err := strconv.ParseUint(s[i+1:i+5], 16, 32)
			if err != nil {
				return "", false
			}
			b.WriteRune(rune(r))
			i += 4
		default:
			// 未知转义按字面保留
			b.WriteByte(s[i])
		}
	}
	return b.String(), true
}
