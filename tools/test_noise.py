#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_noise.py —— noise.py 纯函数单测。

只测统计与渲染逻辑；网络与文件 IO 由 CLI 薄层承担，不进单测。
"""
import unittest

import noise


def row(url, status=None, source="link"):
    r = {"url": url, "source": source}
    if status is not None:
        r["status"] = status
    return r


class TestExtension(unittest.TestCase):
    def test_plain(self):
        self.assertEqual(noise.extension_of("https://a.com/app/router.ts"), ".ts")

    def test_query(self):
        self.assertEqual(noise.extension_of("https://a.com/x.js?v=1"), ".js")

    def test_hashed(self):
        self.assertEqual(noise.extension_of("/chunk.a1b2c3.js"), ".js")

    def test_none(self):
        self.assertEqual(noise.extension_of("https://a.com/docs"), "")

    def test_double_ext(self):
        self.assertEqual(noise.extension_of("/a/b.min.css"), ".css")

    def test_ext_group(self):
        self.assertEqual(noise.ext_group(".ts"), "source")
        self.assertEqual(noise.ext_group(".png"), "asset")
        self.assertEqual(noise.ext_group(".tsv"), "other")
        self.assertEqual(noise.ext_group(""), "none")


class TestParse(unittest.TestCase):
    def test_parse_jsonl_tolerant(self):
        text = (
            '{"url":"https://a/","status":200,"source":"seed"}\n'
            "\n"
            "not json\n"
            '{"url":"https://a/b.ts","status":404,"source":"js-ast"}\n'
        )
        rows = noise.parse_jsonl(text)
        self.assertEqual(len(rows), 2)
        self.assertEqual(rows[1]["status"], 404)


class TestSiteStats(unittest.TestCase):
    ROWS = [
        row("https://a/", 200, "seed"),
        row("https://a/x.css", 200, "link"),
        row("https://a/app/router.ts", 404, "js-ast"),
        row("https://a/app/ui.tsx", 404, "js-ast"),
        row("https://a/img.png", 403, "img"),
        row("https://a/queued", None, "js-ast"),
    ]

    def test_counts(self):
        s = noise.site_stats(self.ROWS)
        self.assertEqual(s["rows"], 6)
        self.assertEqual(s["fetched"], 5)
        self.assertEqual(s["pending"], 1)
        self.assertEqual(s["s4xx"], 3)
        self.assertEqual(s["s404"], 2)
        # 4xx 占已请求的比例
        self.assertAlmostEqual(s["noise_rate"], 3 / 5)

    def test_4xx_sources(self):
        s = noise.site_stats(self.ROWS)
        self.assertEqual(s["n4xx_sources"], [("js-ast", 2), ("img", 1)])

    def test_4xx_ext_groups(self):
        s = noise.site_stats(self.ROWS)
        # .ts/.tsx -> source，.png -> asset
        self.assertEqual(s["n4xx_ext_groups"], [("source", 2), ("asset", 1)])

    def test_empty(self):
        s = noise.site_stats([])
        self.assertEqual(s["rows"], 0)
        self.assertEqual(s["noise_rate"], 0.0)

    def test_all_pending(self):
        s = noise.site_stats([row("https://a/x", None)])
        self.assertEqual(s["noise_rate"], 0.0)


class TestRender(unittest.TestCase):
    def test_verdict_over_threshold(self):
        stats = noise.site_stats(
            [row("https://a/", 200), row("https://a/b.ts", 404, "js-ast")]
        )
        stats["host"] = "a.com"
        text = noise.render_report([stats], threshold=0.10)
        self.assertIn("超阈值", text)
        self.assertIn("立项治理", text)

    def test_verdict_under_threshold(self):
        stats = noise.site_stats([row("https://a/", 200)] * 20 + [row("https://a/b", 404)])
        stats["host"] = "a.com"
        text = noise.render_report([stats], threshold=0.10)
        self.assertIn("不做", text)

    def test_table_contains_host_and_rate(self):
        stats = noise.site_stats([row("https://a/", 200), row("https://a/b", 404)])
        stats["host"] = "a.com"
        text = noise.render_report([stats], threshold=0.10)
        self.assertIn("a.com", text)
        self.assertIn("50.0%", text)


if __name__ == "__main__":
    unittest.main()
