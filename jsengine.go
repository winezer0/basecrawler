package basecrawler

import (
	"github.com/winezer0/basecrawler/extractor"
	"github.com/winezer0/basecrawler/jsext"
	"github.com/winezer0/basecrawler/jsext/goja"
	"github.com/winezer0/basecrawler/jsext/treesitter"
)

// JSEngine 选择 JS 提取引擎。
//
// 各实现的差异（实测）：
//   - regex      零依赖正则，最快，但拿不到拼接上下文、方法与参数
//   - goja       AST 解析，约 50 MB/s，支持 ES2020，**不支持 ESM 的 import/export**
//     及 import.meta、顶层 await；遇到即整文件解析失败
//   - treesitter 纯 Go tree-sitter，语法覆盖最全（含 ESM）且容错，
//     但慢约 1.8 倍、二进制多约 7.7MB
//
// 默认用 auto：goja 快路径优先，失败时用 treesitter 兜底。
// 这样既不牺牲常规脚本的速度，也不会在 Vite/ESM 产物上整文件放弃。
type JSEngine string

const (
	// JSEngineRegex 只用正则，不引入任何第三方依赖。
	JSEngineRegex JSEngine = "regex"
	// JSEngineAuto goja 优先，解析失败时回退 treesitter（默认）。
	JSEngineAuto JSEngine = "auto"
	// JSEngineGoja 强制只用 dop251/goja 的解析器。
	JSEngineGoja JSEngine = "goja"
	// JSEngineTreesitter 强制只用 odvcencio/gotreesitter。
	JSEngineTreesitter JSEngine = "treesitter"
)

// engineChain 按名称返回引擎链（按优先级排列），空链表示只用正则。
func engineChain(name JSEngine) []jsext.Engine {
	switch name {
	case JSEngineGoja:
		return []jsext.Engine{goja.Extract}
	case JSEngineTreesitter:
		return []jsext.Engine{treesitter.Extract}
	case JSEngineRegex:
		return nil
	default:
		// 含 JSEngineAuto 与零值：零值也要拿到默认能力，
		// 否则库使用者不显式设置时就退化成正则了。
		return []jsext.Engine{goja.Extract, treesitter.Extract}
	}
}

// adaptEngine 把引擎链适配成 extractor 需要的注入函数。
//
// 链上任何一个引擎解析成功即采用其结果；全部失败才返回 ok=false，
// 由调用方回退正则。串行的代价可以忽略：goja 遇到 ESM 在第一个 token 就报错。
//
// Q-2 修正：链中**非末位**引擎「解析成功但零产出」时继续下探——AST
// 引擎对压缩/非常规语法可能合法解析却提不出请求，此时下一个引擎
// 仍可能召回。末位引擎的空产出仍然算成功：那代表「解析通过、确实
// 没有可提取的地址」，此时回退正则会引入 AST 刻意排除的噪音
// （`var base="/api/v3"` 这类前缀碎片，见 TestVarLiteralNeedsEndpointShape）。
// 以获取到 URL 资源为准，但「已验证为空」与「没试过」必须区分。
func adaptEngine(chain []jsext.Engine) func(string) ([]extractor.Request, bool) {
	if len(chain) == 0 {
		return nil
	}
	return func(src string) ([]extractor.Request, bool) {
		data := []byte(src)
		for i, fn := range chain {
			reqs, err := fn(data)
			if err != nil {
				continue
			}
			if len(reqs) == 0 && i < len(chain)-1 {
				continue // 也许下一个引擎能提出来
			}
			return toRequests(reqs), true
		}
		return nil, false
	}
}

// toRequests 把 jsext 的输出适配成 extractor.Request。
func toRequests(reqs []jsext.Request) []extractor.Request {
	out := make([]extractor.Request, 0, len(reqs))
	for _, r := range reqs {
		// 参数名单独带出，绝不改写 URL。
		//
		// 早期实现会把参数名补成 `?page=&size=`，等于凭空造出一个源码里不存在的地址：
		// 严格校验的接口会 400，分页接口可能把空值当第一页返回，结果与真实调用不一致。
		// 参数名只放 Params，由调用方决定如何使用。
		params := append([]string(nil), r.QueryParams...)
		for _, b := range r.BodyParams {
			dup := false
			for _, q := range params {
				if q == b {
					dup = true
					break
				}
			}
			if !dup {
				params = append(params, b)
			}
		}
		out = append(out, extractor.Request{
			Raw:        r.URL,
			Method:     r.Method,
			Source:     "js-ast",
			Confidence: r.Confidence,
			Params:     params,
		})
	}
	return out
}
