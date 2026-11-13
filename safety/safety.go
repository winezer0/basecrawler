// Package safety 是危险操作拦截的公共工具包，供 basecrawler、prcrawler
// 及同生态的其他爬虫共用同一份词表与判定逻辑。
//
// 本包零第三方依赖、不认识「爬虫」也不发网络请求，只回答「这条动作是否
// 危险」。调用方自行决定命中后是不入队、不提交表单，还是不发送测试请求；
// 设计目标是「发现不受损」：拦截只影响是否真实触发，不影响发现与结果输出。
//
// 公共 API 面（保持向后兼容，新增字段/函数只做 additive 变更）：
//
//	NewSafety(enabled, extraURL, extraForm)            基础入口，prcrawler 用
//	NewSafetyWithActions(enabled, url, form, action)   完整入口，basecrawler 用
//	(*Safety).Enabled / SkipURL / SkipForm / SkipRequest
//	IsSafeMethod(method)                               幂等方法判定
//
// 两种调用范式：
//   - 浏览器爬虫（prcrawler）：在链接入队前调 SkipURL、表单提交前调
//     SkipForm(action, 字段名+ID)，命中则记录发现但不真实触发。
//   - 静态爬虫（basecrawler）：在 testfill 发送测试请求前调
//     SkipRequest(method, url, fields)，它串联三类词表并返回拦截原因。
//
// 词表只收「明确改变目标状态」的操作类词；password 单独不触发
// （普通登录表单含 password），必须命中改密 / 删除语义。动作词表只在
// 非幂等方法上生效，避免 GET 查询接口因路径里带 delete 字样被误拦。
package safety

import (
	"fmt"
	"regexp"
	"strings"
)

// 内置词表（(?i) 大小写不敏感，中英双语）。
const (
	defaultURLSkip  = `logout|signout|sign-out|sign_out|log-out|\bexit\b|\bquit\b|注销|登出|退出登录`
	defaultFormSkip = `password_new|password_conf|password_confirm|password_current|` +
		`old_?pass(word)?|new_?pass(word)?|curr_?pass(word)?|pass_?conf|` +
		`修改密码|更改密码|重置密码|` +
		`delete[-_]?(user|account|post|article)|remove[-_]?(user|account)|` +
		`删除用户|删除账号|删除文章|reset_?password`
	// 动作词表只在非幂等方法（POST/PUT/PATCH/DELETE）上生效，
	// 避免 GET 查询接口因路径里带 delete 字样被误拦。
	defaultActionSkip = `delete|destroy|purge|erase|remove|drop|truncate|` +
		`删除|移除|清空|销毁|注销`
)

// safeMethods 是不改变对端状态的幂等方法。
var safeMethods = map[string]bool{
	"GET": true, "HEAD": true, "OPTIONS": true,
}

// IsSafeMethod 报告某个 HTTP 方法是否为幂等方法（GET/HEAD/OPTIONS）。
// 供调用方复用同一张表：既用于「非幂等是否允许发送」，也用于「动作词表
// 是否参与判定」，避免两个项目各维护一份方法表。
func IsSafeMethod(method string) bool {
	return safeMethods[strings.ToUpper(strings.TrimSpace(method))]
}

// Safety 是危险操作判定器。enabled=false 时全部放行。
type Safety struct {
	enabled    bool
	urlSkip    []*regexp.Regexp
	formSkip   []*regexp.Regexp
	actionSkip []*regexp.Regexp
}

// NewSafety 构造判定器：内置词表 + extraURL/extraForm 追加正则（叠加不覆盖）。
// 签名与 prcrawler 的 safety 保持一致，便于后续直接复用。
func NewSafety(enabled bool, extraURL, extraForm []string) (*Safety, error) {
	return NewSafetyWithActions(enabled, extraURL, extraForm, nil)
}

// NewSafetyWithActions 是完整构造入口：在 NewSafety 的三类默认词表之外，
// 允许追加「非幂等方法动作词」规则（extraAction）。四组 extra 都是叠加，
// 不覆盖内置词表；非法正则返回错误（启动前失败，不留半防护状态）。
// extraAction 只对 SkipRequest 生效，SkipURL / SkipForm 不受影响。
func NewSafetyWithActions(enabled bool, extraURL, extraForm, extraAction []string) (*Safety, error) {
	s := &Safety{enabled: enabled}
	if !enabled {
		return s, nil
	}
	compile := func(label string, defaults string, extra []string) ([]*regexp.Regexp, error) {
		var out []*regexp.Regexp
		for i, pat := range append([]string{defaults}, extra...) {
			rx, err := regexp.Compile(`(?i)` + pat)
			if err != nil {
				return nil, fmt.Errorf("safety %s 规则 #%d 非法: %w", label, i, err)
			}
			out = append(out, rx)
		}
		return out, nil
	}
	var err error
	if s.urlSkip, err = compile("url", defaultURLSkip, extraURL); err != nil {
		return nil, err
	}
	if s.formSkip, err = compile("form", defaultFormSkip, extraForm); err != nil {
		return nil, err
	}
	if s.actionSkip, err = compile("action", defaultActionSkip, extraAction); err != nil {
		return nil, err
	}
	return s, nil
}

// Enabled 返回防护是否开启。
func (s *Safety) Enabled() bool { return s != nil && s.enabled }

// SkipURL 判定链接是否危险（登出类）：命中则记录发现但不真实导航。
func (s *Safety) SkipURL(raw string) bool {
	if !s.Enabled() {
		return false
	}
	for _, rx := range s.urlSkip {
		if rx.MatchString(raw) {
			return true
		}
	}
	return false
}

// SkipForm 判定表单是否危险（改密码 / 删除类）：action + 全部字段名/ID 拼成
// 联合串后匹配——改密码表单的 action 可能只是 /account，要看字段语义。
func (s *Safety) SkipForm(action string, fields []string) bool {
	if !s.Enabled() {
		return false
	}
	blob := action + "\x00" + strings.Join(fields, "\x00")
	for _, rx := range s.formSkip {
		if rx.MatchString(blob) {
			return true
		}
	}
	return false
}

// SkipRequest 判定一条测试请求是否应被拦截，返回是否拦截与原因。
// 顺序：URL 词表 → 表单词表 → 非幂等方法的动作词表。
func (s *Safety) SkipRequest(method, rawURL string, fields []string) (bool, string) {
	if !s.Enabled() {
		return false, ""
	}
	if s.SkipURL(rawURL) {
		return true, "URL 命中登出/退出类词表"
	}
	blob := rawURL + "\x00" + strings.Join(fields, "\x00")
	for _, rx := range s.formSkip {
		if rx.MatchString(blob) {
			return true, "表单命中改密/删除类词表"
		}
	}
	if !IsSafeMethod(method) {
		for _, rx := range s.actionSkip {
			if rx.MatchString(blob) {
				return true, "非幂等方法命中动作词表"
			}
		}
	}
	return false, ""
}
