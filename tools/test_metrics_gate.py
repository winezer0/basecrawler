#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_metrics_gate.py —— gate_check 纯函数的门禁口径单测。"""
import importlib.util
import json
import os
import tempfile
import unittest

_SPEC = importlib.util.spec_from_file_location(
    "metrics", os.path.join(os.path.dirname(os.path.abspath(__file__)), "metrics.py"))
metrics = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(metrics)


def with_gate(gate):
    def deco(fn):
        def wrapper(*a, **kw):
            fd, path = tempfile.mkstemp(suffix=".json")
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                json.dump(gate, f)
            old = metrics.GATE_PATH
            metrics.GATE_PATH = path
            try:
                return fn(*a, **kw)
            finally:
                metrics.GATE_PATH = old
                os.unlink(path)
        return wrapper
    return deco


PKG = "github.com/winezer0/basecrawler"
GATE = {"cases_min": 10, "coverage_min_per_pkg": {PKG: 80.0}}


def data(cases=10, pkgs=None):
    d = {"cases": cases,
         "coverage": {"per_package": {PKG: 81.0} if pkgs is None else pkgs}}
    return d


class TestGateCheck(unittest.TestCase):
    @with_gate(GATE)
    def test_pass(self):
        errors, _ = metrics.gate_check(data())
        self.assertEqual(errors, [])

    @with_gate(GATE)
    def test_cases_regression(self):
        errors, _ = metrics.gate_check(data(cases=9))
        self.assertTrue(any("用例数回退" in e for e in errors))

    @with_gate(GATE)
    def test_coverage_regression(self):
        errors, _ = metrics.gate_check(data(pkgs={PKG: 79.9}))
        self.assertTrue(any("覆盖率回退" in e for e in errors))

    @with_gate(GATE)
    def test_unregistered_new_package_warns(self):
        errors, _ = metrics.gate_check(data(pkgs={PKG: 81.0, "x/new": 50.0}))
        self.assertTrue(any("未登记" in e for e in errors))

    @with_gate(GATE)
    def test_missing_registered_package(self):
        errors, _ = metrics.gate_check(data(pkgs={}))
        self.assertTrue(any("包消失" in e for e in errors))

    @with_gate({"cases_min": 10, "coverage_min_per_pkg": {PKG: 80.0}})
    def test_coverage_error_blocks_gate(self):
        errors, _ = metrics.gate_check({"cases": 10, "coverage": {"error": "boom"}})
        self.assertTrue(any("覆盖率统计失败" in e for e in errors))

    @with_gate(GATE)
    def test_equal_threshold_passes(self):
        errors, _ = metrics.gate_check(data(pkgs={PKG: 80.0}))
        self.assertEqual(errors, [])


if __name__ == "__main__":
    unittest.main()
