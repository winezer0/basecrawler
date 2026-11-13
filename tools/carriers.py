#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""carriers.py —— 现代前端「URL 载体」增量量化。

回答一个具体问题：响应头 Link、importmap、Web App Manifest、
Service Worker 预缓存、各类内联 hydration 脚本里的 URL，
有多少是 assetcrawler 现有结果里**没有**的？

只有「独有 URL」才是加规则的依据；已由 HTML 标签覆盖的载体加规则是零增量。
脚本分两步，抓到的样本可离线反复分析：

  python tools/carriers.py fetch   --sites sites.json --out-dir /tmp/h0 --proxy ...
  python tools/carriers.py analyze --sites sites.json --sample-dir /tmp/h0 \
      --results-dir /tmp/h0 --json /tmp/h0/carriers.json
"""
import argparse
import json
import re
import sys
import urllib.parse
import urllib.request
from html.parser import HTMLParser
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import baseline as B  # noqa: E402

HYDRATION_IDS = (
    "__NUXT_DATA__", "__NEXT_DATA__", "__INITIAL_STATE__",
    "__APOLLO_STATE__", "__PRELOADED_STATE__", "__remixContext",
    "__sveltekit", "__data", "__QUERY_CACHE__", "__NEXT_DATA__",
)
DATA_FILE_RE = re.compile(r"""["'(]([^"'\s()]*__data\.(?:json|js)[^"'\s()]*)["')]""")
SW_REGISTER_RE = re.compile(r"""serviceWorker\s*\.\s*register\s*\(\s*["']([^"']+)["']""")
URL_IN_TEXT_RE = re.compile(r"""https?://[^\s"'<>]+|/[\w./*?=&%+#@~-]{3,}""")

# 表驱动：载体名 -> 判定说明。新增载体只加一行，不改分析流程。
CARRIERS = (
    "link-header",
    "link-preload",
    "link-modulepreload",
    "link-manifest",
    "importmap",
    "manifest-content",
    "serviceworker",
    "hydration-script",
    "sveltekit-data",
)


class PageParser(HTMLParser):
    """只收集载体线索，不做任何网络请求，便于单测。"""

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.links = []            # (rel, href)
        self.importmaps = []       # 原始 JSON 文本
        self.script_ids = []       # (id, type, has_nuxt_data)
        self.inline_scripts = []   # 所有内联脚本文本
        self._script_stack = []

    def handle_starttag(self, tag, attrs):
        a = {k.lower(): (v or "") for k, v in attrs}
        if tag == "link":
            self.links.append((a.get("rel", "").lower(), a.get("href", "")))
        elif tag == "script":
            self.script_ids.append((
                a.get("id", ""),
                a.get("type", "").lower(),
                "data-nuxt-data" in a,
            ))
            if a.get("type", "").lower() == "importmap":
                self._script_stack.append(["importmap", []])
            elif "src" not in a:
                self._script_stack.append(["inline", []])

    def handle_endtag(self, tag):
        if tag == "script" and self._script_stack:
            kind, buf = self._script_stack.pop()
            text = "".join(buf)
            if kind == "importmap":
                self.importmaps.append(text)
            elif text:
                self.inline_scripts.append(text)

    def handle_data(self, data):
        if self._script_stack:
            self._script_stack[-1][1].append(data)


def parse_link_header(value):
    """解析 `Link: <a>; rel=preload, <b>; rel=modulepreload`。"""
    out = []
    for part in re.findall(r"<([^>]+)>\s*([^,]*)", value or ""):
        url, params = part[0].strip(), part[1]
        rel = re.search(r"rel\s*=\s*\"?([^\";]+)", params)
        out.append((url, (rel.group(1).lower() if rel else "")))
    return out


def importmap_urls(text):
    """imports / scopes 的值才是真正会被浏览器请求的模块地址。"""
    try:
        doc = json.loads(text)
    except (ValueError, TypeError):
        return set()
    out = set()
    for key in ("imports", "scopes"):
        section = doc.get(key) or {}
        if key == "scopes":
            for mapping in section.values():
                if isinstance(mapping, dict):
                    out.update(str(v) for v in mapping.values())
        elif isinstance(section, dict):
            out.update(str(v) for v in section.values())
    return {v for v in out if v.startswith(("/", "http://", "https://"))}


def manifest_urls(text):
    """Web App Manifest 里的 start_url / icons / shortcuts / serviceworker。"""
    try:
        doc = json.loads(text)
    except (ValueError, TypeError):
        return set()
    out = set()
    for key in ("start_url", "scope", "src"):
        v = doc.get(key)
        if isinstance(v, str):
            out.add(v)
    for key in ("icons", "shortcuts", "screenshots"):
        for item in doc.get(key) or []:
            if isinstance(item, dict) and isinstance(item.get("src"), str):
                out.add(item["src"])
    sw = doc.get("serviceworker")
    if isinstance(sw, dict) and isinstance(sw.get("src"), str):
        out.add(sw["src"])
    return {v for v in out if v.startswith(("/", "http://", "https://"))}


def absolute(url, base):
    if not url or url.startswith(("data:", "javascript:", "#")):
        return ""
    try:
        return urllib.parse.urljoin(base, url)
    except ValueError:
        return ""


def collect(page_url, headers, html_text, manifest_text=""):
    """纯函数：返回 {载体名: set(绝对 URL)}。"""
    p = PageParser()
    p.feed(html_text or "")
    found = {name: set() for name in CARRIERS}

    def add(name, raw):
        u = absolute(raw, page_url)
        if u:
            found[name].add(u)

    for name, value in (headers or {}).items():
        if name.lower() == "link":
            for url, rel in parse_link_header(str(value)):
                add("link-header", url)
                if "preload" in rel and "modulepreload" not in rel:
                    add("link-preload", url)
                if "modulepreload" in rel:
                    add("link-modulepreload", url)

    for rel, href in p.links:
        rels = rel.split()
        if "preload" in rels:
            add("link-preload", href)
        if "modulepreload" in rels:
            add("link-modulepreload", href)
        if "manifest" in rels:
            add("link-manifest", href)

    for text in p.importmaps:
        for url in importmap_urls(text):
            add("importmap", url)
    for url in manifest_urls(manifest_text):
        add("manifest-content", url)

    for text in p.inline_scripts:
        for m in SW_REGISTER_RE.finditer(text):
            add("serviceworker", m.group(1))
        for m in DATA_FILE_RE.finditer(text):
            add("sveltekit-data", m.group(1))
    for m in DATA_FILE_RE.finditer(html_text or ""):
        add("sveltekit-data", m.group(1))

    hydration = []
    for sid, typ, nuxt in p.script_ids:
        if sid and (sid in HYDRATION_IDS or sid.startswith("__sveltekit") or
                    any(k in sid for k in ("DATA", "STATE", "NUXT", "NEXT"))):
            hydration.append(sid)
        elif nuxt or (typ == "application/json" and sid):
            hydration.append(sid or typ)
    found["hydration-script"] = set(hydration)
    return found


def result_urls(path, hosts):
    """站内过滤同时认原始 host 与重定向后的 host。

    kit.svelte.dev -> svelte.dev 这类跨 host 跳转很常见；
    只用种子 host 会把整份结果滤成 0，制造出一批假的「独有 URL」。
    """
    urls = B.load(str(path), "jsonl")
    if urls is None:
        return set()

    def inside(u):
        h = B.host_of(u)
        return any(h == x or h.endswith("." + x) for x in hosts)

    return {B.canonical(u) for u in urls if inside(u)}


def analyze_one(host, sample_dir, results_dir):
    sd, rd = Path(sample_dir), Path(results_dir)
    html_text = _read(sd / f"{host}.html")
    headers = _json(sd / f"{host}.headers.json") or {}
    manifest_text = _read(sd / f"{host}.manifest.json")
    page_url = (headers or {}).get("final_url") or "https://" + host + "/"
    found = collect(page_url, headers, html_text, manifest_text)
    final_host = urllib.parse.urlsplit(page_url).hostname or host
    got = result_urls(rd / f"{host}.jsonl", {host, final_host})
    valid = len(got) > 5  # 纯文本 / 单页样本没有可比性，独有数不参与裁定

    rows = {}
    for name in CARRIERS:
        items = found[name]
        if name == "hydration-script":
            rows[name] = {"found": len(items), "covered": None,
                          "unique": None, "items": sorted(items)}
            continue
        canon = {B.canonical(u) for u in items if u}
        unique = sorted(canon - got)
        rows[name] = {
            "found": len(items),
            "covered": len(canon) - len(unique),
            "unique": len(unique),
            "items": unique[:10],
        }
    return {
        "host": host,
        "final_host": final_host,
        "results": len(got),
        "sample_valid": valid,
        "carriers": rows,
    }


def _read(path):
    p = Path(path)
    return p.read_text(encoding="utf-8", errors="replace") if p.exists() else ""


def _json(path):
    p = Path(path)
    if not p.exists():
        return None
    try:
        return json.loads(p.read_text(encoding="utf-8"))
    except ValueError:
        return None


def fetch_one(host, out_dir, proxy, timeout=25):
    """抓首页与 manifest，落盘供离线分析。有副作用，仅 fetch 子命令调用。"""
    handlers = []
    if proxy:
        handlers.append(urllib.request.ProxyHandler({"http": proxy, "https": proxy}))
    opener = urllib.request.build_opener(*handlers)
    opener.addheaders = [("User-Agent", "Mozilla/5.0 (compatible; assetcrawler-carrier-probe/1.0)")]
    url = "https://" + host + "/"
    with opener.open(url, timeout=timeout) as resp:
        body = resp.read(4 * 1024 * 1024).decode("utf-8", "replace")
        headers = {k: v for k, v in resp.headers.items()}
        final = resp.geturl()

    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    (out / f"{host}.html").write_text(body, encoding="utf-8")
    (out / f"{host}.headers.json").write_text(
        json.dumps({"final_url": final, **headers}, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )

    p = PageParser()
    p.feed(body)
    for rel, href in p.links:
        if "manifest" in rel.split() and href:
            murl = urllib.parse.urljoin(final, href)
            try:
                with opener.open(murl, timeout=timeout) as mresp:
                    (out / f"{host}.manifest.json").write_text(
                        mresp.read(512 * 1024).decode("utf-8", "replace"), encoding="utf-8"
                    )
            except Exception as exc:  # 单个 manifest 失败不影响整站分析
                print(f"  manifest {murl}: {exc}", file=sys.stderr)
            break
    return len(body)


def render(report):
    tag = "" if report["sample_valid"] else "  [样本不足，独有数不参与裁定]"
    lines = [f"=== {report['host']} -> {report['final_host']} （结果 {report['results']} 条）{tag} ==="]
    lines.append(f"  {'载体':<22}{'发现':>6}{'已覆盖':>8}{'独有':>6}")
    for name, row in report["carriers"].items():
        cov = "-" if row["covered"] is None else row["covered"]
        uniq = "-" if row["unique"] is None else row["unique"]
        lines.append(f"  {name:<22}{row['found']:>6}{str(cov):>8}{str(uniq):>6}")
    for name, row in report["carriers"].items():
        if row["unique"] and row["unique"] > 0:
            lines.append(f"  {name} 独有样例: {', '.join(row['items'][:4])}")
        if name == "hydration-script" and row["items"]:
            lines.append(f"  hydration 脚本 id: {', '.join(row['items'][:6])}")
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description="现代前端 URL 载体增量量化")
    ap.add_argument("cmd", choices=("fetch", "analyze"))
    ap.add_argument("--sites", required=True, help="JSON 数组，元素为 host 字符串")
    ap.add_argument("--out-dir", default="", help="fetch 输出目录")
    ap.add_argument("--sample-dir", default="", help="analyze 读取的样本目录")
    ap.add_argument("--results-dir", default="", help="assetcrawler JSONL 所在目录")
    ap.add_argument("--proxy", default="")
    ap.add_argument("--json", default="", help="analyze 报告输出路径")
    args = ap.parse_args()

    with open(args.sites, encoding="utf-8") as fh:
        sites = json.load(fh)
    hosts = [s["host"] if isinstance(s, dict) else s for s in sites]

    if args.cmd == "fetch":
        for h in hosts:
            try:
                n = fetch_one(h, args.out_dir, args.proxy)
                print(f"{h}: {n} bytes")
            except Exception as exc:
                print(f"{h}: FAILED {exc}", file=sys.stderr)
        return

    reports = []
    for h in hosts:
        r = analyze_one(h, args.sample_dir or args.out_dir, args.results_dir or args.out_dir)
        reports.append(r)
        print(render(r))
        print()
    if args.json:
        Path(args.json).write_text(
            json.dumps(reports, ensure_ascii=False, indent=2), encoding="utf-8"
        )
        print(f"报告: {args.json}")


if __name__ == "__main__":
    main()
