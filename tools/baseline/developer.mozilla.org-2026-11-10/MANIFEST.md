# 基线：developer.mozilla.org-2026-11-10

采集日期：2026-11-10 18:33
目标：`https://developer.mozilla.org/en-US/`（多子域 + CSP 线索大户）
代码：v0.4.0（`aca3379`，extractor 拆包后）

```bash
./basecrawler.exe -u https://developer.mozilla.org/en-US/ -d 2 -max-urls 300 -rl 5 -c 5 \
    -timeout 15 -jsonl -o raw/basecrawler.jsonl -silent
```

| 指标 | 值 |
|---|---:|
| 抓取总数 | 293 |
| stable 归一条数 | 293 |

基线用途：CSP 线索（48 来源主机）与大型多子域内容站的调度回归参照。
