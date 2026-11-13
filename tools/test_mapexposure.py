#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_mapexposure.py —— mapexposure.py 纯函数单测。"""
import unittest

import mapexposure as M


class TestSourceMapURLs(unittest.TestCase):
    def test_standard_comment(self):
        js = 'console.log(1);\n//# sourceMappingURL=app.9f2a.js.map\n'
        got = M.sourcemap_urls(js, "https://a.com/assets/app.9f2a.js")
        self.assertEqual(got, [("file", "https://a.com/assets/app.9f2a.js.map")])

    def test_legacy_at_comment(self):
        js = '//!/# sourceMappingURL=x.map'  # 无命中
        js = 'var a=1\n//@ sourceMappingURL=x.map\n'
        got = M.sourcemap_urls(js, "https://a.com/x.js")
        self.assertEqual(got, [("file", "https://a.com/x.map")])

    def test_relative_parent(self):
        js = '//# sourceMappingURL=../maps/bundle.js.map'
        got = M.sourcemap_urls(js, "https://a.com/js/bundle.js")
        self.assertEqual(got, [("file", "https://a.com/maps/bundle.js.map")])

    def test_inline_data_uri(self):
        js = '//# sourceMappingURL=data:application/json;base64,eyJ2ZXJzaW9uIjozfQ==\n'
        got = M.sourcemap_urls(js, "https://a.com/app.js")
        self.assertEqual(got, [("inline", "data:application/json;base64,eyJ2ZXJzaW9uIjozfQ==")])

    def test_with_query(self):
        js = '//# sourceMappingURL=app.js.map?v=3'
        got = M.sourcemap_urls(js, "https://a.com/app.js")
        self.assertEqual(got, [("file", "https://a.com/app.js.map?v=3")])

    def test_none(self):
        self.assertEqual(M.sourcemap_urls("console.log(1)", "https://a.com/x.js"), [])

    def test_multiple_takes_all(self):
        js = '//# sourceMappingURL=a.map\n//# sourceMappingURL=b.map\n'
        got = M.sourcemap_urls(js, "https://a.com/x.js")
        self.assertEqual([u for _, u in got],
                         ["https://a.com/a.map", "https://a.com/b.map"])


class TestScriptSrcs(unittest.TestCase):
    HTML = (
        '<html><head>'
        '<script src="/assets/app.a1.js"></script>'
        '<script src="https://cdn.b.com/lib.js" defer></script>'
        '<script>inline()</script>'
        '<script src=\'/theme.css\'></script>'
        '</head></html>'
    )

    def test_extracts_js_only(self):
        got = M.script_srcs(self.HTML)
        self.assertEqual(got, ["/assets/app.a1.js", "https://cdn.b.com/lib.js"])

    def test_absolute_resolution(self):
        got = M.script_srcs(self.HTML, "https://a.com/page")
        self.assertIn("https://a.com/assets/app.a1.js", got)
        self.assertIn("https://cdn.b.com/lib.js", got)


class TestTailCheck(unittest.TestCase):
    def test_tail_of(self):
        self.assertEqual(M.tail_of(b"abcdef", 3), b"def")
        self.assertEqual(M.tail_of(b"ab", 5), b"ab")


class TestRender(unittest.TestCase):
    SITES = [
        {"domain": "a.com", "ok": True, "js_seen": 6, "map_ref": 2,
         "map_inline": 0, "map_reachable": 1, "errors": []},
        {"domain": "b.com", "ok": True, "js_seen": 6, "map_ref": 0,
         "map_inline": 0, "map_reachable": 0, "errors": []},
        {"domain": "c.com", "ok": False, "js_seen": 0, "map_ref": 0,
         "map_inline": 0, "map_reachable": 0, "errors": ["timeout"]},
    ]

    def test_render_counts(self):
        text = M.render_report(self.SITES)
        self.assertIn("有效站 2", text)
        self.assertIn("file 型） | 1 | 50.0%", text)   # a.com 引用，b.com 没有
        self.assertIn("**.map 公开可达（暴露）** | **1**", text)
        self.assertIn("c.com", text)        # 失败站列出

    def test_verdict(self):
        sites = [dict(s, map_reachable=1) for s in self.SITES[:2]]
        text = M.render_report(sites)
        self.assertIn("50.0%", text)


if __name__ == "__main__":
    unittest.main()
