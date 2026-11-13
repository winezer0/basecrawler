# 基线：nuxt.com-2026-11-10

采集日期：2026-11-10 18:33
目标：`https://nuxt.com/`（Nuxt hydration JSON + `./x.js` 相对 chunk）
代码：v0.4.0（`aca3379`，extractor 拆包后）

```bash
./assetcrawler.exe -u https://nuxt.com/ -d 2 -max-urls 300 -rl 5 -c 5 \
    -timeout 15 -jsonl -o raw/assetcrawler.jsonl -silent
```

| 指标 | 值 |
|---|---:|
| 抓取总数 | 312（超配额属正常，bootstrap 并入） |
| stable 归一条数 | 312 |

基线用途：inline-json（`__NUXT_DATA__`）、normalizeChunkPath 相对形态、
deferPage 降优先级三能力的回归参照。
