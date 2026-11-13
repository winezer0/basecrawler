#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_sourcemap.py —— sourcemap.py 纯函数单测。"""
import unittest

import sourcemap


class TestClassifySource(unittest.TestCase):
    def test_webpack_proto(self):
        self.assertEqual(sourcemap.classify_source("webpack://repo/./src/a.ts"), "webpack")

    def test_relative(self):
        self.assertEqual(sourcemap.classify_source("../src/a.ts"), "relative")
        self.assertEqual(sourcemap.classify_source("./a.js"), "relative")

    def test_absolute(self):
        self.assertEqual(sourcemap.classify_source("/src/a.ts"), "absolute")

    def test_node_modules(self):
        self.assertEqual(sourcemap.classify_source("webpack://r/node_modules/x/index.js"), "node_modules")

    def test_bare(self):
        self.assertEqual(sourcemap.classify_source("a.ts"), "bare")


class TestNormalizeSource(unittest.TestCase):
    def test_webpack_strip(self):
        # webpack:// 前缀去掉，路径部分按 map URL 解析
        got = sourcemap.normalize_source("webpack://repo/./src/a.ts",
                                          "https://a.com/assets/app.js.map")
        self.assertEqual(got, "https://a.com/assets/src/a.ts")

    def test_relative(self):
        got = sourcemap.normalize_source("../src/a.ts",
                                         "https://a.com/assets/js/app.js.map")
        self.assertEqual(got, "https://a.com/assets/src/a.ts")

    def test_absolute(self):
        got = sourcemap.normalize_source("/src/a.ts", "https://a.com/assets/app.js.map")
        self.assertEqual(got, "https://a.com/src/a.ts")

    def test_bare_rejected(self):
        # 裸文件名无法定位，返回空（调用方跳过）
        self.assertEqual(sourcemap.normalize_source("a.ts", "https://a.com/x.map"), "")

    def test_url_like_kept(self):
        got = sourcemap.normalize_source("https://cdn.b.com/lib.js", "https://a.com/x.map")
        self.assertEqual(got, "https://cdn.b.com/lib.js")


class TestRender(unittest.TestCase):
    def test_render_counts(self):
        rows = [{
            "domain": "a.com", "maps": 2, "sources": 10,
            "normalizable": 6, "fetched_live": 4, "forms": {"webpack": 6},
        }]
        text = sourcemap.render_report(rows)
        self.assertIn("|a.com|2|10|6|4|", text.replace(" ", ""))
        self.assertIn("立项实现", text)

    def test_verdict(self):
        rows = [{
            "domain": "a.com", "maps": 1, "sources": 10,
            "normalizable": 6, "fetched_live": 1, "forms": {},
        }]
        text = sourcemap.render_report(rows)
        self.assertIn("不做", text)  # live 比例低 → 不做


if __name__ == "__main__":
    unittest.main()
