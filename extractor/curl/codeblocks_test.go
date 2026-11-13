package curl

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// 表驱动：ParseCurl 解析 OpenAI 文档风格的 curl 命令。
// 覆盖：续行、单双引号、-X/-H/-d、data-urlencode、无引号 data。
func TestParseCurl(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want *CurlCommand
	}{
		{
			name: "OpenAI chat completions（续行+单引号 body）",
			raw: `curl https://api.openai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'`,
			want: &CurlCommand{
				URL:    "https://api.openai.com/v1/chat/completions",
				Method: "POST",
				Headers: map[string]string{
					"Content-Type":  "application/json",
					"Authorization": "Bearer $OPENAI_API_KEY",
				},
				Body: "{\n    \"model\": \"gpt-4o-mini\",\n    \"messages\": [\n      {\"role\": \"user\", \"content\": \"Hello!\"}\n    ]\n  }",
			},
		},
		{
			name: "显式 -X GET",
			raw:  `curl -X GET "https://api.example.com/v1/models" -H "Authorization: Bearer sk-test"`,
			want: &CurlCommand{
				URL:     "https://api.example.com/v1/models",
				Method:  "GET",
				Headers: map[string]string{"Authorization": "Bearer sk-test"},
			},
		},
		{
			name: "无 -d 无 -X 时默认 GET",
			raw:  `curl https://api.example.com/v1/models`,
			want: &CurlCommand{URL: "https://api.example.com/v1/models", Method: "GET"},
		},
		{
			name: "-d 存在时默认 POST",
			raw:  `curl https://api.example.com/v1/echo -d '{"a":1}'`,
			want: &CurlCommand{
				URL:    "https://api.example.com/v1/echo",
				Method: "POST",
				Body:   `{"a":1}`,
			},
		},
		{
			name: "双引号 body",
			raw:  `curl https://api.example.com/v1/echo -d "{\"a\": 1}"`,
			want: &CurlCommand{
				URL:    "https://api.example.com/v1/echo",
				Method: "POST",
				Body:   `{"a": 1}`,
			},
		},
		{
			name: "--data 长选项与 --header 长选项",
			raw:  `curl --header "X-Trace: 1" --data 'k=v' https://api.example.com/v1/submit`,
			want: &CurlCommand{
				URL:     "https://api.example.com/v1/submit",
				Method:  "POST",
				Headers: map[string]string{"X-Trace": "1"},
				Body:    "k=v",
			},
		},
		{
			name: "-k 与 -s 等无值开关被忽略",
			raw:  `curl -s -k https://api.example.com/v1/ping`,
			want: &CurlCommand{URL: "https://api.example.com/v1/ping", Method: "GET"},
		},
		{
			name: "无 URL 拒绝",
			raw:  `curl -H "X: 1"`,
			want: nil,
		},
		{
			name: "非 curl 命令拒绝",
			raw:  `wget https://example.com/x`,
			want: nil,
		},
		{
			name: "空串拒绝",
			raw:  ``,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCurl(tc.raw)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("应拒绝, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("应解析, got nil")
			}
			if got.URL != tc.want.URL {
				t.Errorf("URL: got %q, want %q", got.URL, tc.want.URL)
			}
			if got.Method != tc.want.Method {
				t.Errorf("Method: got %q, want %q", got.Method, tc.want.Method)
			}
			if got.Body != tc.want.Body {
				t.Errorf("Body: got %q, want %q", got.Body, tc.want.Body)
			}
			if len(got.Headers) != len(tc.want.Headers) {
				t.Fatalf("Headers: got %v, want %v", got.Headers, tc.want.Headers)
			}
			for k, v := range tc.want.Headers {
				if got.Headers[k] != v {
					t.Errorf("Header %q: got %q, want %q", k, got.Headers[k], v)
				}
			}
		})
	}
}

func TestParseCurlLooksLike(t *testing.T) {
	if !LooksLikeCurl("  curl https://a.com/") {
		t.Error("应以空白开头的 curl 也算")
	}
	if LooksLikeCurl("curling stones") {
		t.Error("普通英文单词不算")
	}
}

func TestCurlCommandsFromHTML(t *testing.T) {
	// OpenAI 文档风格：pre>code 块 + 行内 code + 无关代码块
	doc, err := html.Parse(strings.NewReader(`<html><body>
		<pre><code>curl https://api.openai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "Hi"}]}'</code></pre>
		<p>inline <code>curl https://api.openai.com/v1/models -H "Authorization: Bearer $OPENAI_API_KEY"</code> example</p>
		<pre><code>import OpenAI from "openai";
const client = new OpenAI();</code></pre>
		<pre><code>curl https://api.openai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4o-mini"}'</code></pre>
	</body></html>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cmds := CurlCommands(doc)
	if len(cmds) != 3 {
		t.Fatalf("应提取 3 条（两条 completions body 不同不去重 + models）, got %d", len(cmds))
	}
	if cmds[0].URL != "https://api.openai.com/v1/chat/completions" || cmds[0].Method != "POST" {
		t.Errorf("第一条: %+v", cmds[0])
	}
	if cmds[1].URL != "https://api.openai.com/v1/models" || cmds[1].Method != "GET" {
		t.Errorf("第二条: %+v", cmds[1])
	}
	if cmds[2].Body != `{"model": "gpt-4o-mini"}` {
		t.Errorf("第三条 body: %q", cmds[2].Body)
	}
	// JS 代码块不得误判
	for _, c := range cmds {
		if strings.Contains(c.URL, "openai\"") {
			t.Errorf("JS 块被误提: %+v", c)
		}
	}
}
