# tools —— 对比测试用的外部工具与基线

这个目录存在的目的只有一个：**让「这次跑出了什么」可复现、可对照**。

爬虫类工具的行为会随版本漂移（提取规则、默认深度、并发模型都在变），
如果只保留结论不保留当时的二进制和原始输出，过两周就无法判断
「结果变少了」到底是自己改坏了，还是对端工具/目标变了。

## 目录结构

```
tools/
├── README.md                                  本文件
├── baseline.py                                快照 / 对比脚本
├── bin/                                       对比用的工具二进制（离线存档）
│   ├── assetcrawler.exe
│   ├── cariddi.exe
│   ├── gospider.exe
│   ├── hakrawler.exe
│   └── URLFinder.exe
└── baseline/
    └── testasp-2026-11-06/                    基线
        ├── MANIFEST.md                        本次基线的环境与命令记录
        ├── stats.json                         机器可读的统计
        ├── urls-all.txt                       所有工具合并后的站内 URL
        ├── urls-<tool>.txt                    各工具单独的站内 URL
        └── raw/                               各工具的原始输出
```

入库说明：基线文本、`baseline.py`、`metrics.py` 与各份 `raw/*.jsonl` 都在版本库里；
`bin/` 下 5 个二进制共约 91 MB **不入库**，只在本地离线存档，校验和见下表。
换机器时按各份 `MANIFEST.md` 记录的版本自行获取。

`metrics.py` 负责生成 STATUS.md 引用的行数 / 用例数 / 覆盖率，
避免文档把可变量写死后过期（ISSUES P2-5）：

```
python tools/metrics.py            # 行数、用例数、二进制体积
python tools/metrics.py --cover    # 额外跑 go test -cover ./...
```

## 工具清单

全部为 Windows/amd64 可执行文件，均以 Go 1.26.4 构建。

| 工具 | 版本 | 体积 | 来源 |
|---|---|---|---|
| assetcrawler | 本地构建（devel） | 见 `tools/metrics.py` | 本仓库 |
| cariddi | v1.4.6 | 19.3 MB | `github.com/edoardottt/cariddi` |
| gospider | v1.1.6 | 19.7 MB | `github.com/jaeles-project/gospider` |
| hakrawler | v0.0.0-20260805040537-52a16fe61bd1 | 17.2 MB | `github.com/hakluke/hakrawler` |
| URLFinder | v0.0.0-20260617041451-da01795e3ff8（Update 2026.6.16） | 10.1 MB | `github.com/pingc0y/URLFinder` |

SHA-256 校验和（用于确认二进制没有被替换或重新编译）：

```
9d3e8aa25cbbbffabdd93e3292f8b62d2b36b1e612bfb6f97845ec805ccc3dd8  URLFinder.exe
c46bb3ca4f0077f42f7a0e56a382993b69bd5eb85a78700b3a6e1f849f8f9938  assetcrawler.exe
26da72dfbc45e26fa2703de31f06a09f6fe467cd70cec14260f04e29e07e46ed  cariddi.exe
ee797812fb38b0be8882fd6de126780863cac07a901383a98b30fca686c18016  gospider.exe
d9564b9260436d34b2919d1c3a33535556d59bc09db96e24353e5fc28a8b147b  hakrawler.exe
```

版本号来自二进制内嵌的 Go build info（`go version -m <exe>`），
比各工具自己的 `-version` 输出更可信——hakrawler 甚至没有 version 参数。

> `assetcrawler.exe` 每次重新构建后校验和都会变，对比时应记录构建时间或
> git commit，而不是依赖这个哈希。

## 复现命令

统一口径：深度 3、并发 10、超时 10s，目标 `http://testasp.vulnweb.com`。

```bash
T=http://testasp.vulnweb.com

# hakrawler（从 stdin 读目标；-u 只输出唯一 URL，-s 显示来源）
echo $T | ./bin/hakrawler.exe -d 3 -t 10 -timeout 10 -u -s > hakrawler.txt

# gospider（注意 -d 默认是 1，必须显式给）
./bin/gospider.exe -s $T -d 3 -c 10 -m 10 > gospider.txt

# cariddi（-md 才是深度，-d 是延迟；从 stdin 读目标）
echo $T | ./bin/cariddi.exe -md 3 -c 10 -t 10 -plain > cariddi.txt

# URLFinder 正常模式
./bin/URLFinder.exe -u $T -m 1 -s all -t 10 -time 5 > urlfinder_m1.txt

# URLFinder 深入模式（约 190s，会跟到外链站点）
./bin/URLFinder.exe -u $T -m 2 -s all -t 10 -time 5 > urlfinder_m2.txt

# assetcrawler
./bin/assetcrawler.exe -u $T -d 3 -c 10 -timeout 10 -jsonl -o assetcrawler.jsonl -silent
```

## 用法

```bash
# 把一批新结果固化成基线
python baseline.py snapshot <raw_dir> -o tools/baseline/<名字>

# 现代站点：再生成一份 hash 归一化基线（app.<HASH>.js）
python baseline.py stable <raw_dir> -o tools/baseline/<名字> --host vuejs.org

# 用新结果和基线比，看多了什么少了什么（有丢失会以非 0 退出）
python baseline.py diff tools/baseline/testasp-2026-11-06c <raw_dir>
```

`stable` 只动扩展名前的最后一段且要求长度 >= 6：`app.B91ytTIH.js` 会归一，
`app.js` / `vue.global.js` 不动。已知不覆盖双扩展名形态
（`index.md.7c5b3f77.lean.js`），这类文件每次部署都变，
对比时会成对出现在增删两侧，按成对即可判定为部署漂移。

`diff` 的价值在于**发现回退**：只要某个工具相对基线少了 URL，就会逐条列出并以
退出码 1 结束，可以直接挂到 CI 里。

## 已知环境问题

`gospider.exe` 在本机（Windows 10 / Go 1.26.4）**无法完成抓取**。
用 depth=1/3、并发 1/5/10、`-q`、`--js=false`、`-o` 输出目录共 5 种组合尝试，
均零输出、零文件、无报错，直到超时被杀；`--version` 正常。
基线里它的结果是 0，这是环境问题而非工具能力，比对时不要当成「gospider 抓不到」。

## 多工具横向对比

`cmp-2026-11-06/` 是 2026-11-06 的一次爬虫横向对比（katana / crawlergo / assetcrawler），
原始输出与结论文档见该目录下的 `REPORT.md`。

对比脚本：

```bash
python tools/compare_tools.py --target <目标> \
    name1=结果文件 name2=结果文件 ...
```

支持解析 katana 的 `-jsonl`、crawlergo 的 `--output-mode json`、
assetcrawler 的 `-jsonl`、tanscan 的 `/api/v1/scan/task/urls` 响应。

## 现代站点基线（ESM / Vite）

现行基线：`testasp-2026-11-06c`（传统站，67 条）、`vuejs.org-2026-11-06`（297）、
`vite.dev-2026-11-06`（293）。`testasp-2026-11-06` / `-b` 已失效，仅作历史。

> 注意：`testasp` 是会自己删帖的靶站，2026-11-07 同时段复采只剩 40 条，
> 消失的 27 条是 `id=5..13` 的帖子三件套。该基线**不要直接拿来判回归**，
> 先按下面的 SOP 用旧二进制同时段复跑一次。

`baseline/vuejs.org-2026-11-06/` 与 `baseline/vite.dev-2026-11-06/` 是两个
Vite 构建站点的采集基线，用于回归「chunk 恢复、ESM 解析、噪音控制」。

与 `testasp-2026-11-06` 的区别：

| | testasp | 现代站点 |
|---|---|---|
| 站点形态 | 传统 ASP，几乎无 JS | Vite + ESM，chunk 按路由分割 |
| 文件名 | 稳定 | 带内容哈希，每次部署都变 |
| 回归对比 | `urls-*.txt` | **`urls-stable.txt`（hash 归一化）** |

```bash
cd tools
python baseline.py diff baseline/vuejs.org-2026-11-06 <新结果目录>
# 现代站点先比 urls-stable.txt，否则会把部署产生的 hash 变更当成新增/消失
```

首次采集（2026-11-06）同时暴露并修复了三类噪音：data URI 里的 `url(%23x)`、
`__vite__mapDeps` 的路径前缀、JS 里的 MIME/rel/省略号字面量。
404 数量：vuejs.org 32 → 8，vite.dev 11 → 3。

## 基线漂移裁定 SOP

`baseline.py diff` 只要出现消失项就会以非 0 退出，这对**代码回退**是对的，
但对**目标站自己变了**会误报（删帖、部署换 hash、`v3` / `v5` 子域切换、
配额饱和时的子域抽样）。裁定流程固定为四步，全部用同一代理、同一时段串行跑：

```bash
OLD=tools/bin/assetcrawler.exe        # 本地离线存档，对应基线提交 d17a6a4
NEW=./assetcrawler.exe
P=http://127.0.0.1:10808             # 换成自己可用的代理
FLAGS="-u https://vuejs.org/ -d 2 -max-urls 300 -rl 5 -c 5 -timeout 15 -jsonl -silent -proxy $P"

$OLD $FLAGS -o /tmp/old1.jsonl
$NEW $FLAGS -o /tmp/new1.jsonl
$OLD $FLAGS -o /tmp/old2.jsonl       # 旧二进制连跑，量噪声
$NEW $FLAGS -o /tmp/new2.jsonl       # 新二进制连跑，量噪声
```

然后用 `baseline.py` 里的 `canonical()` / `stable_of()` / `read_set()` 对四份
JSONL 与 `urls-assetcrawler.txt` 求站内集合差（口径必须与基线一致）：

| 比较 | 含义 |
|---|---|
| 旧1 → 新1 | 唯一变量是代码版本；差集应能被站点变化解释 |
| 旧1 → 旧2、新1 → 新2 | **同二进制连跑噪声下限**；跨版本差集不大于它就无回退 |
| 基线 → 旧1 | 旧代码跨日漂移；若它比跨版本差集还大，说明站点在动 |

判据（2026-11-07 实测值见 `STATUS.md` 第四节）：

1. 总数是否撞到 `-max-urls` 配额；撞到了，差集更可能只是抽样不同。
2. 差集是否**成对**且按子域 / hash 整组出现；单侧孤立消失才可疑。
3. 差集里有没有主域 URL 的系统性丢失（外链脚本、logo、路由页）。
4. 同一条带 hash 的产物在旧 / 新同时段是否完全一致；一致即为站点部署变化。

三步都过不了才算代码回退。**不要把 `urls-stable.txt` 当成万能解**：
`stable_of` 只归一化扩展名前最后一段，双扩展名产物仍会成对增删。

## 漂移裁定与载体量化（2026-11-08 新增）

| 脚本 | 作用 | 纯函数单测 |
|---|---|---|
| `drift.py` | 把上面的 SOP 自动化：旧1/新1/旧2/新2 四方差集、子域分组、四档裁定 | `test_drift.py` |
| `carriers.py` | 量化 9 类现代前端载体里「现有结果没有」的 URL | `test_carriers.py` |
| `carriers.sites.json` | H-0 抽样的 8 个站点清单 | — |
| `drift.example.json` | spec 模板，换成自己的样本路径即可用 | — |

```bash
# 离线裁定已有样本；--run 则先按 spec 跑四次采集
python tools/drift.py --manifest tools/drift.example.json
python tools/drift.py --manifest spec.json --run --out-dir /tmp/drift
python tools/drift.py --manifest spec.json --json

# 抓载体样本 -> 离线分析
python tools/carriers.py fetch --sites tools/carriers.sites.json \
    --out-dir /tmp/h0 --proxy http://127.0.0.1:10808
python tools/carriers.py analyze --sites tools/carriers.sites.json \
    --sample-dir /tmp/h0 --results-dir /tmp/h0 --json /tmp/h0/carriers.json

# tools/ 下全部纯函数单测
python -m unittest discover -s tools -p 'test_*.py' -v
```

`drift.py` 分四档（零差异 / 无回退证据 / 需人工复核 / 疑似代码回退），
**刻意不给出「一定没回退」**：后两档会逐条列出「旧代码两次稳定持有、
新代码两次都缺」的 URL 交人工判读。入库报告见 `samples/drift-2026-11-07.json`
与 `samples/carriers-2026-11-08.json`。

`carriers.py` 有两个口径陷阱，H-0 实测时都踩过并修了：

1. 站内过滤必须同时认种子 host 与重定向后的 host（`kit.svelte.dev` →
   `svelte.dev`），否则结果集被滤成 0，造出一批假「独有 URL」；
2. 结果不足 5 条的样本（`turbo.build` 首页是纯文本）标 `sample_valid=false`，
   独有数不参与裁定。

## 噪音 / 分页 / sourcemap 闸门（2026-11-08 新增，批次 I）

| 脚本 | 作用 | 纯函数单测 |
|---|---|---|
| `noise.py` | 每站 4xx 占比、404 的来源与扩展名分布，超阈值才立项治理 | `test_noise.py` |
| `pagination.py` | 分页漏检闸门：对照组（显式 next / Link 头）验能力，计算型量化代价 | `test_pagination.py` |
| `pagination.sites.json` | 6 个含分页的公开 API 站清单 | — |
| `mapexposure.py` | Majestic 前 N 站生产 sourcemap 暴露率抽样（断点续跑） | `test_mapexposure.py` |

```bash
# 噪音率：复用任意一轮采集的 jsonl 样本
python tools/noise.py --dir /tmp/h0 \
    --hosts "astro.build,qwik.dev,angular.dev,kit.svelte.dev,remix.run,docs.solidjs.com,nuxt.com,turbo.build" \
    --json /tmp/h0/noise.json

# 分页闸门：每站自动跑「解析链 + 现状采集」并比对
python tools/pagination.py --sites tools/pagination.sites.json \
    --bin ./assetcrawler.exe --proxy http://127.0.0.1:10808 \
    --work-dir /tmp/i2 --json /tmp/i2/pagination.json

# sourcemap 暴露率：Majestic 前 100 域名（支持断点续跑）
python tools/mapexposure.py --list majestic-head.csv --top 100 \
    --proxy http://127.0.0.1:10808 --work-dir /tmp/i3 \
    --json /tmp/i3/mapexposure.json
```

三个脚本的共同口径：**先数据后规则**。noise.py 的阈值（默认 10%）决定
要不要立项降噪；pagination.py 的对照组判据是「第 2 页被请求」（JSON 数据
URL 的级联提取会饿死配额，链尾缺失是配额竞争不是能力缺口）；mapexposure.py
只发尾部 Range 请求读 `sourceMappingURL`，再探测 `.map` 可达性。
入库报告见 `samples/noise-2026-11-08.json`、`samples/pagination-2026-11-08.json`、
`samples/mapexposure-2026-11-08.json` 与抽样榜单 `samples/majestic-top100-2026-11-08.csv`。

三个闸门的裁定与后续实现（`noisefilter.go`、`extractor/json_pagination.go`）
见 `STATUS.md` 第十节；remix 噪音过滤 A/B 的原始输出在
`samples/remix-noisefilter-{on,off}-2026-11-08.jsonl`。

## 基线扩站与重采（2026-11-13）

`baseline.sites.json` 是站点清单与固定采集口径的**唯一数据源**，`drift.py`
与 `rebaseline.py` 共用。5 个站点（Vite / Nuxt / MDN / OpenAPI / Remix）覆盖
ESM chunk、hydration、大站子域轮换、spec 探测与 RSC flight。

为什么要固定口径：2026-11-11 起字典 fuzz、spec 探测、路由展开、testfill、
safety 全部默认开启，`-s` 又默认按白名单过滤。不显式关掉这些能力，新旧
二进制的差集几乎全是默认行为变化，不是代码回退。清单里的 flags 一次性
全关主动能力并用 `-s all`，与 11-10 基线口径对齐。

```bash
# 只打印将要执行的命令，不发任何请求、不写文件
python tools/rebaseline.py

# 确认后才真正采集；产物写 tools/rebaseline-out/，不覆盖既有基线
python tools/rebaseline.py --run --bin ./assetcrawler.exe \
    --proxy http://127.0.0.1:10808

# 对拍（旧/新各跑两次，自动裁定）
python tools/drift.py --manifest tools/drift.example.json --run
```

重采后的 `*.canonical.txt` 需人工核对再决定是否发布为正式基线。本轮**不建
定时任务**，对拍由人工或外部调度触发。`gate.json` 是覆盖率/用例数门禁，与
这里的 URL 召回基线是两套东西，别混。
