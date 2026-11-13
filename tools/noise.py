#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""noise.py —— 采集结果噪音率量化（批次 I-1）。

回答一个具体问题：现有采集结果里，4xx（主要是 404）占已请求 URL 的比例
是多少、集中在哪个提取来源、什么扩展名？噪音率超阈值才值得立项治理，
否则记「不做」。

口径（与 H-0 / carriers.py 一致，避免「结果数」歧义）：

- 一行 = 一个被发现或被请求的 URL；`status` 缺失表示已发现但未请求
  （-max-urls 截断的队列尾部），不计入噪音率分母。
- 噪音率 = 4xx / 已请求。3xx 由 fetcher 跟随重定向，不视为噪音。

用法：

  python tools/noise.py --dir /tmp/h0 --json tools/samples/noise-DATE.json
  python tools/noise.py --dir /tmp/h0 --threshold 0.05
"""
import argparse
import glob
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

# 表驱动：4xx URL 的扩展名归组。新增扩展只加一行。
# 「source」组的 404 多为构建产物里残留的源码模块路径，部署后必然 404。
EXT_GROUPS = {
    ".ts": "source", ".tsx": "source", ".jsx": "source", ".vue": "source",
    ".svelte": "source", ".coffee": "source", ".elm": "source",
    ".js": "asset", ".mjs": "asset", ".cjs": "asset", ".css": "asset",
    ".map": "asset", ".json": "asset", ".png": "asset", ".jpg": "asset",
    ".jpeg": "asset", ".svg": "asset", ".gif": "asset", ".webp": "asset",
    ".woff": "asset", ".woff2": "asset", ".ttf": "asset", ".ico": "asset",
}


def parse_jsonl(text):
    """逐行解析 jsonl，坏行跳过。纯函数。"""
    rows = []
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except ValueError:
            continue
    return rows


def extension_of(url):
    """取路径最后一段的后缀（小写）；查询串不参与；无后缀返回空串。"""
    path = url.split("?", 1)[0].split("#", 1)[0]
    last = path.rsplit("/", 1)[-1]
    if "." not in last:
        return ""
    return "." + last.rsplit(".", 1)[-1].lower()


def ext_group(ext):
    """扩展名归组；表驱动，未知扩展归 other，无扩展归 none。"""
    return EXT_GROUPS.get(ext, "other") if ext else "none"


def _top(counter, n=3):
    return sorted(counter.items(), key=lambda kv: (-kv[1], kv[0]))[:n]


def site_stats(rows):
    """单站统计。纯函数：输入行列表，输出可 JSON 化的统计字典。"""
    fetched = [r for r in rows if r.get("status") is not None]
    s4xx_rows = [r for r in fetched if 400 <= r.get("status", 0) < 500]
    src_counter = {}
    ext_counter = {}
    for r in s4xx_rows:
        src = r.get("source", "?")
        src_counter[src] = src_counter.get(src, 0) + 1
        g = ext_group(extension_of(r.get("url", "")))
        ext_counter[g] = ext_counter.get(g, 0) + 1
    return {
        "rows": len(rows),
        "fetched": len(fetched),
        "pending": len(rows) - len(fetched),
        "s4xx": len(s4xx_rows),
        "s404": sum(1 for r in fetched if r.get("status") == 404),
        "noise_rate": (len(s4xx_rows) / len(fetched)) if fetched else 0.0,
        "n4xx_sources": _top(src_counter),
        "n4xx_ext_groups": _top(ext_counter),
    }


def render_report(stats_list, threshold=0.10):
    """渲染 markdown 报告并给出裁定。纯函数。"""
    lines = ["# 噪音率量化（批次 I-1）", ""]
    lines.append(f"阈值：4xx / 已请求 ≥ {threshold:.0%} 才立项治理。")
    lines.append("")
    lines.append("| 站 | 行数 | 已请求 | 未请求 | 4xx | 404 | 噪音率 |")
    lines.append("|---|---:|---:|---:|---:|---:|---:|")
    over = []
    for s in stats_list:
        rate = s["noise_rate"]
        flag = " **超阈值**" if rate >= threshold else ""
        lines.append(
            "| {} | {} | {} | {} | {} | {} | {:.1%}{} |".format(
                s.get("host", "?"), s["rows"], s["fetched"], s["pending"],
                s["s4xx"], s["s404"], rate, flag,
            )
        )
        if rate >= threshold:
            over.append(s)
    lines.append("")
    if over:
        lines.append("## 裁定：超阈值，立项治理")
        lines.append("")
        for s in over:
            src = "、".join(f"`{k}`×{v}" for k, v in s["n4xx_sources"]) or "无"
            ext = "、".join(f"`{k}`×{v}" for k, v in s["n4xx_ext_groups"]) or "无"
            lines.append(
                f"- **{s.get('host', '?')}**（{s['noise_rate']:.1%}）："
                f"4xx 来源 {src}；扩展名组 {ext}。"
            )
        lines.append("")
        lines.append("治理方向由上表的来源 / 扩展名分布决定，先数据后规则。")
    else:
        lines.append("## 裁定：全部低于阈值，噪音治理记「不做」")
        lines.append("")
        lines.append("现有 4xx 占比在阈值以内，不引入降噪规则（避免误伤召回）。")
    return "\n".join(lines) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--dir", required=True, help="样本目录（含 <host>.jsonl）")
    ap.add_argument("--hosts", default="", help="逗号分隔的 host 列表；缺省扫描目录全部 *.jsonl")
    ap.add_argument("--threshold", type=float, default=0.10, help="噪音率阈值（默认 0.10）")
    ap.add_argument("--json", default="", help="统计结果 JSON 输出路径")
    args = ap.parse_args(argv)

    if args.hosts:
        hosts = [h.strip() for h in args.hosts.split(",") if h.strip()]
        paths = [os.path.join(args.dir, h + ".jsonl") for h in hosts]
    else:
        paths = sorted(glob.glob(os.path.join(args.dir, "*.jsonl")))
        hosts = [os.path.splitext(os.path.basename(p))[0] for p in paths]

    stats_list = []
    for host, path in zip(hosts, paths):
        if not os.path.exists(path):
            print(f"skip {host}: {path} 不存在", file=sys.stderr)
            continue
        with open(path, encoding="utf-8", errors="ignore") as fh:
            rows = parse_jsonl(fh.read())
        s = site_stats(rows)
        s["host"] = host
        stats_list.append(s)

    print(render_report(stats_list, args.threshold), end="")
    if args.json:
        payload = {
            "threshold": args.threshold,
            "sites": stats_list,
        }
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False, indent=2)
        print(f"[json] {args.json}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
