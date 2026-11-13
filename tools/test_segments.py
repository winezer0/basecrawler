#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_segments.py —— segments.py 纯函数单测。"""
import unittest

import segments


class TestPathSegments(unittest.TestCase):
    def test_basic(self):
        self.assertEqual(segments.path_segments("https://a.com/api/users"), ["api", "users"])
        self.assertEqual(segments.path_segments("https://a.com/api/users?page=1"), ["api", "users"])
        self.assertEqual(segments.path_segments("https://a.com/"), [])

    def test_trailing_slash(self):
        self.assertEqual(segments.path_segments("https://a.com/api/users/"), ["api", "users"])

    def test_three_segments(self):
        self.assertEqual(segments.path_segments("/api/v1/users"), ["api", "v1", "users"])


class TestMissedTwoSeg(unittest.TestCase):
    def test_only_two_segment_misses(self):
        auto = {"https://a.com/api/v1/users", "https://a.com/old"}
        regex = {
            "https://a.com/api/v1/users",   # 共有
            "https://a.com/api/users",      # 2 段漏提
            "https://a.com/old",            # 共有
            "https://a.com/x/y/z",          # 3 段漏提（不算本闸门口径）
        }
        missed = segments.missed_two_seg(auto, regex)
        self.assertEqual(missed, ["https://a.com/api/users"])

    def test_empty(self):
        self.assertEqual(segments.missed_two_seg(set(), set()), [])

    def test_order_stable(self):
        auto = set()
        regex = {"https://a.com/b/b2", "https://a.com/a/a2"}
        got = segments.missed_two_seg(auto, regex)
        self.assertEqual(got, sorted(got))  # 输出确定


class TestRenderReport(unittest.TestCase):
    def test_verdict_over_threshold(self):
        rows = [{
            "name": "s1", "auto": 10, "regex": 12,
            "missed": ["https://a.com/api/users", "https://a.com/api/items"],
            "missed_live": 2, "missed_dead": 0,
        }]
        text = segments.render_report(rows, threshold=0.05)
        self.assertIn("放宽", text)
        self.assertIn("s1", text)

    def test_verdict_under_threshold(self):
        rows = [{
            "name": "s1", "auto": 10, "regex": 11, "missed": ["https://a.com/api/x"],
            "missed_live": 0, "missed_dead": 1,
        }]
        text = segments.render_report(rows, threshold=0.5)
        self.assertIn("不做", text)


if __name__ == "__main__":
    unittest.main()
