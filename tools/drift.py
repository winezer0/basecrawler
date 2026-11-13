#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""drift.py —— 基线漂移自动裁定。

`baseline.py diff` 只回答「和基线比少了什么」，会把目标站自己的变化
（删帖、部署换 hash、v3/v5 子域切换、配额饱和时的子域抽样）误报成回退。

本脚本把 tools/README.md 的四步 SOP 固化：同一时段串行跑
旧1 / 新1 / 旧2 / 新2，用与基线完全相同的 canonical 口径求差集，
再按「旧代码自己也丢过吗 / 差集是否只有新版稳定缺失」给出裁定。

它刻意不给出「一定没回退」这种结论：输出的是证据与分级，
需人工复核的 URL 会逐条列出。

用法：
  python tools/drift.py --manifest tools/drift.example.json           # 离线裁定已有样本
  python tools/drift.py --manifest spec.json --run --out-dir /tmp/d  # 先跑四次采集再裁定
  python tools/drift.py --manifest spec.json --json                  # 机器可读输出
"""
import argparse
import json
import os
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import baseline as B  # noqa: E402  复用同一套归一化口径

VERDICT_IDENTICAL = "零差异"
VERDICT_NO_EVIDENCE = "无回退证据"
VERDICT_REVIEW = "需人工复核"
VERDICT_SUSPECT = "疑似代码回退"


@dataclass(frozen=True)
class Diff:
    """两个集合的差。frozen 保证裁定链里不会被意外改写。"""

    added: frozenset
    lost: frozenset

    @property
    def size(self):
        return len(self.added) + len(self.lost)


def make_diff(a, b):
    """a -> b：added 是 b 独有，lost 是 a 独有。"""
    return Diff(frozenset(b - a), frozenset(a - b))


def load_jsonl_set(path, host):
    """按 jsonl 读原始输出，归一化成 canonical 的站内 URL 集合。"""
    urls = B.load(str(path), "jsonl")
    if urls is None:
        raise FileNotFoundError(path)
    return {B.canonical(u) for u in urls if B.host_of(u).endswith(host)}


def load_baseline_set(path):
    """基线文本已经是 canonical，不再重复归一化。"""
    p = Path(path)
    if not p.exists():
        raise FileNotFoundError(path)
    return B.read_set(str(p))


def host_groups(urls):
    """canonical 形态是 host/path，第一段即 host。"""
    out = {}
    for u in urls:
        h = u.split("/", 1)[0]
        out[h] = out.get(h, 0) + 1
    return dict(sorted(out.items(), key=lambda kv: (-kv[1], kv[0])))


def stable_of_set(urls):
    """hash 归一化，用于识别「同一产物换了部署 hash」。"""
    return frozenset(B.stable_of(u) for u in urls)


def is_subdomain(host, main_host):
    return host != main_host and host.endswith("." + main_host)


def adjudicate(base, old1, new1, old2=None, new2=None, main_host="", quota=0):
    """核心裁定，纯函数：输入都是集合，输出是可 JSON 序列化的证据字典。

    判据按证据强度从高到低：
      1. 旧 -> 新零差异：最强，直接判零差异；
      2. 旧代码两次都稳定有、新代码两次都稳定没有：最可疑；
      3. 差集在「同二进制连跑噪声」量级内：属抽样波动；
      4. 差集全在子域且增删成对：配额饱和下的子域轮换；
      5. 旧代码跨日也丢同一批：站点自身变化。
    """
    cross = make_diff(old1, new1)
    same_code = make_diff(base, old1)
    base_lost_old = frozenset(base - old1)
    base_lost_new = frozenset(base - new1)
    # 基线到旧、到新的消失集合若完全一致，说明这批 URL 在旧代码时代就已经没了。
    baseline_lost_matches = bool(base_lost_old) and base_lost_old == base_lost_new
    old_repeat = make_diff(old1, old2) if old2 is not None else None
    new_repeat = make_diff(new1, new2) if new2 is not None else None
    noise = max(
        (d.size for d in (old_repeat, new_repeat) if d is not None),
        default=None,
    )

    stable_old = old1 & old2 if old2 is not None else old1
    stable_new = new1 & new2 if new2 is not None else new1
    hard_lost = frozenset(stable_old - stable_new)

    reasons = []
    if baseline_lost_matches:
        reasons.append(
            f"基线消失的 {len(base_lost_old)} 条在旧 / 新两次采样中完全一致，属站点自身变化"
        )
    if cross.size == 0:
        verdict = VERDICT_IDENTICAL
        reasons.append("旧 / 新同时段逐条相同，唯一变量没有产生任何差异")
    elif not hard_lost:
        verdict = VERDICT_NO_EVIDENCE
        reasons.append("旧代码两次采样中并不稳定持有这些 URL，新版缺失不能归因于代码")
    else:
        verdict = VERDICT_SUSPECT
        reasons.append("以下 URL 在旧代码两次采样中都存在、新代码两次采样中都缺失")

    if verdict != VERDICT_IDENTICAL and old_repeat is not None:
        reasons.append(f"同二进制连跑噪声上限 {noise} 条（旧 {old_repeat.size} / 新 {new_repeat.size}）")
        if verdict != VERDICT_NO_EVIDENCE and noise is not None and len(hard_lost) <= noise:
            verdict = VERDICT_REVIEW
            reasons.append(f"稳定缺失 {len(hard_lost)} 条不超过连跑噪声，倾向抽样波动")

    lost_hosts = host_groups(cross.lost)
    added_hosts = host_groups(cross.added)
    all_subdomain = all(
        is_subdomain(h, main_host) for h in list(lost_hosts) + list(added_hosts)
    )
    paired = abs(len(cross.added) - len(cross.lost)) <= max(2, len(cross.lost) // 2)

    if verdict in (VERDICT_SUSPECT, VERDICT_REVIEW) and all_subdomain and paired:
        verdict = VERDICT_NO_EVIDENCE
        reasons.append("差集全部落在子域且增删成对，符合配额饱和下的子域轮换")

    if verdict in (VERDICT_SUSPECT, VERDICT_REVIEW) and baseline_lost_matches:
        verdict = VERDICT_NO_EVIDENCE
        reasons.append("旧 / 新相对基线的差异同形，差集不能归因于新版代码")

    if quota and len(old1) >= quota * 0.95:
        reasons.append(f"结果数 {len(old1)} 接近配额 {quota}，差集可能只是抽样不同")

    stable_diff = make_diff(stable_of_set(old1), stable_of_set(new1))
    return {
        "verdict": verdict,
        "reasons": reasons,
        "counts": {
            "baseline": len(base),
            "old1": len(old1),
            "new1": len(new1),
            "old2": len(old2) if old2 is not None else None,
            "new2": len(new2) if new2 is not None else None,
        },
        "baseline_to_new": _diff_dict(make_diff(base, new1)),
        "baseline_to_old": _diff_dict(same_code),
        "baseline_lost_matches": baseline_lost_matches,
        "old_to_new": _diff_dict(cross),
        "old_repeat": _diff_dict(old_repeat),
        "new_repeat": _diff_dict(new_repeat),
        "noise_ceiling": noise,
        "hard_lost": sorted(hard_lost),
        "lost_hosts": lost_hosts,
        "added_hosts": added_hosts,
        "stable_diff": _diff_dict(stable_diff),
    }


def _diff_dict(d):
    if d is None:
        return None
    return {"added": len(d.added), "lost": len(d.lost)}


def run_site(site, cfg, out_dir, timeout):
    """按 spec 跑四次采集，返回 {key: jsonl 路径}。有副作用，仅 --run 时调用。"""
    files = {}
    for key, binary in (("old1", cfg["old_bin"]), ("new1", cfg["new_bin"]),
                        ("old2", cfg["old_bin"]), ("new2", cfg["new_bin"])):
        out = Path(out_dir) / f"{site['host']}-{key}.jsonl"
        cmd = [binary, "-u", site["url"]] + list(cfg.get("flags", [])) + [
            "-o", str(out),
        ]
        if cfg.get("proxy"):
            cmd += ["-proxy", cfg["proxy"]]
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        if proc.returncode != 0:
            raise RuntimeError(f"{key} 采集失败: {proc.stderr[-400:]}")
        files[key] = str(out)
    return files


def render(name, result):
    c, x = result["counts"], result["old_to_new"]
    lines = [
        f"=== {name} ===",
        f"  基线 {c['baseline']} / 旧 {c['old1']} / 新 {c['new1']}"
        + (f" / 旧2 {c['old2']} / 新2 {c['new2']}" if c["old2"] is not None else ""),
        f"  基线->新 {result['baseline_to_new']['added']} 增 / {result['baseline_to_new']['lost']} 失"
        f"    旧->新 {x['added']} 增 / {x['lost']} 失",
        f"  裁定: {result['verdict']}",
    ]
    for r in result["reasons"]:
        lines.append(f"    - {r}")
    if result["hard_lost"]:
        lines.append("  需复核的 URL:")
        lines += [f"    ! {u}" for u in result["hard_lost"][:12]]
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description="基线漂移裁定")
    ap.add_argument("--manifest", required=True, help="站点与样本路径的 JSON 描述")
    ap.add_argument("--run", action="store_true", help="先按 spec 跑四次采集")
    ap.add_argument("--out-dir", default="", help="--run 时的输出目录")
    ap.add_argument("--timeout", type=int, default=300, help="单次采集超时秒数")
    ap.add_argument("--json", action="store_true", help="输出机器可读 JSON")
    args = ap.parse_args()

    with open(args.manifest, encoding="utf-8") as fh:
        cfg = json.load(fh)

    # sites / flags 的单一数据源：与 rebaseline.py 共用 baseline.sites.json，
    # 避免对拍与重采各写一套开关、口径不一致。
    if cfg.get("sites_file"):
        p = Path(cfg["sites_file"])
        if not p.exists():
            p = Path(__file__).resolve().parent / p.name
        with open(p, encoding="utf-8") as fh:
            src = json.load(fh)
        cfg["sites"] = src["sites"]
        cfg["flags"] = src.get("flags", cfg.get("flags", []))

    out_dir = args.out_dir or str(Path(args.manifest).parent / "drift-out")
    if args.run:
        os.makedirs(out_dir, exist_ok=True)

    results = {}
    for site in cfg["sites"]:
        host = site["host"]
        files = site.get("files") or {}
        if args.run:
            files = run_site(site, cfg, out_dir, args.timeout)
        elif not files:
            raise SystemExit(
                f"{host}: 未提供 files 且未给 --run；离线裁定请填 files，"
                "在线裁定请显式加 --run（避免误发外部请求）"
            )
        sets = {k: load_jsonl_set(Path(v), host) for k, v in files.items()}
        base = load_baseline_set(site["baseline"])
        results[host] = adjudicate(
            base,
            sets.get("old1", set()),
            sets.get("new1", set()),
            sets.get("old2"),
            sets.get("new2"),
            main_host=host,
            quota=site.get("quota", 0),
        )

    if args.json:
        print(json.dumps(results, ensure_ascii=False, indent=2))
    else:
        for host, result in results.items():
            print(render(host, result))
            print()
        bad = [h for h, r in results.items() if r["verdict"] in (VERDICT_SUSPECT, VERDICT_REVIEW)]
        if bad:
            print(f"需人工复核的站点: {', '.join(bad)}")
            sys.exit(1)
        print(f"结论: {len(results)} 份站点均无代码回退证据（差集可由站点漂移或抽样解释）。")


if __name__ == "__main__":
    main()
