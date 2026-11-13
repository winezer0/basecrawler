# assetcrawler

零浏览器依赖的 Go API 目标发现引擎。

面向授权安全测试：从一个入口 URL 出发，尽可能找出目标站点的 API / 接口目标——
HTML 标签属性、响应头、JS 文件里的接口路径、robots.txt、sitemap.xml、打包产物里的
懒加载 chunk 与前端路由，以及 OpenAPI spec 与表单 action。发现之后会**自动填充并
发送测试请求**（含表单提交、所有 HTTP 方法），用响应状态确认哪些是真实接口。

不启动无头浏览器、不渲染 DOM。默认会在 goja 进程内 VM 里执行**命中签名粗筛**的
bundle / 混淆解码器，用来恢复静态看不见的运行时路由与字符串
（`--no-route-exec` / `--no-deobf-exec` 可关）。进程内模拟环境，不是强隔离沙箱。

## 为什么不用现成的

调研过一圈，结论是生态里没有对口的包：

| 方案 | 问题 |
|---|---|
| `colly` | 通用爬虫框架，解决的是队列/去重/限速。资产提取要自己从零写，且不支持 JS 内容挖 endpoint |
| `katana`（作为库引入） | 实测只 import `pkg/engine/parser` 就拉进 **56 个依赖**、`-s -w` 后二进制 **20.2MB**（colly 对照：17 依赖 / 13.4MB）；依赖链 `parser → pkg/output → dsl → go-openai/jarm-go` 把 nuclei 的 DSL 引擎和 LLM 客户端都带进来了；且 `parser_generic.go` 带 `//go:build !(386 \|\| windows)`，**Windows 上 jsluice 的 JS 提取能力整体缺失** |
| `hakrawler` / `gospider` / `cariddi` / `URLFinder` | 都是 CLI，核心逻辑在 `main` 包里，无法作为库复用 |
| `projectdiscovery/urlfinder` | 纯被动（只查存档），完全不访问目标，不是爬虫 |

直接依赖 6 个（`go.mod` 的 direct require）：`golang.org/x/net`（HTML）、
`dop251/goja`（JS VM / AST）、`odvcencio/gotreesitter`（容错 AST）、
`winezer0/requestfilter`（相似 URL 过滤）、`alecthomas/kong`（CLI）、
`evanw/esbuild`（ESM→CJS 转译）。「零依赖」只指**零浏览器依赖**，
不是零第三方库。
二进制体积、代码行数、用例数这类可变量由 `python tools/metrics.py` 生成，
不在文档里写死。

## 包结构

```
assetcrawler/      编排层：种子、BFS 调度、结果组装
├── fetcher/     HTTP 抓取：连接池、限速、按 host 并发、重试、重定向链、body 截断
├── extractor/   URL 提取编排：FromHTML / FromJS / FromHeaders 等载体分发
│   ├── base/        共享类型：Request / ParamValue / LimitReport + 通用小工具
│   ├── sourcemap/   .map 识别与 sources 归一化
│   ├── modulemap/   importmap + sourceMappingURL 声明
│   ├── openapi/     OpenAPI/Swagger 探测与解析
│   ├── nextjs/      __NEXT_FLIGHT_DATA__ 内联数据
│   ├── form/        表单参数与候选值
│   ├── curl/        curl 命令解析
│   ├── inlinejson/  script 内联 hydration JSON
│   ├── json/        JSON 响应体与计算型分页
│   └── deobf/       混淆反解（packer/eval 静态 + string-array 沙箱执行，签名门控）
├── frontier/    归一化 + 范围判定 + 去重
├── routegraph/  打包产物恢复：chunk 文件名 + 前端路由表（自带的 JS 词法分析器）
├── jsext/       JS AST 提取引擎：统一契约 + goja / gotreesitter 两套实现
├── testfill/    测试请求填充与产物渲染（curl / python / postman / http / json）
├── safety/      危险动作拦截公共工具包（零依赖，assetcrawler / prcrawler 共用）
└── csp/         CSP / Report-To 响应头解析：纯函数工具包，由编排层调用
```

依赖单向，子包之间没有横向依赖（jsext 的两个实现只依赖 jsext 契约，
extractor 通过 Options 注入 AST 能力；extractor 编排层本身不发起网络请求，
但 `extractor/deobf` 子包会链接 goja 用于沙箱执行）：

```
assetcrawler ──┬──> fetcher
             ├──> extractor ──(注入)──> jsext/{goja,treesitter}
             │             └──> deobf ──> goja
             ├──> frontier
             ├──> routegraph ──> goja / esbuild
             ├──> csp
             └──> testfill ──> safety
```

`safety/` 是刻意做成公共工具包的：零第三方依赖、不发起网络请求，只回答
「这条动作是否危险」。`prcrawler` 的链接入队与表单提交也直接 import 它，
两边共用同一份词表，不存在两份实现漂移的问题。

分工边界是刻意的：**extractor 不认识 URL 语义，frontier 不认识 HTML，fetcher 不认识「爬虫」，
routegraph 不认识网络**。所以想单独要「一张页面里所有 URL」的人，可以只 import `extractor`；
想单独从一份 bundle 里要 chunk 清单的人，可以只 import `routegraph`。

## 能力

**HTML 提取**（覆盖 20+ 标签 / 30+ 属性）
`a[href]` `area` `base` `link` `script[src]` `img[src|srcset|data-src]` `source`
`video[src|poster]` `audio` `iframe` `frame` `embed` `object[data|codebase]`
`applet[code|codebase|archive]` `form[action]` `input[src|formaction]`
`button[formaction]` `blockquote[q|del|ins][cite]` `body[background]`
`table/tr/td/th[background]` `html[manifest]` `svg use[href]`
`meta[http-equiv=refresh]`、htmx 的 `hx-get/post/put/delete/patch`、
内联 `style` 与 `<style>` 块里的 `url()`、以及 `<script>` 内联代码。

**HTML 注释里的路径**
SSI 的 `<!--#include virtual|file="x"-->`、Dreamweaver 的
`<!-- InstanceBegin template="/Templates/x.dwt.asp" -->`、以及注释中带扩展名的绝对路径。
这类路径浏览器不渲染，但在老 ASP/JSP 站点上常直接指向模板与 include 配置文件——
实测里正是靠这一条，才没漏掉 `testasp.vulnweb.com` 的模板页。

**JSON 响应体**
递归提取响应里以 `/` 或 `http(s)://` 开头的字符串。现代接口常用
`{"next":"/api/orders?page=2"}` 这种「下一跳」，它既不在 HTML 也不在 JS 源码里，
只出现在响应体中，静态提取看不到。单份响应体上限 200 条，避免大列表接口灌满队列。

**参数形状**
表单控件名（`input`/`select`/`textarea`，跳过 submit/button/reset/image/file 与 disabled）
与 JS 调用里的参数对象都作为 `params` 字段带出。

其中**值可静态确定**的那部分另走 `param_values`：

```json
{"name":"cat","values":["12"],"kind":"hidden"},
{"name":"sort","values":["price_asc","newest"],"kind":"select"}
```

来源只有三种：`hidden` 的默认值、`select` 的 option、`radio`/`checkbox` 的组值。
文本框、空值、浏览器默认值 `on`、提交按钮一律不收。

**只报告候选，不合成 URL**：hidden 值常带 csrf/session，拼出来的地址必然失败。
testfill 生成测试请求时默认把这些静态值替换为占位测试值，不原样发回目标站；
只有 `--testfill-use-hidden` 显式授权才使用原值。

**响应头提取**
`Location`、`Content-Location`、`Link`、`Refresh`，外加整条重定向链。

**JS / CSS 内容提取**
从脚本源码里捞绝对 URL、引号包裹的绝对路径、相对路径。
文件末尾的 `//# sourceMappingURL=` 声明会作为 `source` 单独输出并抓取
（本身是 `.map` 时，其中的 `sources` 也一并提取）。

**ESM 部署形态（`<script type="importmap">`）**
浏览器加载模块时按 importmap 解析裸模块名，表里的右值就是真实资源地址，
而标签属性与 JS 字面量里都看不到它们。`imports` 与 `scopes` 两层都解析，
右值支持字符串、数组（fallback）与嵌套对象；裸模块名（`react`）不是地址，不收。
来源标记为 `importmap`。**顺序不保证**（来自 JSON 对象），需要稳定序请自行排序。

**打包产物里的 chunk 与前端路由（`routegraph` 包）**

懒加载 chunk 和 SPA 路由都不挂在任何静态链接上，普通爬虫看不到。从 bundle 的
运行时清单里静态恢复两类目标：

- **chunk 文件名**：webpack 4/5 的 `__webpack_require__.u`（含 hash 查找表）、
  `__vite__mapDeps`、动态 `import()` 说明符。
  `o.u = e => "chunk-" + e + "." + {101:"a1b2c3"}[e] + ".js"` 这类写法里，
  文件名里的 `e` 与查表下标的 `e` 是同一个变量，必须按**同一个 key** 对齐，
  否则会拼出 `chunk-101.d4e5f6.js` 这种不存在的组合。
- **前端路由表**：括号栈先收集对象帧与 `children` 父子关系，扫完后递归算路径，
  不依赖 AST，对压缩与 ESM 产物一致有效。兄弟键覆盖 vue-router
  （component/children/redirect/alias/meta/name）、react-router（element/loader）、
  Angular Router（loadChildren/canActivate）。**嵌套子路由拼接**：children 的相对
  path（`users/:id`）与父 path（`/admin`）拼成真实可达地址 `/admin/users/:id`；
  两阶段解析因此不受键顺序影响（`children` 写在 `path` 之前也一样）、支持三层以上
  相对嵌套；无 path 的布局包装跳过继承祖父前缀，空 `path` 的默认子路由继承父地址；
  子 path 以 `/` 或 `*` 开头视为绝对路径不拼。`alias`（字符串或数组）也产出可抓地址，
  标记为 `Source=route-alias`。

结果带 `kind` 字段：`chunk` 是真实文件（会抓取），`route` 是前端路由
（也抓取，但调用方可据此过滤——SPA 的 `/admin/users` 在服务端往往只是同一个 index.html）。

```bash
assetcrawler -u https://target/ --jsonl                # 默认开启
assetcrawler -u https://target/ --no-chunk-graph       # 关掉
```

**bundle 执行兜底（默认开启，`--no-route-exec` 关闭）**

路由表如果由运行时 `map` 生成（`sections.map(s => ({path:"/"+s}))`），静态解析拿不到，
就把 bundle 在 goja 里跑一遍。这条能力**默认编在二进制里，且默认开启**：

```bash
assetcrawler -u https://target/                  # 默认执行
assetcrawler -u https://target/ --no-route-exec  # 显式关闭
```

代价是链接 goja 的完整 VM 与 esbuild 转译器：二进制体积增加（当前值由
`python tools/metrics.py` 生成，不在文档里写死）。
没有做成可选编译，因为「同一个程序有两种能力」会让基线和结论失去可比性。

**签名粗筛**：只有先通过 `routegraph.LooksLikeBundle`（打包/路由产物标记），
再命中 `LooksExecutable`（webpack/vite 运行时、`createRouter`、`children`、
`export` / `import(` 等）的脚本才进 VM，普通 JS 不跑。执行结果与静态结果
**合并去重**，静态已发现路由时仍会补跑运行时生成的那部分。

- webpack 自注册 chunk（`self.webpackChunkX.push(...)`）与 **ESM/Vite 产物**都支持：
  goja 拒绝解析时经 esbuild 转译为 CJS 再执行，动态 `import()` 由词法器改写为桩。
- 执行路径递归 `children`、展开 `alias`，并把动态 import 的模块 id 转成相对/绝对
  chunk 候选路径；webpack 数字 ID 由静态 `u` 函数表按 ID 对齐文件名。
- 依赖模块只给桩、不递归加载，仍是单模块执行；`import.meta` 与顶层 await 不完整。
- **2MB 体积上限 + 2 秒端到端预算**：解析、esbuild 转译、VM 运行、结果提取
  共用一个 deadline，外层 wall-clock 兜底。这是进程内 VM，不是强隔离沙箱。

**string-array 沙箱（默认开启，`--no-deobf-exec` 关闭）**

javascript-obfuscator 家族（`_0x` 数组 + 解码函数）静态反解无能为力，
默认会在 goja 沙箱里真跑解码器，把 `_0x1a2b(0x1f)` 形态的调用点替换为真实字符串。
**专属签名粗筛**：必须同时出现 `_0x` 数组声明、解码函数定义、解码调用点；
普通 JS、packer / eval 类混淆都不进 VM。2 秒端到端预算（含正则扫描与 goja 解析）、
源码 2MB 上限、解码器名上限 32、替换上限 5000、展开长度上限 2MB。

**混淆 bundle 反解（默认开启）**

打包站点常把 URL 藏在 Dean Edwards packer、`_0x` 十六进制标识符、字符串数组、
eval 字符串字面量里，正则与 AST 都提不到。整条链默认开启，顺序是
**先签名判断，再解混淆**：命中签名走常规多轮解包；签名漏报（自研混淆器）
也强制解包一次，Recover 对未混淆代码是 no-op，正常 JS 结果不变。
解包包含 packer 还原、eval 字符串展开、`\xNN`/`\uNNNN` 解码，最多 3 轮。
`--no-deobf` 关闭整条链。packer 的 `count` 钳到字典长度并有
4096 次循环 / 1MB 展开上限，eval 展开也有个数与总长上限，避免目标站用
一个畸形数字拖垮 CPU。string-array 家族由上面的沙箱路径处理。

**路径 fuzz（默认开启，`--no-fuzz` 关闭）与相似 URL 过滤（`--fsu`）**

```bash
assetcrawler -u https://target/                          # 默认探内置 48 条 API 高价值路径
assetcrawler -u https://target/ --no-fuzz                # 显式关闭
assetcrawler -u https://target/ --fuzz-dict fuzzdict.txt # 追加还原完整 154 条（自动去重）
assetcrawler -u https://target/ --fuzz-dict my.txt       # 追加自定义路径
assetcrawler -u https://target/ --fsu                    # 路径形状归并，对齐 katana --fsu
```

内置字典是面向 API 发现的高价值子集（48 条，`fuzzdict-core.txt`），已包含
`/.git/config`、`/actuator/env` 等固定路径，并去掉尾斜杠变体与重复项。
完整 154 条在 `fuzzdict.txt`。`--fuzz-dict` 是**追加**语义：
最终字典 = 内置 48 条 + 文件里的路径，补前导斜杠、保序去重；`--no-fuzz` 优先关闭。
fuzz 命中页面纳入正常爬取；探测类来源（fuzz / openapi-probe）404 即 miss，
不产出不提取，不污染输出。命中但返回非白名单状态码的，还会被下面的**状态码
输出过滤**挡在结果文件之外。`--fsu` 基于 requestfilter 的覆盖参数策略 + 动态路径段
泛化（阈值 10），非法 URL fail-open 不影响主流程。

**总时长控制（`--ct`）与输出格式（`-O`）**

```bash
assetcrawler -u https://target/ --ct 30m              # 精确收敛，超时提示 [limit] time budget reached
assetcrawler -u https://target/ -O txt -o out/urls   # json（默认，保留来源）| txt（去重纯列表）
```

`--ct` 接受裸秒数或 Go duration 语法；0/省略不限。超时走与 Ctrl+C 相同的收敛路径，
结果文件不含截断记录。`-o` 父目录不存在会自动创建；未指定 `-o` 时按
种子 host+路径自动命名（特殊字符转下划线 + 时间戳后缀）。

**状态码输出过滤（`-s` 白名单 / `-S` 黑名单，对齐 ffuf `-mc` / `-fc`）**

txt 与 JSONL 走同一套过滤，判定顺序固定：**先黑名单命中则丢弃，再看白名单**。

| 参数 | 作用 | 默认 |
|---|---|---|
| `-s, --status` | 白名单（包含） | `200,204,301,302,307,401,403,405,500`（ffuf 默认码表） |
| `-S, --filter-status` | 黑名单（排除） | 空 |

值支持逗号分隔的精确码、范围（`200-299`）、`all`（全放行）、以及 `0`
（`StatusCode == 0` 的未访问线索：robots Disallow、CSP 上报端点、curl 代码块、
`:param` 模板、POST 接口形状）。**0 只是普通状态码，没有例外规则**。

```bash
assetcrawler -u https://target/ -s 200                  # 只 200
assetcrawler -u https://target/ -s 404                  # 只看死链
assetcrawler -u https://target/ -s 0                    # 只看线索
assetcrawler -u https://target/ -s 200-399,401,403      # 严格「确实有效」口径
assetcrawler -u https://target/ -s all                  # 全量，替代旧的 --include-invalid
assetcrawler -u https://target/ -s all -S 404,500-599   # 全量除 404/5xx
```

默认码表沿用 ffuf，因此 **405、500 会出现在默认输出里**；要严格只给下游
可请求的 URL，显式写 `-s 200-399,401,403`。`StatusCode == 0` 的线索默认不输出，
`-s 0` 或 `-s ...,0` 才可见。库层 `Crawl` 返回值不受影响，外部导入者仍拿到全量
结果，`--testdata` 也仍从全量结果生成请求脚本。

**testfill 确认结果也写进结果文件（默认，`--no-testfill` 关闭）**

自动发送的测试请求，凡是真正发出且拿到响应状态的，都会作为一条
`Source=testfill` 的结果追加到同一个 txt / JSONL 文件，走的是与 Crawl 结果
**完全相同**的 `scope / -s / -S / 去重 / fsu` 过滤链：

- JSONL 里带 `method`（实际发送方法，可能是 POST/PUT/PATCH/DELETE）、
  `status`（测试请求的响应状态）、`url`（含填充后的 query）与 `source=testfill`；
  这里的 `status` 是「发送响应」而非「发现时状态」，靠 `source` 区分。
- txt 是纯 URL 列表，只追加没出现过的 URL；同址重复会被去重。
- 404 / 5xx 等确认结果同样受 `-s` 约束；`-s all` 可看全量。
- `[done]` 计数包含确认结果，等于结果文件的实际行数。

只有 CLI 输出文件包含确认结果；库层 `Crawl` 返回值不含，外部导入者行为不变。
`--testdata` 产物的行内确认注释仍独立保留。

**发送安全链（默认开启）**：`safety/` 公共工具包按关键字拦截登出、改密、删除类动作，
命中只记 `SkipReason`、不真发请求（发现结果不受影响）；`--no-safety` 关闭
（关闭后危险动作会真实触发，慎用），`--safety-url-skip` / `--safety-form-skip` /
`--safety-action-skip` 分别给三类词表追加正则（动作词表只在非幂等方法上生效）。
`--max-testfill` 默认封顶 200 条（0 = 不限），`--no-unsafe-send` 退回只发
GET/HEAD/OPTIONS；`--follow-form-actions` 让非 GET 表单 action 额外按 GET 抓一次。
被拦截、超限或未发送的条目不会产生 `source=testfill` 记录。
CLI 的 `-h` 把这些开关统一收在 `Active actions` 分组下。

**前端框架支持**

| 层 | 已适配 | 靠通用提取兜住 |
|---|---|---|
| 构建产物 | webpack 4/5（`u` 函数、`publicPath`、自注册 chunk）、Vite/Rollup（ESM、`mapDeps`、动态 `import()`、静态模板；`./x.js` 按当前模块锚定） | Turbopack、Angular CLI、esbuild、Parcel |
| 文件路由 / hydration | Next.js App Router RSC 流、Next.js Pages Router `__NEXT_DATA__`、Nuxt `__NUXT_DATA__`、Vue SSR `__INITIAL_STATE__` | SvelteKit 靠 HTML `link` 与 sitemap（实测资产已覆盖） |
| 前端路由 | 显式路由表（`{path, component/name/lazy/children/alias}`），覆盖 vue-router（含 `vue-router` 纯 chunk 标记兜底）、react-router 配置式、Angular Router（`forRoot`/`loadChildren`）；两阶段父子树解析支持任意层嵌套、键顺序无关，alias 也产出地址。运行时 map 生成的路由由默认开启的执行兜底补上（webpack + ESM/Vite，含 children 递归） | 纯文件路由框架仍靠 sitemap 与 HTML 链接 |

几个实测结论：

- 现代框架普遍用 `<link rel="modulepreload">` 把懒加载 chunk 列在 HTML 里，
  比从 runtime 反推更可靠——所以我们从 `u` 函数恢复的能力，主要对**老 webpack 站点**有价值。
- Next.js（App Router）的 RSC 流把导航结构序列化进 `self.__next_f.push`，
  已专门解析（`extractor/nextjs`），实测单页能多拿 60 条；命中 `__next_f`
  但没有 payload 的初始化脚本会回退到通用 JS 提取，不会整段归零。
- Nuxt 3 的 `__NUXT_DATA__` 把斜杠写成 `\u002F`，必须按 JSON 解码后再取
  字符串值（`extractor/inlinejson`）；单份 payload 上限 200 条，并与原有
  AST 提取合并以免丢掉旧召回。实测 nuxt.com 首页 payload 约 200 KB、
  含 460 条站内路径。
- Nuxt 入口的 `__vite__mapDeps` 元素写成 `./x.js`，早期统一补 `/` 会得到
  `/./x.js` → `/x.js`，整批 211 个 chunk 落空；现在保留相对形态交给
  frontier 按当前 JS 的 URL 解析。SvelteKit 实测没有额外增量，故不加专属规则。
- 配额很小时（如 `--max-urls 120`），sitemap 页面会先占满队列：
  angular.dev 实测 sitemap 91 条、main.js 解析出的 240 个 chunk 只进了 12 个。
  这是「页面优先」的取舍，不是缺陷；要接口就把配额开大（300 以上）。

**CSP 响应头（输出线索，不入队）**

`csp` 包解析 `Content-Security-Policy` / `CSP-Report-Only` / `Report-To` /
`Reporting-Endpoints`，给出「页面 HTML 与 JS 里未必出现」的基础设施线索：

```go
res := csp.ParseHeader(resp.Header)

res.SourceHosts()        // 策略允许的来源主机（通配符已归并到裸域）
res.SameSite("a.com")    // 同站 / 子域的来源
res.Reports()            // report-uri / report-to / reporting-endpoints 的上报端点
res.URLs()               // 可抓取的 URL（跳过通配与 scheme-only）
```

`csp` 包本身是纯函数，但**采集流程默认会调用它**（`crawler.go` →
`emitCSPHints`，`--no-csp` 可关）：把允许的来源主机与上报端点以
`csp:<directive>` 为 source 输出，**只报告不入队**（这些线索 `StatusCode=0`，
默认状态码白名单不含 0 所以不写进结果文件，`-s 0` 才能看到）。理由：

- 来源主机多为域名根，抓取实测 5 个样本只有 1 个 200，抓它只是拿配额换 404；
- 上报端点常带一次性 token（`report-uri` / `report-to` / `reporting-endpoints`），
  抓取等于向目标注入伪造上报，但作为资产线索很有价值，例如 Cloudflare 的
  `https://a.nel.cloudflare.com/report/v4?...`，这类接口在 HTML 与 JS 里都看不到。

排除三类：`frame-ancestors`（入站语义，列的是外部父页面）、通配主机（无法定位）、
同 host 来源（只是重复当前页面）。

早期「48 个主机仅 3 个同站 ⇒ 不接入」的判据**是错的**：非同站主机在默认
`scope=subdomain` 下本来就会被 frontier 挡掉，不会入队，不该算进噪音。
改按「接入后结果里多出什么」重测，vuejs.org +1、stripe.com +23，
于是改为线索接入。完整复盘见 `STATUS.md` / `ISSUES.md`。

**JS 引擎策略（`--js-engine`，默认 `auto`）**

| 取值 | 行为 |
|---|---|
| `auto`（默认） | goja 快路径优先，解析失败自动落到 treesitter |
| `goja` | 只用 goja；遇到 ESM 直接失败并回退正则 |
| `treesitter` | 只用 treesitter，最全但慢约 1.8 倍 |
| `regex` | 零依赖正则，最快，拿不到方法与参数 |

为什么默认是 `auto` 而不是单引擎：

- **goja 解析不了 ESM**（`import`/`export`/`import.meta`/顶层 `await`），
  而 Vite 构建的站点入口 chunk 就是 ESM——实测 `vuejs.org` 与 `vite.dev` 的入口文件
  让 goja 在第一个 token 就报错，整份文件会退化成正则。
- **treesitter 全能但慢**：1MB 输入约 3.1s（goja 1.7s），语法表让二进制多约 4.1MB（符号段实测）。
- 串联之后：常规脚本走快路径，ESM 走完整解析，两者都不牺牲。

注意 `--js-engine` 只管**提取链**的 AST 解析。bundle 执行兜底是另一条路径，
由 `--no-route-exec` 控制；那里 ESM 会先经 esbuild 转译为 CJS，不受 goja 的
ESM 解析限制，两条链路不要混为一谈。

goja 遇到 ESM 时**显式报错**而不是「剥掉 import/export 再试」——后者会让
`export default {a:"/x"}` 变成代码块解析成功，既不报错也提取不到内容，
自动兜底链根本不会触发。详见 `ESM-ANALYSIS.md`。

为什么不用 esbuild 换掉 treesitter：真实产物上零增量，它的优势（作用域、import 溯源）
可以用本地代码覆盖，详见 `ESBUILD-ANALYSIS.md`。

**入口补充**
`robots.txt`（Sitemap 声明）、`sitemap.xml`（递归处理 sitemap index、支持 gzip）、
内置路径字典 fuzz（48 条 API 高价值子集，默认开启，`--fuzz-dict` 可追加）、OpenAPI spec 探测（默认开启）、robots Disallow 入队抓取（默认开启）。

**调度**
分层 BFS（深度边界清晰）、URL 归一化去重（入队登记 + 每层派发前二次去重，
同一页面被多个父页同时发现时只抓一次）、scope 五档
（fqdn / rdn / dn / regex / all）、全局并发 + 单 host 并发 + QPS 限速、
手动跟随重定向、body 截断、gzip/deflate 透明解压、5xx 重试退避。

## 用法

```go
package main

import (
	"context"
	"fmt"

	"github.com/winezer0/assetcrawler"
)

func main() {
	opts := assetcrawler.DefaultOptions()
	opts.MaxDepth = 3
	opts.Concurrency = 10
	opts.Scope = assetcrawler.ScopeSubdomain
	opts.OnResult = func(r assetcrawler.Result) {
		// 库内部已串行化回调，直接写文件不需要额外加锁
		fmt.Printf("%-60s %s %d\n", r.URL, r.Source, r.StatusCode)
	}

	c, _ := assetcrawler.New(opts)
	defer c.Close()

	results, _ := c.Crawl(context.Background(), "https://example.com")
	fmt.Println(len(results))
}
```

库的引入与契约：

```bash
go get github.com/winezer0/basecrawler
```

- **一实例一采集**：`Crawl` 结束后结果集与已见集合不重置，同一实例
  第二次 `Crawl` 会显式报错——再采一次就再 `New` 一个实例；
- `Close` 之后 `Crawl` 返回 closed 错误；
- `OnResult` / `OnError` 由库内部串行化调用，回调里不需要加锁；
- 只想要提取器（`extractor`）、URL 边界（`frontier`）、抓取（`fetcher`）、
  路由图（`routegraph`）的人可以单独 import 对应子包，互不牵连。

## 作为库使用的四个包

本包的爬取与提取能力都是一等公民，别的爬虫可以直接嵌入其中任意一层，
每层都有 godoc 可运行示例（`Example*`，随 `go test` 验证）：

| 场景 | 包 | 入口 | 示例 |
|---|---|---|---|
| 完整采集编排 | 根包 | `New` / `Crawl` / `OnResult` | `ExampleCrawler` |
| 只要 URL 提取 | `extractor` | `FromHTML` / `FromJS` / `FromJSON` / `FromHeaders` / `FromSourceMap` | `extractor.ExampleFromHTML`、`ExampleFromJS` |
| 只要 HTTP 抓取 | `fetcher` | `New` / `Get`（连接复用、限速、重定向链） | `fetcher.ExampleFetcher`、`ExampleFetcher_redirects` |
| 只要 URL 边界 | `frontier` | `Normalize` / `New` / `Push`（规范化、范围、去重） | `frontier.ExampleFrontier`、`ExampleNormalize` |

组合模式：`extractor` 只吐原始字符串 → `frontier` 决定收不收 →
`fetcher` 负责抓 → 根包 `Crawler` 把三者编排成完整采集。
每一层都可单独替换（比如换成自己的 HTTP 客户端或自己的去重集合）。

只想要提取器（不牵扯网络）：

```go
doc, _ := html.Parse(strings.NewReader(pageHTML))
for _, r := range extractor.FromHTML(doc, extractor.DefaultOptions()) {
    fmt.Println(r.Raw, r.Source, r.Method) // Raw 可能是相对路径，由你决定怎么解析
}
```

版本查询（早于一切初始化，不发请求）：

```bash
assetcrawler --version   # dev (commit <vcs.time>)；发布构建显示注入的版本号
```

发布版本号由构建时注入（`-ldflags "-X main.version=<tag>"`），
打 `v*` tag 时 `.github/workflows/release.yml` 自动构建双平台产物
（含 SHA256SUMS）并创建 Release。

CLI（`cmd/assetcrawler`，解析引擎为 [kong](https://github.com/alecthomas/kong)；
**GNU 风格 flag**：多字符参数双横线 `--max-urls`，单字符保留短名 `-u`/`-d`/`-O`）：
```bash
# 只要 URL 列表
assetcrawler -u https://example.com -d 3 -o urls.txt

# JSONL，带来源与状态码
assetcrawler -u https://example.com --jsonl -o result.jsonl

# 收敛范围、加认证头、走代理
assetcrawler -u https://example.com --scope same-host -H "Cookie: sid=xxx" --proxy http://127.0.0.1:8080

# 自定义路径探测：追加到内置字典（重复条目自动去重）
printf '/.well-known/internal.json\n/ops/panel\n' > mydict.txt
assetcrawler -u https://example.com --fuzz-dict mydict.txt

# 完全静态抓取：关掉默认开启的探测与执行链
assetcrawler -u https://example.com --no-fuzz --no-probe-openapi \
    --no-route-expand --no-deobf --no-route-exec --no-deobf-exec

# 测试数据填充：把采集结果（含文档 curl 代码块）渲染成可跑的请求脚本
assetcrawler -u https://platform.openai.com/docs/api-reference -d 3 \
    --testdata curl --testdata-auth 'Authorization: Bearer ${TOKEN}' -o test.sh

# 其他产物格式：postman / python / http / json（jsonl）
assetcrawler -u https://target/ --testdata postman --testdata-out collection.json

# 默认：自动填充并发送测试请求（含 POST 表单提交、所有 HTTP 方法），
# 响应状态同时写入主结果文件（Source=testfill）与产物行内注释
# input type 决定值形态，POST 参数进 body；hidden 静态值替换为占位测试值
# safety 默认拦截危险动作，--max-testfill 默认最多发 200 条
assetcrawler -u https://target/ --testdata curl -o test.sh

# 只生成脚本不发送
assetcrawler -u https://target/ --testdata curl --no-testfill -o test.sh
```

常用参数（GNU 风格：多字符一律 `--xx`，单字符短名保留 `-u/-d/-c/-o/-O/-s/-v/-k/-i/-H`）：
`-d` 深度、`-c` 并发、`-O json|txt` 输出格式、
`-s` 状态码白名单（默认 ffuf 码表，支持 `200-399` / `all` / `0`）、
`-S` 状态码黑名单（如 `404,500-599`）、
`--phc` 单 host 并发、`--rl` QPS（默认 0 = 不限速）、`--max-urls` 页面上限、
`--max-body` body 截断、`--max-seen` 已见集合上限（默认 20 万）、
`--mdp` 单域名页面数上限（防爬虫陷阱）、`--ct` 总时长（如 `30m`）、
`--no-js`、`--no-robots`、`--no-sitemap`、`--no-csp`、`--no-noise-filter`、
`--no-curl-blocks`、`--no-route-exec`、`--no-deobf-exec`、
`--stable-host-order`、`-k` 跳过 TLS 校验、`--dr` 不跟随重定向
（跳转目标仍作为资产报告）、`-i` 只爬种子路径之下
（严格路径内配 `--scope fqdn -i`）、
`--scope` 五档：`rdn`（默认，根域+所有子域）/ `fqdn`（仅种子主机）/
`dn`（域名含种子根域关键字）/ `regex`（配 `--scope-regex` 给自定义正则，
如 `'(company-staging.io|company.com)'`）/ `all`（不限，旧值
`same-host`/`subdomain`/`any` 仍兼容）。作用域双层生效：既约束
爬取导航入队，也约束输出显示（范围外结果一律不打印）；种子
始终作为起点抓取，不受范围判定拦截。
`--include` / `--exclude` 正则过滤、`-v` 打印错误、
`--js-engine`（auto|regex|goja|treesitter）、`--fsu` 相似 URL 过滤。
以下能力**默认开启**，各有对应的 `--no-*` 关闭开关（`-h` 的 `Active actions` 分组）：
`--no-fuzz`（字典探测）、`--no-probe-openapi`（spec 探测）、
`--no-route-expand`（路由占位值展开）、`--no-deobf`（混淆探测链）、
`--no-route-exec` / `--no-deobf-exec`（两条 JS 执行链）、
`--no-robots-disallow`（Disallow 入队抓取）、`--no-testfill`（测试请求填充与自动发送）、
`--no-safety`（危险动作拦截）、`--no-unsafe-send`（只发幂等方法）；
`--fuzz-dict` 追加自定义路径（去重），`--route-expand-max` 限制展开条数（默认 50），
`--max-testfill` 限制实际发送条数（默认 200），`--safety-url-skip` /
`--safety-form-skip` / `--safety-action-skip` 追加拦截正则，
`--testfill-use-hidden` 显式使用 hidden 原值，
`--follow-form-actions` 额外抓取表单 action。
输出状态码过滤见上文「状态码输出过滤」一节。

### OpenAPI spec 探测（默认开启，`--no-probe-openapi` 关闭）

标准 spec 路径命中时能一次拿到整套接口契约（路径 + 方法 + 参数名），精确度
远高于从 HTML/JS 猜路径。代价是每个 origin 多 14 个请求，主流站暴露率低
（实测 2.4%，主流站 0%）；不需要就用 `--no-probe-openapi` 关掉。

```bash
assetcrawler -u https://petstore.swagger.io
# Source=openapi、Confidence=template 的条目就是 spec 里的接口形状，
# 路径模板是 /pet/:petId 形式（不是 {petId}，那会被 URL 编码成 %7BpetId%7D）
```

`--no-noise-filter` 关掉 js-ast 高噪音候选过滤（默认开）。过滤只作用于
js-ast 来源的三类路径：`node_modules/`、`.pnpm` 虚拟包路径、`.ts/.tsx`
等源码扩展、`/:param` 路由模板——依据实测，这三类占 remix.run 404 的
114/118；HTML 链接来源不受影响。

`--stable-host-order` 开启同层任务的确定性排序（按 host 任务数优先、组内按
URL 排序），用于让 `--max-urls` 饱和时的结果可复现。**默认关闭**：实测它把
vuejs.org 的连跑差集从 22 增 / 20 失压到 0 / 0，但净少约 15 条站内 URL、
多 5 条 404。因此只建议做基线回归时显式开启，且基线必须用同一开关重采。

**下列能力全部默认开启**，每次采集都会向目标发请求或在本地执行目标站代码：
字典 fuzz（48 条）、OpenAPI spec 探测、`:param` 路由展开、混淆探测链、两条 JS 执行链、
robots Disallow 入队抓取、testfill 自动填充与发送（所有方法，含表单提交）、
`safety` 危险动作拦截。请求量上限：每 origin 48 + 14 + 最多 50 次探测，
测试请求最多 `--max-testfill 200` 条。
要完全静态运行就一次性全关：

```bash
assetcrawler -u https://target/ --no-fuzz --no-probe-openapi \
    --no-route-expand --no-deobf --no-route-exec --no-deobf-exec \
    --no-robots-disallow --no-testfill
```

路由展开条数用 `--route-expand-max` 限流（默认 50）。

## 结果模型

```go
type Result struct {
	URL         string
	Method      string // 发现时推断的方法（form/hx-post 会是 POST）
	Source      string // seed | a | img | script | js | js-ast | redirect | header:link | robots | robots:allow | robots-disallow | sitemap | fuzz | form-action | testfill | sourcemap | openapi | openapi-probe
	Attribute   string // 该 URL 来自哪个属性
	Tag         string
	Depth       int
	StatusCode  int    // 0 表示未实际访问（如 robots 线索、含 EXPR 的模板）
	ContentType string
	Referer     string
	Confidence  string // 空 | template（含占位符）| interface（非 GET 接口形状）| clue（只报告的情报线索）
	Kind        string // 空 | chunk（打包分片）| route（前端路由）
	Params      []string  // 推断出的参数名（表单控件、JS 调用参数对象、OpenAPI spec）
	ParamValues []ParamValue // 控件的静态候选值（hidden/select/radio/checkbox）与 input type
}
```

`Source` 值得保留：排查误报、评估某类提取是否有效，全靠它。
注意这是**库层 Result 模型**：`StatusCode == 0` 的线索仍在 `Crawl` 返回值里，
只是 CLI 默认状态码白名单不含 0 所以不输出（`-s 0` 可看）。
`source=testfill` 是 CLI 在爬取结束后追加的发送确认记录，不出现在 `Crawl`
返回值里（详见上文「状态码输出过滤」一节）。`Confidence=clue` 标记只报告的
情报线索（CSP 允许域、robots Disallow/Allow），testfill 按语义字段判定，
不再靠 `source` 字符串前缀，新增来源默认按接口形状放行。

## 几个刻意的设计取舍

**robots.txt 的 Disallow 默认入队抓取**（`--no-robots-disallow` 关闭）。
命中 200/401/403 即为真实接口；404 归为探测 miss 不产出（`isProbeSource`）。
关闭时退回旧行为：`Source=robots`、`StatusCode=0` 的线索只输出，
用 `-s 0` 才在结果文件里可见。`Allow` 路径始终输出为 `Source=robots:allow`
线索（不入队）；含 `*$` 的通配路径与范围外路径的跳过量进 `LimitReport`，
在 `[limit] robots-wildcards` 一栏可见，不再静默吞掉。

**JS 提取默认走 AST，正则只是兜底。**
`extractor` 编排层是不发起网络请求的纯函数集合（只捞字面量）；AST 能力由
`extractor.Options.JSExtract` 注入，实现在 `jsext/` 子包（goja 严格解析 +
treesitter 容错解析）。`extractor/deobf` 子包会链接 goja 用于沙箱执行，
所以「零第三方依赖」只适用于编排层，不是整个 `extractor` 目录。
默认引擎 `auto` 先 goja 再 treesitter，两者都失败
（或零产出）才回退正则。关掉用 `--no-js`，强制正则用 `--js-engine regex`。
引 AST 的代价是依赖变重，收益是能还原 `"/api/" + id` 这类拼接、不做无谓的
碎片召回——A/B 实测同一站点 AST 档 169 条 vs 正则档 58 条。

**URL 归一化偏保守。**
只做 scheme/host 小写、去默认端口、去 fragment、折叠 `../`。不排序 query、
不折叠路径大小写、不去尾斜杠——这些在真实站点上可能代表不同资源，过度归一化会漏资产。

**回调串行化。**
抓取是并发的，但 `OnResult` / `OnError` 在库内串行派发，调用方不用自己加锁。

## 不做什么（范围边界）

本包只解决「把一个站点上能静态发现的 URL 尽量收全」这一件事。以下明确不做，
需要时请配合专用工具，不要往里加：

| 不做 | 理由 / 替代 |
|---|---|
| 敏感信息提取（身份证、手机、邮箱、密钥、API key） | 属内容分析而非 URL 采集，规则演进快，交给 URLFinder 这类工具 |
| 递归目录组合 fuzz（笛卡尔积猜路径） | 请求量随目录层级立方增长；内置的有限字典探测性质不同，且默认开启 |
| 漏洞检测、指纹识别 | 不同的问题域 |
| 渲染 DOM / 启动无头浏览器 | 与零浏览器依赖目标冲突。注意：在 goja VM 里执行目标 JS 默认开启，但签名门控、可关闭，且不等于渲染 |
| 自动漏洞利用 / payload 投递 | 不同的问题域；`safety` 只挡危险动作，不做漏洞判定 |
| CSV / HTML 报告导出 | 结果以 `Result` 结构返回，序列化成什么由调用方决定 |

## 已知局限

**能力缺陷（有意不做或暂缓，均为明确边界而非 bug）：**

- 不渲染 DOM、不启动浏览器。纯前端渲染（内容完全靠 XHR 拉取、DOM 里没有
  链接）的页面挖不到。运行时生成的路由由**默认开启**的执行兜底覆盖
  （webpack 自注册 chunk 与 ESM/Vite 均已支持），但它是「单模块 + 依赖桩」：
  入口 bundle 把模块表关在 IIFE 闭包里、`import.meta` / 顶层 await 这类语法
  仍拿不到。这类目标配合浏览器方案（VueCrack 这类运行时接管插件）或业务层
  直接打接口更稳。
- 执行与沙箱都是**进程内 goja VM**，不是强隔离：不注入网络/文件 API，但目标
  代码仍可能消耗 CPU/内存，靠 2MB 源码上限 + 2 秒端到端预算收敛。要完全静态
  运行，用 `--no-route-exec --no-deobf-exec`。
- 不提供断点续爬（`--resume`）：`--ct`/Ctrl+C 中断后需要从头再爬。这是明确的范围边界，不列入后续待办。
- 登录态需手动传：`-H "Cookie: sid=xxx"` 单头注入，无 cookie 文件批量加载。
- `--fsu` 的路径归并状态不持久化，跨次运行不共享。
- 前端路由的 `:param` 展开默认开启（`--no-route-expand` 可关），占位值
  不代表真实取值；SPA 上可能只是重复抓首页。
- webpack 执行产出的数字 chunk ID 只有在同一 body 静态扫到 `u` 函数表时才能
  对齐文件名；否则只保留编号、不入队。
- `safety` 是关键字词表，不是语义理解：改了名字的删除接口、非幂等 GET、
  crawler 的正常 GET 导航（如 `/logout` 链接）都不在拦截范围内。它只作用于
  testfill 发送，不阻止爬取导航。hidden 值默认占位符化、发送默认封顶 200 条。

**结构性取舍：**

- JS 提取默认走 AST（goja + treesitter 串联），正则只是最终兜底；AST 能还原
  `"/api/" + id` 拼接，但动态拼接出的地址仍是候选而非确认可达。
- 分层 BFS 每层之间有屏障，同一层的慢请求会拖住整层。追求极限吞吐时应改用连续 worker pool。
- 已见集合常驻内存（`--max-seen` 默认 20 万封顶），超大站点上它是唯一无上限增长点。
- HTML 注释、模板字符串等载体可能带入失效路径（历史存档、废弃接口），靠
  `kind`/`source` 字段人工甄别。

## 合规

仅用于**已获得书面授权**的目标。默认会自动发送 GET/POST/PUT/PATCH/DELETE 测试请求
（含表单提交），可能创建、修改或删除目标数据；已有 `safety` 关键字拦截、
hidden 值占位符化与 `--max-testfill 200` 上限，但都不是强保证。
生产环境请先 `--no-testfill`（或 `--no-unsafe-send`），并用 `--rl` 限速。
默认限制并发（全局 10 / 单 host 4），**QPS 默认不限速**（`--rl 0`）。
请勿把 `--rl 0`、`--max-urls 0` 同时用在别人的生产环境上。

## 测试

```bash
go test ./...              # 10 个包的全量单测（用例数见 tools/metrics.py）
go test -race ./...        # 已验证无竞态
python -m unittest discover -s tools -p 'test_*.py' -v   # 基线工具纯函数单测
```

非 `--silent` 模式结束时还会打印各提取上限的截断统计，避免「结果看起来完整、
其实被静默截断」：

```text
[limit] inline-json  候选 638 / 产出 200 / 截断 438
```

四处上限（内联 hydration JSON、RSC flight、sourcemap、表单值）统一走
`extractor.LimitReport`，解析器仍是纯函数，状态只存在于编排层。

测试按层分布：`frontier` 测归一化与范围、`extractor` 测各类载体的提取、顶层测完整链路与配额。
