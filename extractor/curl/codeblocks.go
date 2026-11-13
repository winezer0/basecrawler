package curl

import (
	"strings"

	"golang.org/x/net/html"
)

// CurlCommand 是从文档代码块里解析出的 curl 命令。
// Raw 保留原始文本（保真）；其余字段是结构化视图，供测试请求生成使用。
type CurlCommand struct {
	Raw     string            `json:"raw"`
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// LooksLikeCurl 判定一段文本是否以 curl 命令开头。
// 容忍文档常见的 `$ curl ...` 提示符前缀。
func LooksLikeCurl(s string) bool {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "$")
	t = strings.TrimSpace(t)
	return t == "curl" || strings.HasPrefix(t, "curl ") || strings.HasPrefix(t, "curl\t")
}

// ParseCurl 把 curl 命令解析成结构化形式。纯函数。
//
// 覆盖文档场景的常用子集：续行（\ + 换行）、单双引号、-X/--request、
// -H/--header、-d/--data/--data-raw/--data-binary、无值开关（-s -k -L 等）。
// 未识别的 flag 按「消耗下一个 token」处理，避免把 flag 的值误当成 URL。
// URL 必须带 http(s)  scheme，否则拒绝（文档里也可能是伪代码）。
func ParseCurl(raw string) *CurlCommand {
	if !LooksLikeCurl(raw) {
		return nil
	}
	joined := strings.ReplaceAll(raw, "\\\n", " ")
	joined = strings.ReplaceAll(joined, "\\\r\n", " ")
	toks := splitArgs(joined)

	cmd := &CurlCommand{Raw: strings.TrimSpace(raw), Method: "GET", Headers: map[string]string{}}
	var bodies []string
	for i := 0; i < len(toks); i++ {
		tk := toks[i]
		if !strings.HasPrefix(tk, "-") || tk == "-" {
			// 位置参数：第一个 http(s) 地址是 URL，其余忽略
			// （文档里偶尔在 URL 后跟说明文字）
			if isHTTPURL(tk) && cmd.URL == "" {
				cmd.URL = tk
			}
			continue
		}
		// flag 归一化：-H / --header 两种写法
		name := tk
		switch name {
		case "-X", "--request":
			if v, ok := nextTok(toks, &i); ok {
				cmd.Method = strings.ToUpper(v)
			}
		case "-H", "--header":
			if v, ok := nextTok(toks, &i); ok {
				if k, val, found := strings.Cut(v, ":"); found {
					cmd.Headers[strings.TrimSpace(k)] = strings.TrimSpace(val)
				}
			}
		case "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode":
			if v, ok := nextTok(toks, &i); ok {
				bodies = append(bodies, v)
			}
		default:
			// 无值开关（-s -k -L -v --silent …）直接跳过；
			// 未知带值 flag 消费下一个 token，防止其值被当成 URL。
			if !isValuelessCurlFlag(name) {
				nextTok(toks, &i)
			}
		}
	}
	if cmd.URL == "" {
		return nil
	}
	if len(bodies) > 0 {
		cmd.Body = strings.Join(bodies, "&")
		if cmd.Method == "GET" {
			cmd.Method = "POST" // curl 语义：有 body 无 -X 时默认 POST
		}
	}
	return cmd
}

// CurlCommands 从 HTML 文档里提取所有 curl 命令（文档序、按 URL+body 去重）。
func CurlCommands(doc *html.Node) []CurlCommand {
	var out []CurlCommand
	seen := make(map[string]struct{}, 8)
	walkNode(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data != "code" && n.Data != "pre" {
			return
		}
		text := nodeText(n)
		if !LooksLikeCurl(text) {
			return
		}
		cmd := ParseCurl(text)
		if cmd == nil {
			return
		}
		key := cmd.Method + " " + cmd.URL + " " + cmd.Body
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, *cmd)
	})
	return out
}

func nextTok(toks []string, i *int) (string, bool) {
	if *i+1 >= len(toks) {
		return "", false
	}
	*i++
	return toks[*i], true
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// isValuelessCurlFlag 列出常见无值开关。未列出的按带值处理（保守）。
func isValuelessCurlFlag(name string) bool {
	switch name {
	case "-s", "--silent", "-k", "--insecure", "-L", "--location",
		"-v", "--verbose", "-i", "--include", "-g", "--globoff",
		"-compressed", "--no-keepalive", "-O", "--remote-name":
		return true
	}
	return false
}

// splitArgs 按 shell 规则拆分命令串：单/双引号成段、反斜杠转义下一个字符。
// 与 executor 已删除时的历史实现同语义；extractor 保持零包依赖，本地一份。
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	started := false
	var quote rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			if r == '"' {
				quote = 0
			} else if r == '\\' && i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			started = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

// nodeText 取节点的全部文本内容（含后代）。
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// walkNode 深度优先遍历文档树。
func walkNode(n *html.Node, fn func(*html.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(c, fn)
	}
}
