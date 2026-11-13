# 基线：remix.run-2026-11-10

采集日期：2026-11-10 18:33
目标：`https://remix.run/`（RSC/hydration + importmap 的现代站点）
代码：v0.4.0（`aca3379`，extractor 拆包后）

```bash
./basecrawler.exe -u https://remix.run/ -d 2 -max-urls 300 -rl 5 -c 5 \
    -timeout 15 -jsonl -o raw/basecrawler.jsonl -silent
```

| 指标 | 值 |
|---|---:|
| 抓取总数 | 300（配额饱和） |
| stable 归一条数 | 300 |

基线用途：importmap 载体（R-1b 增量 91 条 live）与 RSC 提取的回归参照。
对比用 `python tools/baseline.py diff <本目录> <新 raw 目录>`。
