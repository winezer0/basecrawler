#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_openapi.py —— tools/openapi.py 的纯函数测试。

口径与 mapexposure/segments 等闸门一致：先锁纯函数，再跑实测。
"""
import unittest

import openapi


class TestSpecCandidatePaths(unittest.TestCase):
    def test_table_driven(self):
        paths = openapi.SPEC_CANDIDATES
        # 表驱动：覆盖三大生态的常见约定
        self.assertIn("/openapi.json", paths)
        self.assertIn("/swagger.json", paths)
        self.assertIn("/v3/api-docs", paths)   # Springdoc
        self.assertIn("/api-docs", paths)      # FastAPI/springfox
        self.assertIn("/swagger/v1/swagger.json", paths)  # .NET Swashbuckle
        self.assertIn("/v2/swagger.json", paths)  # Swagger 2.0 生态（P-1 补测）
        # 去重且有序
        self.assertEqual(len(paths), len(set(paths)))

    def test_no_duplicates(self):
        seen = set()
        for p in openapi.SPEC_CANDIDATES:
            self.assertNotIn(p, seen)
            seen.add(p)


class TestLooksLikeSpec(unittest.TestCase):
    def test_openapi3(self):
        self.assertTrue(openapi.looks_like_spec({
            "openapi": "3.0.0", "paths": {"/pets": {"get": {}}}}))

    def test_swagger2(self):
        self.assertTrue(openapi.looks_like_spec({
            "swagger": "2.0", "paths": {"/pets": {"get": {}}}}))

    def test_reject_non_spec_json(self):
        self.assertFalse(openapi.looks_like_spec({"items": [1, 2]}))
        self.assertFalse(openapi.looks_like_spec({"openapi": "3.0.0"}))  # 无 paths

    def test_reject_paths_not_dict(self):
        self.assertFalse(openapi.looks_like_spec(
            {"openapi": "3.0.0", "paths": []}))


class TestClassifySpec(unittest.TestCase):
    def test_summary_counts(self):
        doc = {
            "openapi": "3.0.2",
            "paths": {
                "/pets": {"get": {}, "post": {}},
                "/pets/{id}": {"get": {}, "delete": {}},
            },
        }
        got = openapi.classify_spec(doc)
        self.assertEqual(got["version"], "3.0.2")
        self.assertEqual(got["paths"], 2)
        self.assertEqual(got["operations"], 4)

    def test_swagger2_version(self):
        got = openapi.classify_spec({"swagger": "2.0", "paths": {"/a": {"get": {}}}})
        self.assertEqual(got["version"], "2.0")
        self.assertEqual(got["operations"], 1)

    def test_non_dict_path_entries_skipped(self):
        # paths 值可能不是 dict（异常文档），只数得动的
        doc = {"openapi": "3.0.0", "paths": {"/a": {"get": {}}, "/b": "oops"}}
        got = openapi.classify_spec(doc)
        self.assertEqual(got["paths"], 2)
        self.assertEqual(got["operations"], 1)


class TestRenderReport(unittest.TestCase):
    def test_gate_verdicts(self):
        rows = [{
            "domain": "a.com", "exposed": True, "spec_url": "https://a.com/openapi.json",
            "version": "3.0.0", "paths": 10, "operations": 20, "error": "",
        }]
        text = openapi.render_report(rows)
        self.assertIn("100.0%", text)
        self.assertIn("立项实现", text)

    def test_zero_exposure_says_skip(self):
        rows = [{
            "domain": "b.com", "exposed": False, "spec_url": "",
            "version": "", "paths": 0, "operations": 0, "error": "",
        }]
        text = openapi.render_report(rows)
        self.assertIn("记「不做」", text)

    def test_error_row_listed(self):
        rows = [{
            "domain": "c.com", "exposed": False, "spec_url": "",
            "version": "", "paths": 0, "operations": 0, "error": "timeout",
        }]
        text = openapi.render_report(rows)
        self.assertIn("c.com", text)
        self.assertIn("timeout", text)


if __name__ == "__main__":
    unittest.main()
