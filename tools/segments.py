#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""segments.py —— 二段路径闸门（批次 O-2）。

回答一个具体问题：AST 引擎（默认 auto 链）的 `endpointShaped` 要求
路径段数 ≥3，`/api/users` 这类 **2 段路径**（REST 最常见形态）会被
静默丢弃，而正则引擎没有这个限制。放宽到 2 段的净增量与噪音各是多少？

方法（与 pagination.py 同框架）：
  1. 对每个站点分别用 `-js-engine auto` 与 `-js-engine regex` 各跑一次；
  2. 差集 regex − auto 里的 2 段路径 = auto 的漏提；
  3. 对漏提 URL 实抓，200 记 live、4xx/5xx 记 dead——噪音率决定去留。

判据：missed 占 auto 结果比例超阈值（默认 5%）且 live 占多数 →
放宽 `endpointShaped` 到 2 段；否则记「不做」。

用法：

  python tools/segments.py --sites tools/segments.sites.json \
      --bin ./basecrawler.exe --proxy http://127.0.0.1:10808 \
      --work-dir /tmp/o2 --json /tmp/o2/segments.json
"""
import argparse
import json
import os
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import baseline as B  # noqa: E402


def path_segments(url):
    """URL 路径的段列表（去查询串、去首尾空段）。纯函数。"""
    path = urllib.parse.urlsplit(url).path
    return [s for s in path.split("/") if s]


def missed_two_seg(auto_urls, regex_urls):
    """regex 有而 auto 无的 2 段路径 URL（排序保证确定性）。纯函数。"""
    out = []
    for u in regex_urls - auto_urls:
        if len(path_segments(u)) == 2:
            out.append(u)
    return sorted(out)


def fetch_status(url, proxy="", timeout=15):
    """实抓取状态码；网络错误返回 0。"""
    handlers = []
    if proxy:
        handlers.append(urllib.request.ProxyHandler({"http": proxy, "https": proxy}))
    opener = urllib.request.build_opener(*handlers)
    req = urllib.request.Request(url, headers={"User-Agent": "basecrawler-segments-gate/1.0"})
    try:
        with opener.open(req, timeout=timeout) as resp:
            return resp.status
    except urllib.error.HTTPError as e:
        return e.code
    except Exception:
        return 0


def crawl_urls(bin_path, url, out_path, engine, proxy="", max_urls=120, depth=2):
    """跑一次采集，返回结果 URL 集合。"""
    cmd = [bin_path, "-u", url, "-d", str(depth), "-max-urls", str(max_urls),
           "-c", "3", "-timeout", "15", "-js-engine", engine,
           "-jsonl", "-o", out_path, "-silent"]
    if proxy:
        cmd += ["-proxy", proxy]
    import subprocess
    subprocess.run(cmd, check=False, timeout=600,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    got = set()
    if os.path.exists(out_path):
        with open(out_path, encoding="utf-8", errors="ignore") as fh:
            for line in fh:
                try:
                    got.add(json.loads(line)["url"])
                except (ValueError, KeyError):
                    continue
    return got


def probe_site(site, bin_path, work_dir, proxy=""):
    """单站：双引擎对比 → 漏提 2 段 → 实抓验证。"""
    auto = crawl_urls(bin_path, site["url"],
                      os.path.join(work_dir, site["name"] + "-auto.jsonl"), "auto", proxy)
    regex = crawl_urls(bin_path, site["url"],
                       os.path.join(work_dir, site["name"] + "-regex.jsonl"), "regex", proxy)
    missed = missed_two_seg(auto, regex)
    live = dead = 0
    for u in missed:
        code = fetch_status(u, proxy)
        if 200 <= code < 300:
            live += 1
        else:
            dead += 1
        time.sleep(0.2)
    return {
        "name": site["name"],
        "url": site["url"],
        "auto": len(auto),
        "regex": len(regex),
        "missed": missed,
        "missed_live": live,
        "missed_dead": dead,
    }


def render_report(rows, threshold=0.05):
    """markdown 汇总 + 闸门裁定。纯函数。"""
    lines = ["# 二段路径闸门（批次 O-2）", ""]
    lines.append(f"阈值：2 段漏提占 auto 结果 ≥ {threshold:.0%} 且 live 占多数才放宽。")
    lines.append("")
    lines.append("| 站 | auto | regex | 2 段漏提 | live | dead |")
    lines.append("|---|---:|---:|---:|---:|---:|")
    total_missed = total_live = 0
    worth = []
    for r in rows:
        ratio = (len(r["missed"]) / r["auto"]) if r["auto"] else 0.0
        lines.append("| {} | {} | {} | {} ({:.1%}) | {} | {} |".format(
            r["name"], r["auto"], r["regex"], len(r["missed"]), ratio,
            r["missed_live"], r["missed_dead"]))
        total_missed += len(r["missed"])
        total_live += r["missed_live"]
        if ratio >= threshold and r["missed_live"] > r["missed_dead"]:
            worth.append(r)
    lines.append("")
    lines.append(f"- 合计漏提 {total_missed} 条，其中 live {total_live} 条。")
    lines.append("")
    if worth:
        lines.append("## 裁定：有站点超阈值且 live 占多数 → 立项放宽 endpointShaped 到 2 段")
        for r in worth:
            for u in r["missed"][:3]:
                lines.append(f"- **{r['name']}** 漏 `{u}`")
    else:
        lines.append("## 裁定：无站点超阈值或 live 不占多数 → 记「不做」")
    return "\n".join(lines) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--sites", required=True, help="站点表 json")
    ap.add_argument("--bin", default="./basecrawler.exe")
    ap.add_argument("--proxy", default="")
    ap.add_argument("--work-dir", required=True)
    ap.add_argument("--json", default="")
    ap.add_argument("--threshold", type=float, default=0.05)
    args = ap.parse_args(argv)

    os.makedirs(args.work_dir, exist_ok=True)
    with open(args.sites, encoding="utf-8") as fh:
        sites = json.load(fh)

    rows = []
    for site in sites:
        try:
            rows.append(probe_site(site, args.bin, args.work_dir, args.proxy))
        except Exception as exc:
            rows.append({"name": site["name"], "url": site["url"], "auto": 0,
                         "regex": 0, "missed": [], "missed_live": 0,
                         "missed_dead": 0, "error": "{}: {}".format(type(exc).__name__, exc)})

    print(render_report(rows, args.threshold), end="")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump({"threshold": args.threshold, "sites": rows}, fh,
                      ensure_ascii=False, indent=2)
        print(f"[json] {args.json}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
