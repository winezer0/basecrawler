#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""openapi.py —— OpenAPI/Swagger spec 暴露率抽样（批次 P-1）。

回答一个具体问题：公开站点里有多少把 OpenAPI/Swagger spec 直接
挂在常见路径上？spec 提取（契约级接口发现）是否值得实现，由这个
比例决定，不靠感觉。

与 curl 块提取（M-1，示例级）的关系：spec 是契约级——它带
parameters/schema/enum 约束，能生成比「按名字猜值」更准的测试
请求。但只有 spec 真的暴露且规模可观时才值得做。

抽样口径：

- 站点：Majestic 前 N 域名（外部公开榜单，可复现）+ `--extra`
  补充的已知 API 站；
- 每站：依次探测 `SPEC_CANDIDATES` 里的常见路径（表驱动，覆盖
  OpenAPI 3 / Swagger 2 / Springdoc / FastAPI / .NET 五类生态）；
- 命中：200 且 JSON 且 `looks_like_spec`（有 openapi/swagger 键
  且 paths 是非空 dict）；
- 暴露率 = 命中站 / 有效站（至少一个候选路径得到确定性回应的站）。

用法：

  python tools/openapi.py --list majestic-head.csv --top 50 \
      --extra api.stripe.com,gitlab.com,petstore.swagger.io \
      --proxy http://127.0.0.1:10808 --work-dir /tmp/p1 \
      --json /tmp/p1/openapi.json
"""
import argparse
import json
import time
import urllib.error
import urllib.request
from pathlib import Path

import mapexposure

# spec 候选路径：按生态约定表驱动排列。顺序即优先级——spec 网关
# （/openapi.json）优先于框架默认（/v3/api-docs）。
SPEC_CANDIDATES = [
    "/openapi.json",
    "/swagger.json",
    "/api-docs",
    "/v3/api-docs",
    "/v2/api-docs",
    "/docs/openapi.json",
    "/api/openapi.json",
    "/spec/openapi.json",
    "/swagger/v1/swagger.json",
    "/api-docs/swagger.json",
    # v2 生态变体（P-1 补测发现：petstore.swagger.io/v2/swagger.json
    # 是 200/13.8KB 的真 spec，初版表漏了它——闸门口径缺陷）
    "/v2/swagger.json",
    "/api/v2/swagger.json",
    "/rest/v2/swagger.json",
    "/swagger/v2/swagger.json",
]

UA = "basecrawler-openapi-gate/1.0"
GATE_THRESHOLD = 0.10  # 与 I-1/I-3 同口径：10%


def looks_like_spec(doc):
    """判定 JSON 是否 spec：有 openapi/swagger 版本键且 paths 是非空 dict。

    纯函数。paths 为空 dict 的 spec 没有接口信息，按未暴露处理。
    """
    if not isinstance(doc, dict):
        return False
    if "openapi" not in doc and "swagger" not in doc:
        return False
    paths = doc.get("paths")
    return isinstance(paths, dict) and len(paths) > 0


def classify_spec(doc):
    """摘要：版本、paths 数、operations 数。纯函数。

    operations 只数得动的（path item 是 dict 且值是 dict）；
    异常文档不抛，数到多少算多少。
    """
    version = doc.get("openapi") or doc.get("swagger") or ""
    paths = doc.get("paths") or {}
    ops = 0
    if isinstance(paths, dict):
        for item in paths.values():
            if isinstance(item, dict):
                ops += sum(1 for v in item.values() if isinstance(v, dict))
    return {"version": version, "paths": len(paths), "operations": ops}


def probe_spec(url, proxy="", timeout=12):
    """探测单个 URL 是否暴露 spec。返回 (doc, err)。纯 IO。

    网络错误/非 200/非 JSON 一律 (None, err)——调用方只关心命中。
    """
    try:
        data, ctype = mapexposure._open(url, proxy, timeout=timeout)
    except urllib.error.HTTPError as exc:
        return None, "http {}".format(exc.code)
    except Exception as exc:
        return None, "{}: {}".format(type(exc).__name__, exc)
    try:
        doc = json.loads(data.decode("utf-8", "replace"))
    except ValueError as exc:
        return None, "bad json: {}".format(exc)
    if not looks_like_spec(doc):
        return None, ""
    return doc, ""


def probe_site(domain, proxy="", sleep=0.2):
    """单站探测：依次试候选路径，命中即停。返回行记录。

    有效站 = 至少一个候选路径得到确定性回应（命中或明确 404/非 JSON）；
    全部网络错误的站计 error，不进暴露率分母。
    """
    row = {"domain": domain, "exposed": False, "spec_url": "",
           "version": "", "paths": 0, "operations": 0, "error": ""}
    definite = 0
    for path in SPEC_CANDIDATES:
        url = "https://{}{}".format(domain, path)
        doc, err = probe_spec(url, proxy)
        if err and ":" in err and not err.startswith("http "):
            row["error"] = err  # 网络层错误
            break
        definite += 1
        if doc:
            row["exposed"] = True
            row["spec_url"] = url
            summary = classify_spec(doc)
            row.update(summary)
            break
        if sleep:
            time.sleep(sleep)
    if definite == 0 and not row["error"]:
        row["error"] = "no definite response"
    return row


def load_domains(list_path, top, extra):
    """榜单取前 top + extra 补充。纯函数。"""
    domains = []
    if list_path:
        seen = set()
        with open(list_path, encoding="utf-8", errors="ignore") as fh:
            for i, line in enumerate(fh):
                if i == 0:
                    continue  # 表头
                if len(domains) >= top:
                    break
                parts = line.strip().split(",")
                if len(parts) < 2:
                    continue
                d = parts[2].strip() if len(parts) > 2 else parts[1].strip()
                d = d.strip('"').lower()
                if d and d not in seen:
                    seen.add(d)
                    domains.append(d)
    for d in extra:
        d = d.strip().lower()
        if d and d not in domains:
            domains.append(d)
    return domains


def render_report(rows):
    """markdown 汇总 + 闸门裁定。纯函数。"""
    lines = ["# OpenAPI/Swagger 暴露率闸门（批次 P-1）", ""]
    lines.append("候选路径 {} 个（表驱动，五类生态）；命中 = 200 且 JSON 且 "
                 "有版本键 + 非空 paths。".format(len(SPEC_CANDIDATES)))
    lines.append("")
    lines.append("| 站 | 暴露 | spec | 版本 | paths | ops |")
    lines.append("|---|---|---|---|---:|---:|")
    for r in rows:
        err = "（{}）".format(r["error"]) if r.get("error") else ""
        mark = "是" if r["exposed"] else "否"
        spec = r["spec_url"].replace("https://", "") if r["spec_url"] else "—"
        lines.append("| {} {} | {} | {} | {} | {} | {} |".format(
            r["domain"], err, mark, spec, r["version"] or "—",
            r["paths"], r["operations"]))
    lines.append("")
    valid = [r for r in rows if not r.get("error")]
    exposed = [r for r in valid if r["exposed"]]
    rate = len(exposed) / len(valid) if valid else 0.0
    lines.append("- 有效站 {}，暴露 {}（**{:.1%}**，阈值 {:.0%}）".format(
        len(valid), len(exposed), rate, GATE_THRESHOLD))
    total_paths = sum(r["paths"] for r in exposed)
    lines.append("- 暴露站 paths 合计 {}。".format(total_paths))
    lines.append("")
    if rate >= GATE_THRESHOLD and exposed:
        lines.append("## 裁定：暴露率过阈值 → 立项实现 spec 提取（契约级接口发现）")
    else:
        lines.append("## 裁定：暴露率低于阈值 → 记「不做」（spec 提取不现实）")
    failed = [r for r in rows if r.get("error")]
    if failed:
        lines.append("")
        lines.append("探测失败站：{}".format(
            ", ".join("{}（{}）".format(r["domain"], r["error"]) for r in failed)))
    return "\n".join(lines) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description="OpenAPI/Swagger 暴露率抽样")
    ap.add_argument("--list", default="", help="Majestic 榜单 CSV")
    ap.add_argument("--top", type=int, default=50)
    ap.add_argument("--extra", default="", help="逗号分隔的补充域名")
    ap.add_argument("--proxy", default="")
    ap.add_argument("--work-dir", default="/tmp/p1")
    ap.add_argument("--json", default="")
    ap.add_argument("--sleep", type=float, default=0.2)
    args = ap.parse_args(argv)

    domains = load_domains(args.list, args.top, args.extra.split(","))
    if not domains:
        raise SystemExit("无站点可探测")

    Path(args.work_dir).mkdir(parents=True, exist_ok=True)
    progress = Path(args.work_dir) / "progress.jsonl"
    rows = []
    with open(progress, "a", encoding="utf-8") as pf:
        for i, d in enumerate(domains, 1):
            row = probe_site(d, args.proxy, args.sleep)
            rows.append(row)
            pf.write(json.dumps(row, ensure_ascii=False) + "\n")
            pf.flush()
            print("[{}/{}] {} exposed={} paths={}{}".format(
                i, len(domains), d, row["exposed"], row["paths"],
                " ({})".format(row["error"]) if row["error"] else ""),
                flush=True)

    text = render_report(rows)
    print("\n" + text)
    if args.json:
        Path(args.json).parent.mkdir(parents=True, exist_ok=True)
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump({"sites": rows}, fh, ensure_ascii=False, indent=2)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
