# Changelog

本项目的显著变更记录于此。批次 A~J 的完整实施记录见 `STATUS.md` 与
`ROADMAP.md`；数字类指标（行数、用例数、二进制体积）以
`python tools/metrics.py` 为准，不在本文件写死。

## v0.5.0（2026-11-13）

### 发布与 CI 门禁（2026-11-13）

- 把 Unreleased 周期内的全部变更定版为 `v0.5.0` 并推送 tag；`safety/`
  作为公共工具包随版本发布，prcrawler 删除本地 `replace` 后直接 require
  该版本，不再需要本地路径过渡。
- `tools/gate.json` 重采：补 deobf 预算分支、routegraph 执行/ESM 守卫、
  extractor 类型判定与 sitemap/CSS 边界共 21 个用例；`safety` 登记 100%，
  `cases_min` 274 → 337，其余包按「只升不降」取新旧最大值。
- 修 `testdata/probe/check.py`：`--jsonl` / `--silent` / `--js-engine` 是 kong
  长 flag，写成单横线会被当成 `-j` 直接退出 2；同时显式 `-s all` /
  `--scope all` / `--no-fuzz`，让夹具只量提取能力、不受默认输出策略影响
  （实测 20/20 载体、5/5 方法、3/3 参数、0 噪音）。
- 修 `cmd/assetcrawler/main.go` 中「关键字拦截尚未实现」的过时注释。
- **已验证**（本地对齐 CI 全步骤）：`gofmt`、`go vet ./...`、
  `go test -race ./...`（22 包全绿）、`go test -cover ./...`、`go build`、
  tools 112 项 + probe 4 项 Python 单测、探针夹具、`metrics.py --gate`。
- **已知限制**：默认开启 fuzz 时，字典里的地址会先以 GET 入队，URL 级
  去重可能吞掉同址的 POST 表单接口形状（探针因此显式 `--no-fuzz`）；
  本轮不改去重语义，留待后续评估。
- **未做**：真实授权站点的基线重采（`tools/rebaseline.py --run`）与定时
  对拍；`STATUS.md` / `ROADMAP.md` / `ISSUES.md` 仍停在批次 T。

### safety 收敛为公共工具包（2026-11-13）

- `safety/` 定位为可独立 import 的公共工具包：补完整包文档与公共 API 面
  （`NewSafety` / `NewSafetyWithActions` / `Enabled` / `SkipURL` / `SkipForm` /
  `SkipRequest` / `IsSafeMethod`），新增两个可运行 Example 展示浏览器爬虫与
  静态爬虫两种调用范式。零第三方依赖、不发起网络请求。
- **prcrawler 不再维护副本**：删除其 `internal/crawl/safety.go`，链接入队与
  表单提交两处直接调用 `safety.NewSafety` / `SkipURL` / `SkipForm`；它的
  `safety_test.go` 改为对公共包做黑盒回归。两边共用同一份词表，消除漂移。
- `testfill` 删除本地重复的幂等方法表，统一用 `safety.IsSafeMethod`。
- CLI 新增 `--safety-action-skip`（第三类动作词表可追加正则，之前 API 有但
  CLI 不可达）；`--no-safety` 的 help 补「危险操作将真实触发，慎用」。
- **发布状态**：`safety` 是 `v0.4.0` 之后新增的包，随 `v0.5.0` 发布。
  prcrawler 的 `go.mod` 已删除本地 `replace`，直接 require 该版本；
  两边共用同一份词表，不存在两份实现漂移的问题。

### 发送安全链、线索语义化与调度去重（2026-11-13，破坏性变更）

- **新增 `safety/` 包**：参考 `prcrawler/internal/crawl/safety.go`，零第三方依赖、
  `NewSafety` 签名保持兼容（prcrawler 后续可在 assetcrawler 发版后直接 import）。
  内置登出 / 改密 / 删除词表 + 非幂等方法动作词表，在 testfill 发送前拦截；
  命中只记 `SkipReason`，发现结果不受损。CLI 默认开启，`--no-safety` 关闭，
  `--safety-url-skip` / `--safety-form-skip` 追加正则（非法正则启动即报错）。
- **hidden 值默认占位符化**：`FillParamsSafe` 把 csrf/session 等静态值替换为
  占位测试值，`--testfill-use-hidden` 才使用原值；`TestRequest.Fields` 带上
  参数名供 safety 判定。`--max-testfill` 默认 200 接上 `MaxSend`；`send.go`
  「必须由调用方显式授权」的过时注释已修正。
- **线索判定语义化**：删掉 `clueSources` 的 5 个前缀，改按
  `StatusCode / Confidence=clue / Kind` 判定，新增来源 fail-open。
  `robots-disallow` 抓取命中的真接口不再被误过滤；CSP、robots Disallow/Allow、
  `:param` 路由形状统一打 `Confidence=clue` 标记。
- **robots 边界补齐**：`Allow` 输出为 `Source=robots:allow` 线索（不入队）；
  `*$` 通配与范围外路径的跳过量进 `LimitReport`（`robots-wildcards`）。
- **库层边界可配置**：`Options.FollowFormActions`（默认 false）与
  `AllowUnsafeSend`（默认 true），CLI 暴露 `--follow-form-actions` /
  `--no-unsafe-send`；`-h` 把主动开关收进 `Active actions` 分组。
- **去重下移到派发通道**：`Crawler.dispatched` 跨层全局去重，消除同址重复
  派发与 `MaxURLs` 配额浪费。**结果条数会略减，真实站点基线需重采**
  （`gate.json` 的用例/覆盖率基线已在 v0.5.0 定版时重采）。
- **基线工具与配置**：`tools/baseline.sites.json`（5 站 + 固定采集口径）、
  `tools/rebaseline.py`（默认 dry-run，不请求外部站点、不覆盖既有基线）、
  `drift.py` 支持 `sites_file` 且离线无样本时拒绝自动采集、`tools/README.md`
  补重采 SOP。本轮**未建定时任务、未实际采集外部站点**。
- **已验证**：`go build ./...`、根包 / cmd / testfill / safety / extractor 全簇
  针对性测试全绿；本地站点冒烟覆盖 safety 拦截与 `--no-safety`、hidden 占位、
  Allow 线索、robots-disallow 生成测试请求、通配统计、两个新主动开关、派发去重；
  `tools/` 下配置测试 4 项 + drift 单测 11 项通过。**未验证**：真实站点基线重采
  与定时对拍（需显式 `--run` 与授权目标，本轮未执行）。gate 基线重采与全量
  `-race` 已在 v0.5.0 定版时补齐（见上节）。

### testfill 确认结果写入主结果文件

- 之前只有 `--testdata` 产物与 stderr 计数能看到发送结论，主结果文件（txt/JSONL）
  不含任何确认信息。现在自动发送且拿到响应状态的测试请求会以 `Source=testfill`
  追加到同一结果文件，复用统一的 `writeResult` 过滤链（scope / -s / -S / 去重 / fsu）：
  JSONL 带 `method`（实际发送方法）、`status`（响应状态）、含填充 query 的 `url`；
  txt 只追加未见过的 URL。`[done]` 汇总移到所有写入之后，计数等于文件实际行数。
- 新增 `TestRequest.SentURL()`：把填充后的 query 合并进 URL，SendRequests 与落盘
  共用一个口径，避免结果文件里的地址与真实请求不一致。
- `--no-testfill` 时不产生确认记录；库层 `Crawl` 返回值不含确认结果，外部导入者
  行为不变；`--testdata` 产物的行内确认注释保持独立。
- 测试：`TestRequestSentURL`（参数覆盖 / 非法 URL 回退）、`TestConfirmResults`
  （只保留已发送且有状态的条目、字段正确、空列表不产出）；本地站点冒烟确认
  JSONL 出现 POST 200 的 `source=testfill` 记录，`[done]` 计数与文件行数一致。

### 定位改为 API 目标发现：统一 testfill + 主动探测（破坏性变更）

- **工具定位**：从「URL 资产采集」改为「尽可能发现 API / 接口目标」，发现后自动
  填充并发送测试请求，用响应状态确认哪些是真实接口。
- **发送开关统一为 `--no-testfill`（默认开启）**：删除 `--testdata-send` /
  `--testdata-send-unsafe` 的幂等/非幂等二分，`AllowUnsafe` 固定为 true，所有 HTTP
  方法（含 POST/PUT/PATCH/DELETE）默认自动发送；发送不再依赖 `--testdata` 产物
  格式。`--testdata` / `--testdata-out` / `--testdata-auth` 保留。
  **关键字级危险动作拦截为后续方案，本轮未实现**：hidden 值原样填入、发送条数不限。
- **robots Disallow 入队抓取**：新增 `Options.FollowRobotsDisallow`（默认 true）与
  `--no-robots-disallow`；命中 200/401/403 即真接口，404 归入 `isProbeSource`
  不产出。`readRobots` 改为返回 sitemap 与 Disallow 路径，由 bootstrap 决定入队或
  退回「只输出线索」。
- **内置 fuzz 字典精简为 48 条 API 高价值子集**（`fuzzdict-core.txt`）；完整 154 条
  保留在 `fuzzdict.txt`，`--fuzz-dict fuzzdict.txt` 可追加还原（保序去重）。
- **风险**：默认行为会向目标写入数据，仅用于书面授权目标；不需要就 `--no-testfill`。
- 测试：参数映射与默认值、`TestCoreDictIsSubsetOfFull`、测试 helper 隔离 Disallow
  入队；本地站点冒烟确认默认产生 `POST /login` 与 `GET /secret`，两个 `--no-*`
  开关能分别关掉。

### 内置字典精简与 --fuzz-dict 追加语义

- **内置字典 185 → 154 条**：删 23 组尾斜杠变体（`/admin` 与 `/admin/` 多数只差一次
  301）、与 robots/sitemap 引导重复的 `/robots.txt` `/sitemap.xml`、重复首页内容的
  `/index.html` `/index.php`、以及与 `OpenAPIProbePaths` 重叠的 4 条 spec 路径
  （`/api-docs`、`/openapi.json`、`/swagger.json`、`/swagger/v1/swagger.json`）。
  默认每 origin 少 31 次请求；文档数量口径同步改为 154。
- **`--fuzz-dict` 改为追加**：最终字典 = `BuiltinFuzzDict()` + 文件内路径，
  新增 `AppendFuzzDict` 统一补前导斜杠、保序去重，不再替换内置字典。
  这补回删除 `--known-file` 后「加几条自定义路径」的能力（其原语义本就是并入
  内置字典）。`--no-fuzz` 仍优先关闭。
- 测试：`TestBuiltinFuzzDict` 锁定无尾斜杠冗余，`TestAppendFuzzDict` /
  `TestParseArgsFuzzDictAppend` 锁定追加与跨列表去重。

### 探测类能力默认开启、KnownFiles 移除（破坏性变更）

- **库层 `DefaultOptions` 与 CLI 同步默认开启**：`FuzzPaths`、`ProbeOpenAPI`、
  `RouteExpand`、`DeobfForce`（`Deobfuscate` 本来就默认开）。对应关闭开关
  `--no-fuzz` / `--no-probe-openapi` / `--no-route-expand` / `--no-deobf`。
  正向开关 `--fuzz` / `--probe-openapi` / `--route-expand` / `--deobf` 全部移除。
  `--fuzz-dict` 只设置字典内容，不再隐含启用；`--no-fuzz` 优先级更高。
- **默认请求量显著上升**：每 origin 154（字典）+ 14（spec）+ 最多 50
  （`--route-expand-max`）次额外请求。要完全静态运行需一次性传齐 `--no-*`。
- **混淆探测链改为「先判断、再解混淆」**：命中签名走常规多轮解包；签名漏报
  也强制解包一次（Recover 对未混淆代码是 no-op，正常 JS 结果不变）。
  `--no-deobf` 同时关掉 `Deobfuscate` 与 `DeobfForce`。
- **删除 `Options.KnownFiles` 字段与 `--known-file` 参数**（库 API 破坏性变更）：
  内置字典已包含 `/.git/config`、`/actuator/env` 等固定路径，自定义路径统一
  走 `--fuzz-dict`；探测任务来源统一为 `fuzz`，`isProbeSource` 不再列
  `known-file`。
- 测试侧新增 `testNoProbeOptions()`，把功能用例与默认探测隔离，
  保证结果集只取决于被测链路；生产默认值不变。

### 输出过滤：ffuf 式状态码白/黑名单（破坏性变更）

- CLI 的 txt 与 JSONL 统一走 `-s, --status`（白名单）+ `-S, --filter-status`
  （黑名单），对齐 ffuf 的 `-mc` / `-fc`；判定顺序先黑名单命中丢弃、再看白名单。
  默认白名单 `200,204,301,302,307,401,403,405,500`（ffuf 默认码表），黑名单空。
  值支持精确码、范围（`200-299`）、`all`、`0`（未访问的线索）。
- **过滤点收成一个**：删掉硬编码 `effectiveResult` 与旧的 `-s` 独立判断。
  `StatusCode == 0` 不再是「不受 -s 影响」的例外，只是普通码；默认码表不含 0
  所以线索不输出，`-s 0` 可看。删除 `effective.go`，新增 `statusfilter.go`。
- 同一 Unreleased 周期内的临时开关 `--include-invalid` **未发布即移除**，
  等价写法是 `-s all`。
- **影响**：默认输出口径由「2xx/3xx/401/403」变为 ffuf 码表，**405、500 会重新
  出现在默认结果里**，其它 2xx/3xx（201/206/303 等）反而被排除；要严格只给下游
  可请求的 URL，显式写 `-s 200-399,401,403`。基线 / gate 需用同一开关重采；
  库层 `Crawl` 返回值与 `--testdata` 输入保持全量不变，外部导入者不受影响。

### 2026-11-11 默认执行与正确性收尾

- **破坏性变更：JS 执行默认开启，正向开关移除**。`--route-exec` 与
  `--deobf-exec` 不再存在；`Options.RouteExec` / `DeobfExec` 默认值改为
  `true`，关闭用 `--no-route-exec` / `--no-deobf-exec`。两条路径都有签名粗筛：
  bundle 执行要求 `LooksLikeBundle` + `LooksExecutable`，string-array 沙箱
  要求「数组声明 + 解码函数 + 调用点」专属签名，普通 JS 不进 VM。
  下面 W-2 / ESM / 沙箱等历史条目记录的是当时的 opt-in 状态，以本节为准。
- **ESM 执行链接通编排层**：`crawler.extractRouteGraph` 去掉 webpackChunk
  专属门槛与「静态为空才补跑」条件，静态/执行结果合并去重；新增根包
  爬虫层 ESM 集成测试（默认开、显式关、静态+动态混合）。
- **路由表两阶段父子树解析**：先收集对象帧与 `children` 关系再递归算路径，
  消除键顺序依赖，支持三层相对嵌套、空 `path`、隔层布局包装；`alias`
  字符串/数组产出 `Source=route-alias`。执行 loader 同步递归 children /
  展开 alias，并把 ESM 模块 id 转成可抓相对 chunk 路径；webpack 数字 ID
  由静态 `u` 函数表按 ID 对齐。
- **端到端执行预算与反混淆工作量上限**：packer `count` 钳到字典长度
  （4096 次循环 / 1MB 展开上限）、eval 展开个数与总长上限；沙箱与
  routegraph 执行统一 2MB 源码上限 + 2 秒 deadline，覆盖 `goja.Parse`、
  esbuild 转译、VM 运行与结果提取，外层 wall-clock 兜底。
- `go.mod` 经 `go mod tidy` 修正 direct/indirect 分类：直接依赖为 6 个，
  README / 合规段落同步修正「默认限速」「零第三方依赖」等失实口径，
  全部多字符 CLI 参数统一为 `--xx`。

### 变更

- **CLI 参数解析迁移到 kong**：命令行解析从标准库 `flag` 换为
  `github.com/alecthomas/kong`（结构体 tag 驱动，flag 定义/默认值/help
  集中在 cliSpec 一个类型上）。flag 名契约同步升级为 **GNU 风格**：
  多字符参数改双横线（`--max-urls`、`--deobf`、`--fsu`、`--ct`），
  单字符短名保留（`-u`、`-d`、`-c`、`-o`、`-O`、`-s`、`-v`、`-k`、
  `-i`、`-H`）；`=` 传值（`--max-urls=100`）与分组 help 输出为 kong
  带来的新能力。这是**破坏性变更**：批量脚本里的多字符单横线写法需要
  加一横。库 API（Options/Result/frontier 等）不受影响。

### 新增

- **混淆检测与轻量反混淆**（W-2）：新包 extractor/deobf。签名检测
  （Dean Edwards packer / `_0x` 十六进制标识符 / 字符串数组 / JJEncode /
  AAEncode / eval 字符串字面量），命中后自动解包（packer 还原、
  eval 字符串展开、`\xNN`/`\uNNNN` 转义解码）再走提取链，正常代码
  行为不变；`-deobf` 在自动判定漏报时强制启用。默认开启。
- **`__NEXT_DATA__` 结构化解析**（W-3）：inlinejson 在通用 walk 之外
  优先抽取 page 键与 pages/pageList 清单（Source 带 `:page` 后缀）；
  静态资源不作为 page 产出。
- **Angular 路由恢复**（W-4）：bundleMarkers 增加 forRoot/loadChildren/
  RouterModule；路由对象识别增加 loadChildren/canActivate 兄弟键；
  Angular 不带前导斜杠的 path（path:"admin"）自动补 / 后校验。
- **Vue Router 专项收尾**：bundleMarkers 增加 `vue-router`（纯路由
  chunk 无 webpack/vite 特征时兜底触发粗筛）；路由对象兄弟键增加
  `alias`（Vue Router 特有键）。其余 Vue 形态（Vite 动态 import、
  defineAsyncComponent、SSR 内联 JSON）由既有通用层覆盖，实测确认，
  不做执行兜底（goja 不支持 ESM，对 Vue 3 主流产物结构性失效）。
- **执行兜底支持 ESM/Vite**：此前 goja 不认 import/export，对 Vue 3 等
  Vite 生态产物结构性失效。现在先 goja 脚本模式解析判定，ESM 报语法错误
  时经 esbuild Transform（新增依赖 github.com/evanw/esbuild）转 CJS 后
  执行：静态 import 转 require 桩（路由感知 stub）、动态 import( 由词法器
  改写为 __import 桩并记录模块 id 作为 chunk 候选。二进制 +5.6MB
  （33.27→38.83，metrics 口径）。
- **string-array 沙箱 `-deobf-exec`**（opt-in）：javascript-obfuscator 家族
  的 _0x 数组 + 解码函数此前刻意不做；现在源码在 goja 沙箱执行一遍建立
  解码器运行时，词法调用点（`_0x1a2b(0x1f)` 形态）逐个求值并替换为真实
  字符串，下游常规提取即可见。vm.Interrupt 超时兜底（2 秒）、替换上限
  5000；self-defending/顶层异常不硬 abort。会执行目标站代码，与
  `-route-exec` 同风险档，默认关闭。
- **路由嵌套拼接**：routeTables 帧栈增加绝对前缀传播，children
  相对子路由（users/:id）与父 path（/admin）拼成真实可达地址
  /admin/users/:id；无 path 的布局包装跳过继承祖父前缀；子 path
  以 / 或 * 开头视为绝对路径不拼。参数实例化仍由 RouteExpand 控制。
- **inlinejson 确定性修复**：nextDataPages 产出按发现深度降序、
  同深度字典序排列，消除 map 遍历随机序导致嵌套具体页与顶层
  模板页（`[slug]`）顺序不定（`TestNextDataPages` 30 次复现挂 8 次）。

- **路径 fuzz `-fuzz` / `-fuzz-dict`**（W-1）：对每个 origin 探测约
  185 条常见入口路径（管理后台/API 网关/配置备份/CMS/云凭证泄漏），
  命中页面纳入正常爬取；`-fuzz-dict` 可用自定义字典替换内置。
  探测类来源（fuzz/known-file/openapi-probe）404 即 miss，不产出
  结果不做正文提取，不污染输出。

- **相似 URL 过滤 `-fsu`**（V-9）：接入 winezer0/requestfilter 的
  覆盖参数策略 + 动态路径段泛化（阈值 10，对齐 katana -fsu 默认），
  同形状动态段（/users/123 ≈ /users/456）只保留首条；输出层实现，
  与 scope/状态码过滤同链路；非法 URL fail-open，过滤器故障不吞产出；
  结束输出 `[fsu]` 统计行。

- **输出类型 `-O` 与自动命名**：`-O json`（JSONL，默认）| `-O txt`
  （纯 URL）；`-jsonl` 保留为旧别名。`-o` 支持父目录不存在自动创建；
  未指定 `-o` 时按首个种子 URL 自动命名（host+path 特殊符号转下划线
  + 时间戳后缀防覆盖，扩展名随 `-O`）。
- **接口路由表 method 识别**（V-3）：routegraph 路由对象新增 `method`
  键识别（`{method:'post', path:'/api/x'}` → Method=POST），path/url/
  endpoint 均可作为路径键，非 HTTP 动词同名词（如 timeout）不误判；
  调用点侧 axios/fetch/ajax 的方法推断此前已实现。带 method 的路由
  走「接口形状」输出（Confidence=interface），不再一律 GET。
- **`-mdp` 每域名页面数上限**（V-4）：单 host 的导航 HTML 页面数封顶，
  防爬虫陷阱；抓取前原子预留配额，并发下不超卖。0 = 不限。
- **`-s` 状态码过滤显示**（V-6）：如 `-s 200,403` 只显示命中状态码的
  已抓取结果；无状态码的线索类结果不受影响；`all`/省略 = 不过滤。
- **`-ct` 总时长上限**（V-5a）：如 `-ct 5m`、`-ct 90`（裸数字=秒）；
  到点后与 Ctrl+C 走同一条收敛路径，已采集结果照常输出；`0`/省略 = 不限。
  到点截断会在 `[done]` 汇总中显式标注（`[limit] time budget reached`）。


### 新增

- **scope 五档命名**（批次 U）：`-scope rdn|fqdn|dn|regex|all`，
  对齐 hakrawler 语义；旧值 `subdomain`/`same-host`/`any` 继续兼容。
  新增 `-scope-regex` 配合 `-scope regex` 给自定义正则（匹配完整 URL）。
- **作用域双层生效**：scope 除约束爬取导航入队外，现在也过滤输出
  显示——范围外结果（含已抓取的跨域资产）一律不打印。种子始终
  作为起点抓取，不受范围判定拦截（此前 `-scope regex` 下种子不匹配
  正则会导致整个爬取中止）。



### 新增

- **`-dr` 不跟随重定向**：首个 3xx 响应直接返回，跳转目标仍记录进
  Redirects（重定向目标本身是资产，只是不追进去抓）。
- **`-i` 只爬种子路径之下**：种子 `https://example.com/en-US/` 时
  `/en-US/` 之外的同站路径不入队；robots Disallow 线索同口径过滤。
  种子 host 的路径限制不影响 `-scope` 对子域/根域的范围判定，严格路径内
  采集用 `-i -scope same-host` 组合。
- 说明：URL 的父页信息已由 JSONL 输出的 `referer` 字段提供，无需新增字段。

## v0.4.0（2026-11-10）

### 结构

- **extractor 拆分为子包**：每类额外解析一个子包（`sourcemap` / `modulemap` /
  `openapi` / `nextjs` / `form` / `curl` / `inlinejson` / `json`），共享类型
  （`Request` / `ParamValue` / `LimitReport`）与通用小工具独立为 `extractor/base`；
  根包只留载体分发编排（`FromHTML` / `FromJS` / `FromHeaders` / robots /
  sitemap / curl 命令归属 / 内容类型判定）。
- **对外 API 零改动**：根包用类型别名 + 函数值转发旧符号（`compat.go`），
  crawler / testfill / jsext / cmd 的 import 不需要任何修改。新代码建议直接
  import `extractor/<类型>` 子包。
- 测试随包走：截断统计用例移到各自子包，「FromHTML 分派与回退」类端到端
  用例留在根包（`inline_dispatch_test.go`），职责与被测对象一致。
- 子包覆盖率：curl 92.5% / inlinejson 94.0% / json 93.2% / modulemap 93.4% /
  openapi 91.6% / nextjs 86.7% / sourcemap 85.2% / form 74.0%。

## v0.3.1（2026-11-09）

批次 R（先量后改）：用两轮真实站体检定位「同层重复抓取」与两处
ESM 部署形态的载体缺口。

### 正确性与配额

- **同层重复任务去重（R-1 / N-4）**：`frontier` 的去重只在入队时生效，
  同一 URL 出现在同层多个父页里时会各抓一次、各产出一行，并多占一份
  `MaxURLs` 配额（配额是硬上限，重复一次就是一次召回损失）。新增
  `dedupeTasks` / `dedupeAndMark`，在每层派发前与「引导任务合并后」各去重一次。
  实测（-d 3 / 500）：vite.dev 重复行 10→3、vuejs.org 13→6、
  remix.run 101→0（按路径口径）；`vite.dev/blog` 的 sitemap(depth 1) 与
  js-ast(depth 2) 双抓消除。
- **残留说明**：仍有个位数重复，来自「同层并发抓取期间」被并行父页同时
  发现的候选（两批任务在 runLevel 内并发，去重发生在批次边界）。彻底消除
  需要把去重下移到派发通道，代价是串行化热点；当前按「可接受残留」记录。

### 召回能力

> 提取结果的**顺序不保证**（imports/scopes 是 JSON 对象，按 map 迭代）。
> 需要稳定序请自行排序——CI 曾因用例断言顺序而在 Linux 上间歇变红。

- **`<script type="importmap">` 提取（R-1 / N-2）**：新增
  `extractor/modulemap`（`ImportMapRequests` / `parseImportMap`）。
  按规范处理 imports / scopes 两层与字符串、数组（fallback）、嵌套对象
  三种右值形态；裸模块名（`react`）不是地址，不收。真实站实测：
  docs.remix.run 单页 54 条、remix.run/jam/2026 93 条，其中 91 条 live 且
  完全不在既有结果里。**H-0「importmap 不做」的结论作废**（当时八站样本
  都不含 importmap，属「测了没有」而非「没有」）。
- **`//# sourceMappingURL` 声明（R-1 / N-2）**：新增 `SourceMapRequests`，
  JS 与 CSS 两个分支接入。既有实现只在响应本身是 `.map` 时解析其 sources，
  从不去取声明的那个文件。实测 petstore3 的 `swagger-ui.css.map` 200 且漏检。
  位置限定在尾部 8KB（避免把工具链字符串里的同形文本当成声明），
  `data:` 内联不产出。

### 体检裁定（不做）

- **N-3 尾斜杠 / index 变体归一化**：7 份结果、1600+ 行逐一枚举成对 URL，
  **0 组**。`CleanPath` 已保留尾部斜杠语义，frontier 已折叠 `./..//` 与
  默认端口。前提不成立，不做。
- **N-1 同站二进制内嵌路径**：4 站二进制响应仅 6 条（全部是 ico / wasm /
  octet-stream），朴素抽取候选 0 条 live。样本不足且有替代路径（WASM/PDF
  的地址面通常由 JS/HTML 先行暴露），暂记「不做」，留档 `r1-dedup`。



判据修正带来的召回能力变化（三项此前被否决的能力重新立项并落地）。

### 判据修正（本版的核心）

项目以资产发现为目标，**漏检是唯一不可接受的失败**。此前把「噪音率」
当第一判据，否决了三条能拿到真实资源的路径。现在统一为：能拿到 URL
资源就做，噪音交给 deferPage 降优先级与开关控制；唯一仍成立的否决
理由是「前提不成立」。

### 召回能力

- **空产出回退（Q-2）**：`adaptEngine` 在链中**非末位**引擎「解析成功
  但零产出」时继续下探下一个引擎。AST 对压缩/非常规语法可能合法解析
  却提不出请求，此前就直接返回空结果。实测 remix.run 净增 4 条，零
  新增噪音。**末位**引擎的空产出仍是最终结论——那代表「已验证为空」，
  回退正则会引入 AST 刻意排除的前缀碎片（`var base="/api/v3"`）。
- **OpenAPI opt-in 探测（Q-4）**：新开关 `-probe-openapi`（默认关闭）。
  打开后每个 origin 试 14 个标准 spec 路径，命中则提取 paths×方法×参数名
  为接口形状（`Source=openapi`，`Confidence=template`，emit-only 交
  testfill）。petstore 实测 14/14 操作全提取。默认关闭是因为暴露率仅
  2.4%（P-1），不该让所有人付这份配额。
- **sourcemap sources 配额保护（Q-3）**：归一化本就完整，本次把该来源
  纳入 `deferPage` 延后分区——单站可产 202 条路径（O-4 实测），不延后
  会挤掉主资源召回。

### 修复

- `-js-engine` 非法值不再静默退化：拼错的名字此前落到 auto 分支，
  用户以为指定了 goja 其实不是；现在白名单校验并报错。
- OpenAPI template 分支丢失 `Params`/`ParamValues`（spec 的意义正是
  「形状 + 参数名」）；`{petId}` 被 URL 编码成 `%7BpetId%7D` → 转 `:param`。

### 测试与文档

- 新增 goja / treesitter 引擎子包测试：覆盖率 **0.0% → 57.7% / 54.7%**。
  实测锁定两引擎行为相反（treesitter 容错解析、不拒 ESM；goja 严格）。
- cmd 覆盖率 40% → 41.1%（flag 边界表驱动）。
- 闸门报告入 `tools/samples/`：`q2-recall-gate`、`q4-openapi-probe` 及原始 jsonl。

## v0.2.1（2026-11-09）

修复与库工效补强（无新抓取能力，semver patch）。

- **input type 维度填充（P-2）**：`ParamValue` 新增 `Type` 字段（additive）；
  文本类 input（text/email/password/number/date/tel/url/search）此前整体
  不收集，现收集为 `Kind=input:<type>`，`FillParams` 按 type 选值形态
  （type 优先于参数名，text/search 回退名字推断）——M 批次「自动填充
  输入框」的原始需求至此完整。
- **表单接口修正（P-2b/P-2c）**：① SiteHosts 过滤收窄为「同站 + 资产
  路径才滤」，单页应用的同站接口（/login、/api/items）不再被误滤；
  ② 表单 action（POST）改为 emit-only（`Confidence=interface`）——
  GET 抓取只会得到 404 噪音；③ POST/PUT/PATCH 的表单参数进
  form-urlencoded body（此前错误拼 query）。
- **routegraph 缺陷修复（P-3）**：`rhsRange` 不把函数体闭合当表达式
  结束，`o.u=function(e){...}` 后换行的代码被收进 chunk 模板——
  真实 bundle 里 u 后面总跟着模块表，修复后 chunk 路径不再污染。
  同批补 godoc 可运行示例（ExampleExtract / ExampleLooksLikeBundle）。
- **闸门留档**：OpenAPI spec 暴露率（tools/openapi.py）——总体 2.4%
  （主流站 0% / API 子集 12.5% 仅 Swagger demo 站）→ 不做；
  多文档站复验（GitHub 33 endpoint/页 vs Stripe/GitLab 1/页）。

## v0.2.0（2026-11-09）

新增「采集 → 填充 → 确认」完整链路与工程收口。

- **版本号（V-1）**：CLI `-version`（`version (commit <vcs.time>)`，
  构建时 `-ldflags -X` 注入）；`.github/workflows/release.yml`：tag
  触发双平台构建 + SHA256SUMS + GitHub Release。
- **测试数据填充（批次 M，新包 `testfill`）**：`:id`/`{id}`/`${id}`/
  全大写占位符填充；表单/查询参数按名推断（select/hidden 静态候选
  优先）；JSON body 按 key 填充；确定性取值源（seeded，每 kind
  独立计数，可复现）；四渲染器 curl/python/postman(v2.1)/http/json。
- **接口确认（批次 N）**：`fetcher.Send` 泛化（任意方法/body/重定向
  方法规则）；`-testdata-send` 发送确认（幂等方法默认，非幂等需
  `-testdata-send-unsafe`）；确认状态进产物注释与 `[confirm]` 汇总。
- **curl 代码块提取**：`extractor.ParseCurl` + `CurlCommands`，
  `Kind=curl` 结果（Raw 保真、不入队）——文档站（GitHub REST 实测
  单页 33 endpoint）的 endpoint 主要来源。
- **真实场景验证（批次 O-1~O-3）**：OpenAI 官方文档 Cloudflare 403
  （记不做）；docs.github.com 等效站全链路实测；二段路径闸门 6 站
  （34/35 漏提为 404 → 不做）；按真实数据补 SiteHosts 资产过滤、
  线索来源过滤、`[confirm]` 汇总。
- **配额收口（O-5）**：噪音过滤「丢弃」改「降优先级」（remix 三态
  A/B：40.2%→14.5%、200 集合 177→253、误伤 27→14）；分页链 boost
  优先（计算型 3 站全链 5/5 漏检 0）；route-expand 激进版噪音率
  实测 9.1%（维持默认关）。
- **闸门留档**：sourcemap sources 归一化（live 3% → 不做）；
  二段路径放宽（噪音 97% → 不做）。

## v0.1.0（2026-11-09）

首个语义化版本。公共 API 面已冻结：`New / Crawl / Close / Limits /
DefaultOptions / Options / Result / OnResult / OnError`。

- **库化（批次 J）**：模块迁移到 `github.com/winezer0/assetcrawler`，
  可 `go get`；`Crawler` 契约为「一实例一采集」（二次 `Crawl` 显式报错）；
  `Close` 后 `Crawl` 报 closed（修复了此前不生效的检查）；根包新增可运行
  `Example`（godoc 可见）。
- **三个实测闸门与实现（批次 I）**：`tools/noise.py`（remix.run 噪音率
  40.2% → js-ast 三类高噪音候选过滤，默认开、`-no-noise-filter` 回退）；
  `tools/pagination.py` + `extractor/json_pagination.go`（计算型分页
  page/nbPages、has_more、continue、skip/limit 四规则）；
  `tools/mapexposure.py`（Majestic 前 100 站 .map 暴露率 15.4%，
  sourcemap 提取保持默认开）。
- **测量闭环与实测闸门（批次 G/H）**：`tools/drift.py` 基线漂移自动裁定；
  CI 升级 v7 + 覆盖率 + Windows 构建；提取截断可观测（`[limit]` 统计）；
  `tools/carriers.py` 八站载体增量实测（7 个有效站独有 URL 全 0，
  否决 H-1~H-3 拍脑袋规则）；`-stable-host-order` 确定性排序（默认关）。
- **正确性与文档（批次 E/F）**：探针夹具与身份校验；RSC 流解析；
  内联 hydration JSON；chunk 相对锚定；可复现基线。
- **召回与准确（批次 A~D）**：常量按作用域隔离；import 别名映射方法；
  og/twitter meta 与 data-* 泛化；sourcemap sources；表单参数推断；
  routegraph 静态层（chunk + 路由表）；goja 执行兜底；jsext 双引擎。

### 已知限制（有意的设计取舍，均记录于 ISSUES.md）

- AST 引擎按「段数 ≥3 或末段含扩展名」判定端点，2 段路径
  （`/api/orders`）仅正则引擎提取，引擎间行为不一致；
- js-ast 噪音过滤会误伤真实部署的 `.pnpm`/`.ts` 路径（A/B 净收益为正，
  误伤明细见 STATUS 第十节）；
- goja 不解析 ESM（`import/export`），遇 ESM 显式报错走 treesitter。

## v0.1.0 之前

以 `git log` 为准（批次 A~I 均直接提交至 main）。
