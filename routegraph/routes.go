package routegraph

import "strings"

// Route 是从前端路由配置里恢复出来的一条路由。
//
// 注意：前端路由不等于服务端资源。SPA 的 /admin/users 在服务器上
// 往往只是同一个 index.html，所以调用方应把 Kind=route 的结果
// 与真实抓取到的 URL 区分对待。
type Route struct {
	// Path 路由路径，可能含 :id 这类参数占位。
	Path string
	// Name 路由名（存在时才有）。
	Name string
	// Component 指向的组件名或模块路径（存在时才有）。
	Component string
	// Method 路由/接口表声明的 HTTP 方法（存在且合法时才有，如 POST）。
	// 后端接口表 {method:'post', path:'/api/x'} 会命中；纯前端路由无此键，
	// 调用方可据 Method 判空回退 GET 语义。
	Method string
	// Source 命中的识别点，如 route-table / route-alias / exec。
	Source string
}

// keyClass 是键的归类。只区分「需要保存的值」与「能证明这是路由对象的兄弟键」。
type keyClass int

const (
	keyOther keyClass = iota
	keyPath
	keyName
	keyComponent
	keyMethod
	keySibling
	keyChildren
	keyAlias
)

// classifyKey 把 `:` 前的 token 归类。
// 只认 path 会把普通配置对象也当成路由，加上兄弟键能大幅收敛。
func classifyKey(t Token, src []byte) keyClass {
	switch strings.ToLower(strings.TrimSpace(t.Value(src))) {
	case "path", "url", "endpoint":
		return keyPath
	case "name":
		return keyName
	case "component", "element", "lazy":
		return keyComponent
	case "method":
		return keyMethod
	case "children":
		return keyChildren
	case "alias":
		return keyAlias
	case "components", "redirect", "meta", "loader",
		"handler", "controller", "middleware", "auth", "schema", "template",
		"loadchildren", "canactivate", "routconfig":
		return keySibling
	}
	return keyOther
}

// isHTTPVerb 判断小写后的字符串是否是合法 HTTP 方法名。
func isHTTPVerb(s string) bool {
	switch s {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return true
	}
	return false
}

// routeFrame 是一次括号扫描收集到的对象帧。
//
// 与旧版「见到 } 立刻算路径」不同：这里只记录结构与父子关系，
// 全部 token 扫完后再递归算路径。这样 children 写在 path 之前、
// 三层以上嵌套、或父对象还没解析完时都不会丢前缀。
type routeFrame struct {
	hasPath    bool
	path       string
	hasSibling bool
	name       string
	component  string
	method     string
	aliases    []string
	// parent 是词法上的外层对象，routeParent 是 children 直接元素对应的
	// 父路由对象。只有后者参与路径拼接，避免 redirect/meta 的值对象污染。
	parent       int
	routeParent  int
	isRouteChild bool
}

const (
	maxRouteFrames     = 4096
	maxAliasesPerRoute = 8
)

// childCtx 描述「当前处于某个 children 数组的直接元素层」。
// stackDepth 用来区分数组元素对象与对象内部的嵌套对象/数组。
type childCtx struct {
	owner      int
	stackDepth int
	arrayDepth int
}

// routeTables 用括号栈扫出所有路由对象，再按父子关系递归算真实路径。
//
// 不用 AST 的原因：这里要的是「结构形状」而不是语法树，
// 栈扫描对压缩、嵌套、以及 ESM/CJS 两种产物都一致，实现也只要几十行。
// 分两阶段是为了消除键顺序依赖：旧版在 `}` 时立即取父帧当时的 path，
// `{children:[{path:"users"}],path:"/admin"}` 这种写法会漏拼。
func (tl *tokenList) routeTables() []Route {
	frames := make([]routeFrame, 0, 64)
	stack := make([]int, 0, 64)
	var pending keyClass
	var prev Token
	hasPrev := false
	// children 数组可以逐层嵌套，所以用栈而不是单个上下文：
	// 内层数组关闭后，外层数组剩下的元素仍要能被识别。
	var ctxStack []childCtx
	aliasArray := false
	pendingChildObj := false // children: { ... } 单对象写法

	for i := 0; i < len(tl.toks); i++ {
		t := tl.toks[i]
		if t.Kind == TokPunct {
			switch {
			case tl.is(i, "{"):
				if pending == keyChildren {
					pendingChildObj = true
				}
				if len(frames) < maxRouteFrames {
					f := routeFrame{parent: -1, routeParent: -1}
					if n := len(stack); n > 0 {
						f.parent = stack[n-1]
					}
					switch {
					case pendingChildObj:
						f.isRouteChild = true
						if n := len(stack); n > 0 {
							f.routeParent = stack[n-1]
						}
						pendingChildObj = false
					case len(ctxStack) > 0:
						top := ctxStack[len(ctxStack)-1]
						if top.arrayDepth == 1 && len(stack) == top.stackDepth {
							f.isRouteChild = true
							f.routeParent = top.owner
						}
					}
					frames = append(frames, f)
					stack = append(stack, len(frames)-1)
				}
				pending = keyOther
			case tl.is(i, "}"):
				if n := len(stack); n > 0 {
					stack = stack[:n-1]
				}
				pending = keyOther
				aliasArray = false
			case tl.is(i, "["):
				switch {
				case pending == keyChildren && len(stack) > 0:
					ctxStack = append(ctxStack, childCtx{
						owner:      stack[len(stack)-1],
						stackDepth: len(stack),
						arrayDepth: 1,
					})
					pending = keyOther
				case pending == keyAlias:
					aliasArray = true
				case len(ctxStack) > 0:
					top := &ctxStack[len(ctxStack)-1]
					if len(stack) == top.stackDepth {
						// children 数组里的嵌套数组，不当作直接元素层。
						top.arrayDepth++
					}
				}
			case tl.is(i, "]"):
				if len(ctxStack) > 0 {
					top := &ctxStack[len(ctxStack)-1]
					if len(stack) == top.stackDepth {
						top.arrayDepth--
						if top.arrayDepth <= 0 {
							ctxStack = ctxStack[:len(ctxStack)-1]
						}
					}
				}
				aliasArray = false
				pending = keyOther
			case tl.is(i, ":"):
				if len(stack) > 0 && hasPrev {
					pending = classifyKey(prev, tl.src)
					id := stack[len(stack)-1]
					switch pending {
					case keySibling:
						frames[id].hasSibling = true
						pending = keyOther
					case keyChildren, keyAlias:
						// 值可能是数组/对象（children）或字符串数组（alias），
						// 必须保留 pending 等下一个 punct/字符串 token。
						frames[id].hasSibling = true
					case keyComponent:
						frames[id].hasSibling = true
					}
				}
			}
			prev, hasPrev = t, true
			continue
		}

		if len(stack) > 0 {
			id := stack[len(stack)-1]
			f := &frames[id]
			// alias: ["/a","/b"] —— 数组里的字符串都是别名。
			if aliasArray {
				if (t.Kind == TokString || t.Kind == TokTemplate) && len(f.aliases) < maxAliasesPerRoute {
					f.aliases = append(f.aliases, t.Value(tl.src))
				}
				prev, hasPrev = t, true
				continue
			}
			if pending != keyOther {
				var val string
				ok := false
				switch t.Kind {
				case TokString, TokTemplate:
					val, ok = t.Value(tl.src), true
				case TokIdent:
					if pending == keyComponent || pending == keyName || pending == keyMethod {
						val, ok = t.Text(tl.src), true
					}
				}
				if ok {
					switch pending {
					case keyPath:
						f.hasPath, f.path = true, val
					case keyName:
						f.name = val
					case keyComponent:
						f.hasSibling = true
						if f.component == "" {
							f.component = val
						}
					case keyMethod:
						if f.method == "" {
							f.method = val
						}
					case keySibling:
						f.hasSibling = true
					case keyChildren:
						// children: someArray（标识符）——静态无法关联父子，
						// 只把它当路由兄弟键。
						f.hasSibling = true
					case keyAlias:
						f.hasSibling = true
						if len(f.aliases) < maxAliasesPerRoute {
							f.aliases = append(f.aliases, val)
						}
					}
					pending = keyOther
				}
			}
		}
		prev, hasPrev = t, true
	}

	// 第二阶段：递归算路径。memo 让每个帧只算一次；visiting 防环。
	memo := make(map[int]string, len(frames))
	visiting := make(map[int]bool, 8)
	seen := make(map[string]struct{}, len(frames))
	var out []Route

	emit := func(p string, f routeFrame, source string) {
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		m := strings.ToUpper(f.method)
		if m != "" && !isHTTPVerb(strings.ToLower(m)) {
			m = ""
		}
		out = append(out, Route{
			Path:      p,
			Name:      f.name,
			Component: f.component,
			Method:    m,
			Source:    source,
		})
	}

	for id := range frames {
		f := frames[id]
		if !f.hasPath || !f.hasSibling {
			continue
		}
		p := resolveFramePath(frames, id, memo, visiting)
		if validRoutePath(p) {
			emit(p, f, "route-table")
		}
		// alias 与当前路由同级：绝对值直接用，相对值拼父前缀。
		for _, a := range f.aliases {
			ap := resolveAliasPath(frames, id, a, memo, visiting)
			if validRoutePath(ap) {
				emit(ap, f, "route-alias")
			}
		}
	}
	return out
}

// ancestorPrefix 沿 routeParent 链找最近一个带有效 path 的祖先路由，
// 自动跳过无 path 的布局包装层。
func ancestorPrefix(frames []routeFrame, id int, memo map[int]string, visiting map[int]bool) string {
	cur := frames[id].routeParent
	for cur >= 0 && cur < len(frames) {
		if frames[cur].hasPath && frames[cur].path != "" {
			return resolveFramePath(frames, cur, memo, visiting)
		}
		cur = frames[cur].routeParent
	}
	return ""
}

// resolveFramePath 算某个路由对象的最终可达路径。
//
// 规则与 vue-router / react-router 一致：以 / 或 * 开头视为绝对不拼父；
// 相对 path 拼最近祖先路由的绝对前缀；空 path 的默认子路由继承父地址。
func resolveFramePath(frames []routeFrame, id int, memo map[int]string, visiting map[int]bool) string {
	if p, ok := memo[id]; ok {
		return p
	}
	if visiting[id] {
		return ""
	}
	visiting[id] = true
	defer delete(visiting, id)

	f := frames[id]
	var p string
	switch {
	case !f.hasPath:
		p = ancestorPrefix(frames, id, memo, visiting)
	case f.path == "":
		p = ancestorPrefix(frames, id, memo, visiting)
	case strings.HasPrefix(f.path, "/") || strings.HasPrefix(f.path, "*"):
		p = f.path
	default:
		base := ""
		if f.isRouteChild {
			base = ancestorPrefix(frames, id, memo, visiting)
		}
		p = joinRoutePath(base, f.path)
	}
	memo[id] = p
	return p
}

// resolveAliasPath 算 alias：绝对别名不拼，相对别名按父路由前缀解析。
func resolveAliasPath(frames []routeFrame, id int, alias string, memo map[int]string, visiting map[int]bool) string {
	if alias == "" {
		return ""
	}
	if strings.HasPrefix(alias, "/") || strings.HasPrefix(alias, "*") {
		return alias
	}
	return joinRoutePath(ancestorPrefix(frames, id, memo, visiting), alias)
}

func joinRoutePath(base, raw string) string {
	p := "/" + strings.TrimPrefix(raw, "/")
	if base == "" || base == "/" || strings.HasPrefix(base, "*") {
		return p
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(raw, "/")
}

// validRoutePath 过滤掉明显不是路由的字符串。
func validRoutePath(p string) bool {
	if len(p) == 0 || len(p) > 200 {
		return false
	}
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "*") {
		return false
	}
	if strings.ContainsAny(p, " \t\n\r\"'<>\\") {
		return false
	}
	// 纯通配/根路径也保留，排除全是占位符的
	if strings.Trim(p, "/*:.") == "" && p != "/" {
		return false
	}
	return true
}
