#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""metrics.py —— 生成 STATUS.md 里那些容易过期的可变量。

用法：
  python tools/metrics.py             # 静态统计：行数、用例数、二进制体积
  python tools/metrics.py --cover     # 额外跑 go test -cover ./... 取覆盖率
  python tools/metrics.py --json      # 机器可读输出
  python tools/metrics.py --gate      # 用 tools/gate.json 的阈值做门禁（CI 用）

门禁口径：用例数与各包覆盖率不得低于 gate.json 里登记的基线；
门禁只拦回退、不拦提升，覆盖率提高后应手动调高 gate.json（基线只升不降）。

文档里写死行数、用例数、覆盖率，改一次代码就失真一次（ISSUES P2-5）。
现在 STATUS.md 只保留结论与日期，具体数字以本脚本当次输出为准。

覆盖率口径提醒（ISSUES P2-4）：`go test -cover ./...` 里
`jsext/goja`、`jsext/treesitter` 显示 0% 只是「用例写在 jsext 包做黑盒测试」
的统计假象，真实覆盖要看 `go test -coverpkg=./... ./jsext/`。
"""
import argparse
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GATE_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "gate.json")
CASE_RE = re.compile(r"^func\s+(?:Test|Example|Benchmark)[A-Za-z0-9_]+", re.M)
COVER_RE = re.compile(r"coverage:\s*([\d.]+)%\s*of statements")


def go_files():
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in (".git", "vendor", "node_modules")]
        for name in filenames:
            if name.endswith(".go"):
                yield os.path.join(dirpath, name)


def line_count(path):
    with open(path, "rb") as f:
        return sum(1 for _ in f)


def static_stats(bin_path):
    impl = test_lines = impl_files = test_files = cases = 0
    for path in go_files():
        n = line_count(path)
        if path.endswith("_test.go"):
            test_lines += n
            test_files += 1
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                cases += len(CASE_RE.findall(f.read()))
        else:
            impl += n
            impl_files += 1
    size = os.path.getsize(bin_path) if os.path.exists(bin_path) else None
    return {
        "impl_lines": impl,
        "test_lines": test_lines,
        "total_lines": impl + test_lines,
        "impl_files": impl_files,
        "test_files": test_files,
        "cases": cases,
        "binary_bytes": size,
        "binary_mb": round(size / 1024 / 1024, 2) if size else None,
    }


def coverage_stats():
    proc = subprocess.run(["go", "test", "-cover", "./..."], cwd=ROOT,
                          capture_output=True, text=True)
    if proc.returncode != 0:
        return {"error": proc.stdout + proc.stderr}
    per_package = {}
    for line in proc.stdout.splitlines():
        m = COVER_RE.search(line)
        if not m:
            continue
        parts = line.split()
        if len(parts) >= 2:
            per_package[parts[1]] = float(m.group(1))
    return {"per_package": per_package,
            "average": round(sum(per_package.values()) / len(per_package), 1) if per_package else None}


def gate_check(data):
    """按 gate.json 的阈值门禁，回退即失败。返回 (错误列表, gate配置)。"""
    with open(GATE_PATH, "r", encoding="utf-8") as f:
        gate = json.load(f)
    errors = []
    if data["cases"] < gate["cases_min"]:
        errors.append(f"用例数回退：{data['cases']} < 基线 {gate['cases_min']}")
    cov = data.get("coverage", {})
    if "error" in cov:
        errors.append("覆盖率统计失败，门禁无法执行")
        return errors, gate
    mins = gate["coverage_min_per_pkg"]
    for pkg, pct in cov["per_package"].items():
        if pkg not in mins:
            # 新包未登记基线：不拦，但提示补登记
            errors.append(f"新包未登记覆盖率基线（请补录 gate.json 后重跑）：{pkg} {pct}%")
            continue
        if pct < mins[pkg]:
            errors.append(f"覆盖率回退：{pkg} {pct}% < 基线 {mins[pkg]}%")
    for pkg in mins:
        if pkg not in cov["per_package"]:
            errors.append(f"包消失（基线仍在登记）：{pkg}")
    return errors, gate


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cover", action="store_true", help="额外统计覆盖率")
    ap.add_argument("--gate", action="store_true", help="按 gate.json 做门禁（隐含 --cover）")
    ap.add_argument("--bin", default=os.path.join(ROOT, "basecrawler.exe"))
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    data = static_stats(args.bin)
    if args.cover or args.gate:
        data["coverage"] = coverage_stats()
    if args.json:
        print(json.dumps(data, ensure_ascii=False, indent=2))
        return 0

    if args.gate:
        errors, gate = gate_check(data)
        if errors:
            print("门禁未通过：")
            for e in errors:
                print("  - " + e)
            return 1
        print(f"门禁通过：用例 {data['cases']} >= {gate['cases_min']}，"
              f"{len(data['coverage']['per_package'])} 包覆盖率均不低于基线")
        return 0

    print("| 项 | 数值 |")
    print("|---|---:|")
    print(f"| Go 实现代码 | {data['impl_lines']} 行（{data['impl_files']} 个文件） |")
    print(f"| Go 测试代码 | {data['test_lines']} 行（{data['test_files']} 个文件） |")
    print(f"| 合计 | {data['total_lines']} 行 |")
    print(f"| 测试用例 | {data['cases']} 个 |")
    if data["binary_mb"] is not None:
        print(f"| 二进制（默认构建） | {data['binary_mb']} MB |")
    else:
        print("| 二进制（默认构建） | 未构建 |")
    if args.cover:
        cov = data["coverage"]
        if "error" in cov:
            print("\n覆盖率统计失败：\n" + cov["error"][:800])
            return 1
        print(f"\n覆盖率（go test -cover ./... 平均 {cov['average']}%）：\n")
        print("| 包 | 覆盖率 |")
        print("|---|---:|")
        for pkg, pct in sorted(cov["per_package"].items()):
            print(f"| {pkg} | {pct}% |")
        print("\n注意：jsext/goja、jsext/treesitter 的 0% 是统计假象，见本文件顶部说明。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
