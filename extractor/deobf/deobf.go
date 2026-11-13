// Package deobf 提供「检测混淆 + 轻量反混淆」，服务于 API 发现：
//
// 目标不是还原可读源码，而是把藏在 eval 字符串参数、packer 打包串
// 里的 URL/路径字面量释放出来，交给下游常规字面量提取（regex/AST）。
// string-array 解码器需要沙箱执行任意解码逻辑，由 SandboxRecover
// 提供（见 sandbox.go）：核心层默认开启，但只对命中 string-array
// 专属签名的源码执行，--no-deobf-exec 可关闭。
package deobf

import (
	"regexp"
	"strconv"
	"strings"
)

// obfSignatures 是按混淆器族分的特征；命中任一即判定已混淆。
var obfSignatures = []*regexp.Regexp{
	// Dean Edwards packer：eval(function(p,a,c,k,e,d){...}('...',62,1,'...'.split('|')))
	regexp.MustCompile(`eval\(function\(p,a,c,k,e,(?:d|r)\)`),
	// 十六进制标识符（javascript-obfuscator 默认命名）
	regexp.MustCompile(`(?:var|let|const|function)\s+_0x[0-9a-fA-F]{4,}`),
	// 字符串数组 + 位移解码的典型骨架
	regexp.MustCompile(`var\s+_0x[0-9a-fA-F]+\s*=\s*\[`),
	// JJEncode / AAEncode
	regexp.MustCompile(`(-~\[\]|\$\=~\[\])`),
	// eval 直接收字符串字面量：动态分析的核心信号。普通产物极少
	// 这样写，误报成本只是多跑一遍无副作用的 Recover。
	regexp.MustCompile(`eval\(\s*["']`),
}

// LooksObfuscated 判断一段 JS 是否已混淆。
//
// 纯签名匹配，零解析成本——判断本身要在每个脚本上跑一遍。
// 只判「是否值得做反混淆」，精确性交给 Recover 的实际解包。
func LooksObfuscated(src string) bool {
	if len(src) < 32 {
		return false
	}
	for _, re := range obfSignatures {
		if re.MatchString(src) {
			return true
		}
	}
	return false
}

// packerRe 匹配 Dean Edwards packer 调用整体。
// 分组：1=payload 2=radix 3=count 4=字典（FindStringSubmatchIndex 下标 2/4/6/8）。
var packerRe = regexp.MustCompile(
	`eval\(function\(p,a,c,k,e,(?:d|r)\)\{[\s\S]{10,1000}?\}\('((?:[^'\\]|\\.)*)',(\d+),(\d+),'((?:[^'\\]|\\.)*)'\.split\('\|'\)\)\)`)

// Recover 返回反混淆后的源码与是否发生了变换。
//
// 步骤：packer 解包（最外层先解，结果里的嵌套 eval 由 unwrapEval 继续处理）
// → eval("字符串") 解包 → JS 字符串转义解码。
// 未混淆的输入会原样返回（ok=false），调用方无需判断重复提取。
func Recover(src string) (string, bool) {
	changed := false
	if m := packerRe.FindStringSubmatchIndex(src); m != nil {
		if out, ok := unpackPacker(src, m); ok {
			src = out
			changed = true
		}
	}
	if out, ok := unwrapEval(src); ok {
		src = out
		changed = true
	}
	return src, changed
}

// unpackPacker 实现 Dean Edwards packer 还原：payload 里以 a 进制编码的
// token（\b 词边界）替换回字典词。字典第 i 项为空表示保留原编码词。
func unpackPacker(src string, m []int) (string, bool) {
	payload := unescapePackerString(src[m[2]:m[3]])
	radix, _ := strconv.Atoi(src[m[4]:m[5]])
	count, _ := strconv.Atoi(src[m[6]:m[7]])
	dict := strings.Split(src[m[8]:m[9]], "|")
	if radix < 2 || radix > 62 || count <= 0 || len(dict) == 0 {
		return src, false
	}
	// count 来自目标源码：字典外的编号全是无效循环，直接钳到字典长度。
	if count > len(dict) {
		count = len(dict)
	}
	if count > maxPackerCount {
		count = maxPackerCount
	}
	if len(payload) > maxRecoverBytes {
		return src, false
	}
	out := payload
	// 从高位往低位替换：先换 "12" 再换 "1"，避免前缀词先吃掉长词的词边界。
	for i := count - 1; i >= 0; i-- {
		word := encodeWord(i, radix)
		if word == "" || i >= len(dict) || dict[i] == "" {
			continue
		}
		re, err := regexp.Compile(`\b` + regexp.QuoteMeta(word) + `\b`)
		if err != nil {
			continue
		}
		out = re.ReplaceAllLiteralString(out, dict[i])
		if len(out) > maxPackerExpandBytes {
			// 保留已展开的部分，不再继续放大（结果不完整，但避免炸弹）。
			break
		}
	}
	return out, true
}

// encodeWord 把整数编码为指定进制的词（packer 的 e(c) 约定：c.toString(a)）。
func encodeWord(n, radix int) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if n == 0 {
		return "0"
	}
	var b strings.Builder
	for n > 0 {
		b.WriteByte(alphabet[n%radix])
		n /= radix
	}
	// 循环是从低位往高位收集的，翻转后才是正常书写序。
	r := []byte(b.String())
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// unescapePackerString 处理 payload 字符串里的 \' 转义。
func unescapePackerString(s string) string {
	if !strings.Contains(s, `\'`) {
		return s
	}
	return strings.ReplaceAll(s, `\'`, "'")
}

// evalStringRe 按 RE2 无反向引用的约束拆成单双引号两版；
// 串内容许转义序列与异类引号，不容许未转义的同类引号。
var (
	evalDQRe = regexp.MustCompile(`eval\("((?:[^"\\]|\\.)+?)"\)`)
	evalSQRe = regexp.MustCompile(`eval\('((?:[^'\\]|\\.)+?)'\)`)
)

// unwrapEval 把 eval("代码字符串") 的参数解转义后原地展开。
//
// 解出的代码里若还有 eval 套娃，靠 Do 的多轮循环处理。
func unwrapEval(src string) (string, bool) {
	changed := false
	out := src
	expansions := 0
	total := len(src)
	for _, re := range []*regexp.Regexp{evalDQRe, evalSQRe} {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			if expansions >= maxEvalExpansions {
				return m
			}
			sub := re.FindStringSubmatch(m)
			code := unescapeJS(sub[1])
			if !strings.ContainsAny(code, `."(=`) {
				return m // 解出来不是代码，保持原样
			}
			if total+len(code) > maxRecoverBytes {
				return m // 展开总量触顶，停止放大
			}
			total += len(code)
			expansions++
			changed = true
			return code
		})
	}
	return out, changed
}

// unescapeJS 解 JS 字符串转义：\xNN、\uNNNN、常见控制转义。
// 解不出来原样返回——反混淆是尽力而为，不是必须完整。
func unescapeJS(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'x':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteString(`\x`)
		case 'u':
			if i+4 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(v))
					i += 4
					continue
				}
			}
			b.WriteString(`\u`)
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case '"', '\'':
			b.WriteByte(s[i]) // \" \' 解成裸引号
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// 反混淆工作量预算：输入来自目标站，不能让 packer 的 count 或嵌套 eval
// 把纯 Go 路径拖进无上限的 CPU / 内存消耗。
const (
	maxRecoverRounds     = 3       // 嵌套 eval 展开轮数
	maxPackerCount       = 4096    // packer 循环上限
	maxPackerExpandBytes = 1 << 20 // 单次 packer 展开后的字节上限
	maxEvalExpansions    = 256     // 单轮 eval 字符串展开个数
	maxRecoverBytes      = 2 << 20 // 反混淆后源码总长上限
)

// Do 检测 + 反混淆的完整入口：未混淆返回原串（ok=false）；
// 混淆则最多做 maxRecoverRounds 轮展开。
func Do(src string, force bool) (string, bool) {
	if !force && !LooksObfuscated(src) {
		return src, false
	}
	out := src
	changed := false
	for i := 0; i < maxRecoverRounds; i++ {
		if len(out) > maxRecoverBytes {
			break
		}
		t, ok := Recover(out)
		if !ok || t == out {
			break
		}
		out = t
		changed = true
	}
	return out, changed
}
