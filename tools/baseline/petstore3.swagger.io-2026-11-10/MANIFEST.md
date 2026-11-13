# 基线：petstore3.swagger.io-2026-11-10

采集日期：2026-11-10 18:34
目标：`https://petstore3.swagger.io/`（OpenAPI 哨兵站）
代码：v0.4.0（`aca3379`，extractor 拆包后）

```bash
./basecrawler.exe -u https://petstore3.swagger.io/ -d 2 -max-urls 300 -rl 5 -c 5 \
    -timeout 15 -jsonl -o raw/basecrawler.jsonl -silent
```

| 指标 | 值 |
|---|---:|
| 抓取总数 | 45 |
| stable 归一条数 | 45 |

基线用途：哨兵——`-probe-openapi` 下应稳定产出 14/14 操作
（Q-4 E2E 口径）；`sourceMappingURL`（R-1c）下 `swagger-ui.css.map`
不应漏检。行数小、结构稳定，任何明显波动都值得人工复核。
