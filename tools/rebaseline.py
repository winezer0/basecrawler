#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""rebaseline.py —— 按固定口径重采基线的脚本骨架。

默认 dry-run：只打印将要执行的命令，不请求任何外部站点、不写文件。
加 --run 才实际采集，产物写到 --out-dir，绝不覆盖 tools/baseline/ 下的既有基线。

用法：
  python tools/rebaseline.py                              # 只打印命令（安全）
  python tools/rebaseline.py --run --bin ./assetcrawler.exe --out-dir /tmp/rebase
  python tools/rebaseline.py --run --proxy http://127.0.0.1:10808

采集口径来自 baseline.sites.json，与 drift.py 共用同一套开关。
"""
import argparse
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import baseline as B  # noqa: E402

DEFAULT_SITES = Path(__file__).resolve().parent / "baseline.sites.json"


def load_cfg(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def commands(cfg, binary, out_dir, proxy):
    """生成 (site, cmd, out_path)。纯函数，不执行。"""
    for site in cfg["sites"]:
        out = Path(out_dir) / f"{site['host']}.jsonl"
        cmd = [binary, "-u", site["url"]] + list(cfg.get("flags", [])) + ["-o", str(out)]
        if proxy:
            cmd += ["--proxy", proxy]
        yield site, cmd, out


def canonicalize(site, jsonl_path):
    """把一次采集的 JSONL 转成 canonical 文本，与 baseline.py 口径一致。"""
    urls = B.load(str(jsonl_path), "jsonl") or []
    host = site["host"]
    canon = sorted({B.canonical(u) for u in urls if B.host_of(u).endswith(host)})
    target = Path(jsonl_path).with_suffix(".canonical.txt")
    target.write_text("\n".join(canon) + ("\n" if canon else ""), encoding="utf-8")
    return len(canon), target


def main():
    ap = argparse.ArgumentParser(description="基线重采（默认 dry-run）")
    ap.add_argument("--sites", default=str(DEFAULT_SITES), help="站点清单 JSON")
    ap.add_argument("--bin", default="./assetcrawler.exe", help="当前构建的二进制")
    ap.add_argument("--out-dir", default="tools/rebaseline-out", help="产物目录，不写回 baseline/")
    ap.add_argument("--proxy", default="", help="可选代理")
    ap.add_argument("--timeout", type=int, default=300, help="单次采集超时秒数")
    ap.add_argument("--run", action="store_true", help="真正执行采集（默认只打印）")
    args = ap.parse_args()

    cfg = load_cfg(args.sites)
    if args.run:
        Path(args.out_dir).mkdir(parents=True, exist_ok=True)

    for site, cmd, out in commands(cfg, args.bin, args.out_dir, args.proxy):
        print(("[run] " if args.run else "[dry-run] ") + " ".join(cmd))
        if not args.run:
            continue
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=args.timeout)
        if proc.returncode != 0:
            raise RuntimeError(f"{site['host']} 采集失败: {proc.stderr[-400:]}")
        count, target = canonicalize(site, out)
        print(f"[ok] {site['host']}: {count} 条 -> {target}")

    if not args.run:
        print(f"共 {len(cfg['sites'])} 个站点；未发起任何请求。确认后加 --run 执行。")


if __name__ == "__main__":
    main()
