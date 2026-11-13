#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""pagination.py —— 响应体分页「漏检量」闸门（批次 I-2）。

回答一个具体问题：对含分页的公开 API 站点，assetcrawler 现状会漏掉多少
「下一页」URL？按三类分页形态分别量化：

- explicit     显式字段（next / next_url / nextPage…）——FromJSON 遍历全部
               字符串值，理论上已天然覆盖，作为对照组；
- link-header  响应头 Link rel="next"——FromHeaders 已提取，同为对照组；
- computed     计算型（page+1 / has_more / continue 合并）——响应体里没有
               下一页地址，必须构造，是真正的候选缺口。

闸门判据：对照组漏检应为 0（验证既有覆盖）；computed 漏检量即
「不实现响应体分页构造的代价」，有数据才立项，零增量记「不做」。

用法：

  python tools/pagination.py --sites tools/pagination.sites.json \
      --bin ./assetcrawler.exe --proxy http://127.0.0.1:10808 \
      --work-dir /tmp/i2 --json /tmp/i2/pagination.json
"""
import argparse
import gzip
import io
import json
import os
import re
import subprocess
import sys
import urllib.parse
import urllib.request
from pathlib import Path

# 表驱动：显式「下一页」字段名。新框架的新字段只加一行。
NEXT_FIELDS = (
    "next", "next_url", "next_href", "nextpage", "nextPage", "next_page",
    "nextLink", "next_link", "after_url",
)

LINK_NEXT_RE = re.compile(r'<([^>]+)>\s*;\s*rel="next"', re.I)

KIND_NAMES = {
    "explicit": "显式字段",
    "computed": "计算型",
    "link-header": "Link 头",
    "none": "无分页",
}


def classify(kind):
    return KIND_NAMES.get(kind, kind)


def _usable(u):
    return isinstance(u, str) and len(u) > 1 and (
        u.startswith(("http://", "https://")) or u.startswith("/")
    ) and not u.startswith("//")


def _abs(raw, base):
    return urllib.parse.urljoin(base, raw)


def next_from_body(node, base, depth=0):
    """显式字段：递归找 NEXT_FIELDS 里的可用地址。纯函数。"""
    out = []
    if depth > 8:
        return out
    if isinstance(node, dict):
        for k, v in node.items():
            if k in NEXT_FIELDS and _usable(v):
                out.append(_abs(v, base))
            else:
                out.extend(next_from_body(v, base, depth + 1))
    elif isinstance(node, list):
        for v in node:
            out.extend(next_from_body(v, base, depth + 1))
    return out


def _set_query(url, updates):
    parts = urllib.parse.urlsplit(url)
    q = urllib.parse.parse_qsl(parts.query, keep_blank_values=True)
    d = dict(q)
    d.update(updates)
    return urllib.parse.urlunsplit(parts._replace(query=urllib.parse.urlencode(d)))


def _norm_url(u):
    """URL 规范化比较键：query 参数排序。纯函数。

    Go 与 Python 的编码顺序不同，直接字符串比对会把
    「已覆盖」误判成「漏检」。"""
    parts = urllib.parse.urlsplit(u)
    q = sorted(urllib.parse.parse_qsl(parts.query, keep_blank_values=True))
    return urllib.parse.urlunsplit(parts._replace(
        query="&".join("{}={}".format(k, v) for k, v in q)))


def same_url(a, b):
    """参数顺序无关的 URL 相等判定。纯函数。"""
    return _norm_url(a) == _norm_url(b)


def next_computed(doc, url):
    """计算型：page+1 / has_more / continue 合并。纯函数。

    三条规则都要求「能从现有 URL 构造下一页」，构造不出来的不产出。
    """
    if not isinstance(doc, dict):
        return []
    out = []
    parts = urllib.parse.urlsplit(url)
    q = dict(urllib.parse.parse_qsl(parts.query, keep_blank_values=True))
    page = doc.get("page")
    nb = doc.get("nbPages")
    if isinstance(page, int) and isinstance(nb, int) and page < nb - 1:
        out.append(_set_query(url, {"page": page + 1}))
    has_more = doc.get("has_more")
    if has_more is True and "page" in q:
        try:
            out.append(_set_query(url, {"page": int(q["page"]) + 1}))
        except ValueError:
            pass
    cont = doc.get("continue")
    if isinstance(cont, dict) and cont:
        out.append(_set_query(url, cont))
    # skip/limit/total 形态（dummyjson 这类）：skip+limit < total 时构造下一跳
    skip, limit, total = doc.get("skip"), doc.get("limit"), doc.get("total")
    if all(isinstance(x, int) for x in (skip, limit, total)) and skip + limit < total:
        out.append(_set_query(url, {"skip": skip + limit, "limit": limit}))
    return out


def next_from_link_header(value, base):
    """Link 头 rel="next"。纯函数。"""
    if not value:
        return []
    return [_abs(m, base) for m in LINK_NEXT_RE.findall(value)]


def parse_next(doc, headers, url):
    """一页的下一页候选，按形态归类。纯函数。"""
    found = {"explicit": [], "computed": [], "link-header": []}
    found["explicit"] = next_from_body(doc, url)
    found["computed"] = next_computed(doc, url)
    for hv in headers.get_all("Link", []) if headers else []:
        found["link-header"].extend(next_from_link_header(hv, url))
    return found


def fetch(url, proxy="", timeout=20, retries=2):
    """抓一页，返回 (headers, json_doc, body_text)。失败重试，应对代理偶发 SSL 中断。"""
    handlers = []
    if proxy:
        handlers.append(urllib.request.ProxyHandler(
            {"http": proxy, "https": proxy}))
    opener = urllib.request.build_opener(*handlers)
    req = urllib.request.Request(url, headers={
        "User-Agent": "assetcrawler-pagination-gate/1.0",
        "Accept": "application/json",
    })
    last = None
    for _ in range(retries + 1):
        try:
            with opener.open(req, timeout=timeout) as resp:
                raw = resp.read()
                if resp.headers.get("Content-Encoding", "") == "gzip":
                    raw = gzip.GzipFile(fileobj=io.BytesIO(raw)).read()
                text = raw.decode("utf-8", "replace")
                return resp.headers, json.loads(text) if text else None, text
        except Exception as exc:
            last = exc
    raise last


def crawl(bin_path, url, out_path, proxy="", max_urls=1000, depth=5):
    """跑一次现状采集（seed 即分页第一页），返回结果 URL 集合。

    两个口径修正，避免把测量条件误报成能力缺口：
    - depth 与链长对齐：链上第 N 页的发现深度是 N，`-d 1` 会把对照组
      的自动翻页截在第 2 页；
    - 固定 `-no-csp`：CSP 允许域会吃掉一半配额（pokeapi 实测 198/200）。
    """
    cmd = [bin_path, "-u", url, "-d", str(depth), "-max-urls", str(max_urls),
           "-rl", "5", "-c", "3", "-timeout", "15", "-jsonl", "-o", out_path,
           "-silent", "-no-csp"]
    if proxy:
        cmd += ["-proxy", proxy]
    subprocess.run(cmd, check=False, timeout=300,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    got = set()
    if os.path.exists(out_path):
        with open(out_path, encoding="utf-8", errors="ignore") as fh:
            for line in fh:
                try:
                    got.add(_norm_url(json.loads(line)["url"]))
                except (ValueError, KeyError):
                    continue
    return got


def probe_site(site, bin_path, work_dir, proxy="", max_pages=5):
    """单站闸门：解析首页三类形态 → 沿链走 N 页 → 与现状采集比对。"""
    url = site["url"]
    headers, doc, _ = fetch(url, proxy)
    found = parse_next(doc, headers, url)

    chain = [url]
    cur, cur_doc, cur_headers = url, doc, headers
    for _ in range(max_pages - 1):
        nxt = parse_next(cur_doc, cur_headers, cur)
        candidates = nxt["explicit"] + nxt["link-header"] + nxt["computed"]
        if not candidates:
            break
        cur = candidates[0]
        if cur in chain:
            break
        chain.append(cur)
        try:
            cur_headers, cur_doc, _ = fetch(cur, proxy)
        except Exception:
            break

    got = crawl(bin_path, url, os.path.join(work_dir, site["name"] + ".jsonl"),
                proxy, max_urls=max_pages * 200, depth=max_pages)
    missed = [u for u in chain if _norm_url(u) not in got]
    # 对照组的能力判据：第 2 页被请求即证明「next 被提取并入队」。
    # 链后续页会被 JSON 数据 URL 的级联提取饿死配额（pokeapi 每个
    # pokemon 详情又引出上百 URL），那是配额竞争，不是能力缺口。
    page2_covered = len(chain) > 1 and _norm_url(chain[1]) in got
    return {
        "name": site["name"],
        "url": url,
        "kind": next((k for k in ("explicit", "link-header", "computed")
                      if found[k]), "none"),
        "page1_next": {k: v for k, v in found.items() if v},
        "chain_pages": len(chain),
        "chain_urls": chain,
        "crawled": len(got),
        "covered": len(chain) - len(missed),
        "page2_covered": page2_covered,
        "missed": missed,
    }


def render_report(rows):
    """markdown 汇总 + 闸门裁定。纯函数。

    对照组的判据是「第 2 页被请求」（能力确认），不是整链覆盖：
    JSON API 的数据 URL 级联会把配额吃得只剩前几页，链尾缺失
    属于配额竞争，不算既有能力缺口。
    """
    lines = ["# 分页漏检闸门（批次 I-2）", ""]
    lines.append("对照 = 显式字段 / Link 头，判据为「第 2 页被请求」（能力确认）；"
                 "computed = 计算型（响应体无下一页地址，需构造），漏检即代价。")
    lines.append("")
    lines.append("| 站 | 形态 | 链页数 | 第2页 | 已覆盖 | 漏检 |")
    lines.append("|---|---|---:|---|---:|---:|")
    for r in rows:
        err = "（探测失败：{}）".format(r["error"]) if r.get("error") else ""
        if r["kind"] in ("explicit", "link-header"):
            p2 = "已覆盖" if r.get("page2_covered") else "**未覆盖**"
        else:
            p2 = "—"
        lines.append("| {} {} | {} | {} | {} | {} | {} |".format(
            r["name"], err, classify(r["kind"]), r["chain_pages"],
            p2, r["covered"], len(r["missed"])))
    lines.append("")
    control = [r for r in rows if r["kind"] in ("explicit", "link-header")]
    computed = [r for r in rows if r["kind"] == "computed"]
    control_gap = sum(1 for r in control if not r.get("page2_covered"))
    comp_miss = sum(len(r["missed"]) for r in computed)
    lines.append(f"- 对照组（{len(control)} 站）：第 2 页未覆盖 {control_gap} 站"
                 "（=既有能力缺口）。")
    lines.append(f"- 计算型（{len(computed)} 站）：漏检合计 {comp_miss} 条。")
    lines.append("")
    if comp_miss > 0:
        lines.append("## 裁定：计算型分页有实测漏检 → 立项实现（带噪音数据）")
        for r in computed:
            if r["missed"]:
                lines.append(f"- **{r['name']}** 漏 {len(r['missed'])} 条，如 `{r['missed'][0]}`")
    else:
        lines.append("## 裁定：计算型分页零漏检 → 记「不做」")
    if control_gap > 0:
        lines.append("")
        lines.append("**注意：对照组第 2 页未全覆盖，既有覆盖有缺口，需复查。**")
    return "\n".join(lines) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--sites", required=True, help="站点表 json")
    ap.add_argument("--bin", default="./assetcrawler.exe", help="assetcrawler 可执行文件")
    ap.add_argument("--proxy", default="", help="HTTP(S) 代理")
    ap.add_argument("--work-dir", required=True, help="中间产物目录")
    ap.add_argument("--json", default="", help="结果 JSON 输出路径")
    ap.add_argument("--max-pages", type=int, default=5, help="沿链最多走几页")
    args = ap.parse_args(argv)

    os.makedirs(args.work_dir, exist_ok=True)
    with open(args.sites, encoding="utf-8") as fh:
        sites = json.load(fh)

    rows = []
    for site in sites:
        try:
            rows.append(probe_site(site, args.bin, args.work_dir,
                                   args.proxy, args.max_pages))
        except Exception as exc:  # 单站失败不拖垮整份报告，但必须在报告里可见
            rows.append({"name": site["name"], "url": site["url"],
                         "kind": "none", "error": "{}: {}".format(
                             type(exc).__name__, exc),
                         "chain_pages": 0, "covered": 0, "missed": [],
                         "crawled": 0, "chain_urls": [], "page1_next": {}})

    print(render_report(rows), end="")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump(rows, fh, ensure_ascii=False, indent=2)
        print(f"[json] {args.json}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
