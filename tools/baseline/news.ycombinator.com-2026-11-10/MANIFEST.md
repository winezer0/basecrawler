# 基线：news.ycombinator.com-2026-11-10

采集日期：2026-11-10 18:35
目标：`https://news.ycombinator.com/`（2000 年代服务端渲染站，反向参照）
代码：v0.4.0（`aca3379`，extractor 拆包后）

**网络约束**：本机直连 HN 超时（curl exit 28），采集经本机代理
`HTTPS_PROXY=http://127.0.0.1:10808` 完成。对拍复跑需同样走代理，
否则结果是网络不可达不是能力回退。

```bash
HTTPS_PROXY=http://127.0.0.1:10808 ./basecrawler.exe \
    -u https://news.ycombinator.com/ -d 2 -max-urls 300 -rl 5 -c 5 \
    -timeout 15 -jsonl -o raw/basecrawler.jsonl -silent
```

| 指标 | 值 |
|---|---:|
| 抓取总数 | 306 |
| stable 归一条数 | 306 |

基线用途：反向参照——无构建产物、无 ESM 的老式站，现代提取器
不应在本站产出大量噪音（404 候选）；增量应接近 0。
