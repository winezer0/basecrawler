#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""sourcemap.py —— sourcemap sources 归一化闸门（批次 O-4）。

回答一个具体问题：`FromSourceMap` 提取的 sources 里，各种形态
（`webpack://repo/./src/a.ts`、`../src/a.ts`、`/src/a.ts`、裸文件名）
各占多少？归一化成可抓 URL 后，实际能抓到的（live）有多少？

I-3 已证明热门站 15.4% 暴露 `.map`；本闸门决定 sources 归一化
（去掉 webpack:// 前缀、按 map URL 解析相对路径）值不值得做。

判据：归一化后可抓增量里 live 占比 ≥ 50% 才实现归一化；
否则记「不做」（sources 多为构建期路径，归一化产出 404）。

用法：

  python tools/sourcemap.py --domains github.com,wordpress.org \
      --proxy http://127.0.0.1:10808 --work-dir /tmp/o4 \
      --json /tmp/o4/sourcemap.json
"""
import argparse
import json
import os
import sys
import time
import urllib.parse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import mapexposure as M  # noqa: E402


def classify_source(s):
    """sources 条目形态分类。纯函数。
    顺序有讲究：node_modules 是内容特征（归一化后仍是构建路径），
    比 webpack:// 协议前缀更有信息量，先判。"""
    if "node_modules/" in s:
        return "node_modules"
    if s.startswith("webpack://"):
        return "webpack"
    if s.startswith("./") or s.startswith("../"):
        return "relative"
    if s.startswith("/"):
        return "absolute"
    if "/" not in s:
        return "bare"
    return "relative"


def normalize_source(s, map_url):
    """把 source 归一化成绝对 URL；无法定位返回空串。纯函数。"""
    if s.startswith("webpack://"):
        # webpack://<repo>/<path>：取 <path> 部分按 map URL 解析
        rest = s[len("webpack://"):]
        _, _, path = rest.partition("/")
        if not path:
            return ""
        s = path
    if "://" in s:
        return s  # 已是绝对地址
    if "/" not in s:
        return ""  # 裸文件名无法定位
    return urllib.parse.urljoin(map_url, s)


def probe_domain(domain, proxy="", js_per_site=6, max_fetch=40):
    """单域：找 .map → 解析 sources → 归一化 → 实抽验证。"""
    res = {"domain": domain, "maps": 0, "sources": 0,
           "normalizable": 0, "fetched_live": 0, "forms": {}, "samples": []}
    try:
        page_url, html = M.fetch_page(domain, proxy)
    except Exception:
        return res
    js_urls = M.script_srcs(html, page_url)[:js_per_site]
    fetched = 0
    for ju in js_urls:
        try:
            found = M.probe_js(ju, proxy)
        except Exception:
            continue
        time.sleep(0.2)
        for kind, mu in found:
            if kind != "file":
                continue
            # 取 map JSON 的 sources
            try:
                data, _ = M._open(mu, proxy, max_read=1 << 20)
                doc = json.loads(data.decode("utf-8", "replace"))
            except Exception:
                continue
            sources = doc.get("sources") or []
            res["maps"] += 1
            res["sources"] += len(sources)
            for s in sources:
                if not isinstance(s, str):
                    continue
                form = classify_source(s)
                res["forms"][form] = res["forms"].get(form, 0) + 1
                norm = normalize_source(s, mu)
                if not norm:
                    continue
                res["normalizable"] += 1
                if fetched >= max_fetch:
                    continue
                fetched += 1
                try:
                    code = M.probe_map(norm, proxy)
                except Exception:
                    code = False
                if code:
                    res["fetched_live"] += 1
                    if len(res["samples"]) < 5:
                        res["samples"].append(norm)
                time.sleep(0.2)
    return res


def render_report(rows):
    """markdown 汇总 + 裁定。纯函数。"""
    lines = ["# sourcemap sources 归一化闸门（批次 O-4）", ""]
    lines.append("| 域 | map 数 | sources | 可归一化 | 实抓 live |")
    lines.append("|---|---:|---:|---:|---:|")
    tot_src = tot_norm = tot_live = 0
    for r in rows:
        lines.append("| {} | {} | {} | {} | {} |".format(
            r["domain"], r["maps"], r["sources"], r["normalizable"], r["fetched_live"]))
        tot_src += r["sources"]
        tot_norm += r["normalizable"]
        tot_live += r["fetched_live"]
    lines.append("")
    lines.append(f"- 合计 sources {tot_src} 条，可归一化 {tot_norm} 条，实抓 live {tot_live} 条。")
    lines.append("")
    forms = {}
    for r in rows:
        for k, v in r["forms"].items():
            forms[k] = forms.get(k, 0) + v
    if forms:
        lines.append("形态分布：" + "、".join(f"`{k}`×{v}" for k, v in
                                            sorted(forms.items(), key=lambda kv: -kv[1])))
        lines.append("")
    live_ratio = (tot_live / tot_norm) if tot_norm else 0.0
    if tot_norm > 0 and live_ratio >= 0.5:
        lines.append(f"## 裁定：live 占比 {live_ratio:.0%} ≥ 50% → 立项实现 sources 归一化")
    else:
        lines.append(f"## 裁定：live 占比 {live_ratio:.0%} 低于 50% → 记「不做」")
    return "\n".join(lines) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--domains", required=True, help="逗号分隔域名")
    ap.add_argument("--proxy", default="")
    ap.add_argument("--work-dir", required=True)
    ap.add_argument("--json", default="")
    args = ap.parse_args(argv)

    os.makedirs(args.work_dir, exist_ok=True)
    rows = []
    for d in [x.strip() for x in args.domains.split(",") if x.strip()]:
        rows.append(probe_domain(d, args.proxy))

    print(render_report(rows), end="")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump({"sites": rows}, fh, ensure_ascii=False, indent=2)
        print(f"[json] {args.json}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
