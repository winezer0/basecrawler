#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""mapexposure.py —— 生产环境 sourcemap 暴露率抽样（批次 I-3）。

回答一个具体问题：热门站点的前端 JS 里，有多少真的带
`sourceMappingURL`、其中又有多少 `.map` 文件公开可达？
sourcemap 提取（B-3）的默认优先级由这个比例决定，不靠感觉。

抽样口径：

- 榜单：Majestic Million 前 N 域名（外部公开榜单，可复现）；
- 每站：抓首页 HTML -> 取前 `--js-per-site` 个 <script src> 指向的 JS
  （只发尾部 Range 请求，64 KB 足够读到末尾的 sourceMappingURL 注释）；
- 分类：`file`（.map 地址，需再探测可达性）/ `inline`（data: URI）；
- 暴露率 = map 可达的站 / 有效站（HTML 与 JS 至少各拿到一个的站）。

用法：

  python tools/mapexposure.py --list majestic-head.csv --top 100 \
      --proxy http://127.0.0.1:10808 --work-dir /tmp/i3 \
      --json /tmp/i3/mapexposure.json
"""
import argparse
import base64
import json
import os
import re
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path

SMAP_RE = re.compile(
    r"//[#@]\s*sourceMappingURL=([^\s\n\r]+)", re.I)
SCRIPT_SRC_RE = re.compile(
    r"""<script\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']""", re.I)
JS_EXT_RE = re.compile(r"\.js(?:\?|$)|\.mjs(?:\?|$)", re.I)

UA = "assetcrawler-mapexposure/1.0"


def tail_of(data, n):
    """取字节串尾部 n 字节。纯函数。"""
    return data[-n:] if len(data) > n else data


def sourcemap_urls(js_text, base_url):
    """从 JS 文本提取 sourceMappingURL，返回 [(kind, url)]。纯函数。

    kind: "file"（需探测可达性）或 "inline"（data: URI，内容已在 JS 里）。
    """
    out = []
    for raw in SMAP_RE.findall(js_text):
        if raw.startswith("data:"):
            out.append(("inline", raw))
        else:
            out.append(("file", urllib.parse.urljoin(base_url, raw)))
    return out


def script_srcs(html_text, base_url=None):
    """提取 <script src> 里的 JS 地址（只取 .js/.mjs）。纯函数。"""
    srcs = []
    for raw in SCRIPT_SRC_RE.findall(html_text):
        if not JS_EXT_RE.search(raw):
            continue
        srcs.append(urllib.parse.urljoin(base_url, raw) if base_url else raw)
    return srcs


def _open(url, proxy="", timeout=15, range_tail=None, max_read=5 << 20):
    handlers = []
    if proxy:
        handlers.append(urllib.request.ProxyHandler(
            {"http": proxy, "https": proxy}))
    opener = urllib.request.build_opener(*handlers)
    headers = {"User-Agent": UA, "Accept-Encoding": "identity"}
    if range_tail:
        headers["Range"] = "bytes=-{}".format(range_tail)
    req = urllib.request.Request(url, headers=headers)
    with opener.open(req, timeout=timeout) as resp:
        data = resp.read(max_read)
        partial = resp.status == 206 or resp.headers.get("Content-Range") is not None
    return data, partial


def fetch_page(domain, proxy=""):
    url = "https://" + domain + "/"
    data, _ = _open(url, proxy)
    return url, data.decode("utf-8", "replace")


def probe_js(url, proxy=""):
    """拉 JS 尾部，返回其 sourceMappingURL。服务器忽略 Range 时读全量再取尾。"""
    data, partial = _open(url, proxy, range_tail=65536)
    if not partial:
        data = tail_of(data, 65536)
    return sourcemap_urls(data.decode("utf-8", "replace"), url)


def probe_map(url, proxy=""):
    """探测 .map 可达性：200 且非空即视为暴露。
    网络错误/非 200 一律 False（调用方只关心可达性）。"""
    try:
        data, _ = _open(url, proxy, max_read=2048)
        return len(data) > 0
    except Exception:
        return False


def probe_site(domain, proxy="", js_per_site=6, sleep=0.3):
    """单站探测。返回可 JSON 化的结果字典。"""
    res = {"domain": domain, "ok": False, "js_seen": 0, "map_ref": 0,
           "map_inline": 0, "map_reachable": 0, "errors": [],
           "maps": []}
    try:
        page_url, html = fetch_page(domain, proxy)
    except Exception as exc:
        res["errors"].append("page: {}".format(exc))
        return res
    js_urls = script_srcs(html, page_url)[:js_per_site]
    res["js_seen"] = len(js_urls)
    if not js_urls:
        res["errors"].append("page: no external js")
        return res
    for ju in js_urls:
        try:
            found = probe_js(ju, proxy)
        except Exception as exc:
            res["errors"].append("js {}: {}".format(ju, type(exc).__name__))
            continue
        time.sleep(sleep)
        for kind, mu in found:
            if kind == "inline":
                res["map_inline"] += 1
                res["maps"].append({"kind": "inline", "js": ju})
                continue
            res["map_ref"] += 1
            entry = {"kind": "file", "js": ju, "map": mu, "reachable": False}
            try:
                entry["reachable"] = probe_map(mu, proxy)
                if entry["reachable"]:
                    res["map_reachable"] += 1
            except Exception as exc:
                entry["error"] = type(exc).__name__
            res["maps"].append(entry)
    res["ok"] = True
    return res


def load_domains(list_path, top):
    """读 Majestic CSV，取前 top 个域名。"""
    domains = []
    with open(list_path, encoding="utf-8", errors="ignore") as fh:
        header = fh.readline()
        if "Domain" not in header:
            fh.seek(0)
        for line in fh:
            parts = line.strip().split(",")
            if len(parts) >= 3 and "." in parts[2]:
                domains.append(parts[2])
            if len(domains) >= top:
                break
    return domains


def render_report(sites):
    """markdown 汇总 + 裁定。纯函数。"""
    valid = [s for s in sites if s["ok"]]
    failed = [s for s in sites if not s["ok"]]
    n = len(valid)
    ref = sum(1 for s in valid if s["map_ref"] > 0 or s["map_inline"] > 0)
    reach = sum(1 for s in valid if s["map_reachable"] > 0)

    lines = ["# sourcemap 暴露率抽样（批次 I-3）", ""]
    lines.append("榜单前 {} 站；有效站 {}（拿到 HTML 且有外链 JS）；"
                 "失败 {}。".format(len(sites), n, len(failed)))
    lines.append("")
    lines.append("| 指标 | 站数 | 占有效站 |")
    lines.append("|---|---:|---:|")
    rate = (lambda x: "{:.1%}".format(x / n) if n else "-")
    lines.append("| JS 带 sourceMappingURL（file 型） | {} | {} |".format(
        sum(1 for s in valid if s["map_ref"] > 0), rate(
            sum(1 for s in valid if s["map_ref"] > 0))))
    lines.append("| JS 带 inline data: map | {} | {} |".format(
        sum(1 for s in valid if s["map_inline"] > 0), rate(
            sum(1 for s in valid if s["map_inline"] > 0))))
    lines.append("| **.map 公开可达（暴露）** | **{}** | **{}** |".format(
        reach, rate(reach)))
    lines.append("")
    if failed:
        names = ", ".join(s["domain"] for s in failed[:12])
        lines.append("失败站（前 12）：" + names)
        lines.append("")
    pct = (reach / n) if n else 0.0
    if pct >= 0.10:
        verdict = ("裁定：暴露率 {:.1%} ≥ 10% —— sourcemap 提取保持默认开、"
                   "值得继续投入（如 sources 归一化覆盖）").format(pct)
    elif pct > 0:
        verdict = ("裁定：暴露率 {:.1%} 低于 10% —— sourcemap 提取保持现状"
                   "（默认开、低优先级），不追加专项投入").format(pct)
    else:
        verdict = "裁定：暴露率 0% —— sourcemap 提取对热门站无增量，评估默认关闭"
    lines.append("## " + verdict)
    return "\n".join(lines) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--list", required=True, help="Majestic CSV 路径")
    ap.add_argument("--top", type=int, default=100, help="抽样站数")
    ap.add_argument("--proxy", default="", help="HTTP(S) 代理")
    ap.add_argument("--work-dir", required=True, help="进度/中间产物目录")
    ap.add_argument("--json", default="", help="结果 JSON 输出路径")
    ap.add_argument("--js-per-site", type=int, default=6)
    ap.add_argument("--sleep", type=float, default=0.3, help="请求间基础间隔秒")
    args = ap.parse_args(argv)

    os.makedirs(args.work_dir, exist_ok=True)
    domains = load_domains(args.list, args.top)

    progress_path = os.path.join(args.work_dir, "progress.jsonl")
    done = set()
    if os.path.exists(progress_path):
        with open(progress_path, encoding="utf-8") as fh:
            for line in fh:
                try:
                    done.add(json.loads(line)["domain"])
                except (ValueError, KeyError):
                    continue

    sites = []
    for i, d in enumerate(domains):
        if d in done:
            continue
        res = probe_site(d, args.proxy, args.js_per_site, args.sleep)
        sites.append(res)
        with open(progress_path, "a", encoding="utf-8") as fh:
            fh.write(json.dumps(res, ensure_ascii=False) + "\n")
        print("[{}/{}] {} ok={} ref={} reach={}".format(
            i + 1, len(domains), d, res["ok"], res["map_ref"],
            res["map_reachable"]), file=sys.stderr)
        time.sleep(args.sleep)

    # 合并断点续跑的历史结果
    if done:
        with open(progress_path, encoding="utf-8") as fh:
            for line in fh:
                try:
                    sites.append(json.loads(line))
                except ValueError:
                    continue
        seen = set()
        merged = []
        for s in sites:
            if s["domain"] not in seen:
                seen.add(s["domain"])
                merged.append(s)
        sites = merged

    print(render_report(sites), end="")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump(sites, fh, ensure_ascii=False, indent=2)
        print("[json] {}".format(args.json), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
