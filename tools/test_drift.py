#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""drift.py 的裁定逻辑单测：全部用合成集合，不依赖网络与真实样本。"""
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import drift  # noqa: E402

H = "example.com"


def u(p, host=H):
    return host + p


class DiffTest(unittest.TestCase):
    def test_make_diff(self):
        d = drift.make_diff({"/a", "/b"}, {"/b", "/c"})
        self.assertEqual(d.added, frozenset({"/c"}))
        self.assertEqual(d.lost, frozenset({"/a"}))
        self.assertEqual(d.size, 2)

    def test_host_groups_sorted_by_count(self):
        groups = drift.host_groups({
            u("/1", "b.example.com"), u("/2", "b.example.com"), u("/3", "a.example.com"),
        })
        self.assertEqual(list(groups.items())[0][0], "b.example.com")
        self.assertTrue(drift.is_subdomain("b.example.com", H))
        self.assertFalse(drift.is_subdomain(H, H))

    def test_stable_of_set_normalizes_hashes(self):
        s = drift.stable_of_set({u("/app.ABCDEF12.js"), u("/plain.js")})
        self.assertIn(u("/app.<HASH>.js"), s)
        self.assertIn(u("/plain.js"), s)


class VerdictTest(unittest.TestCase):
    def test_identical(self):
        same = {u("/a"), u("/b")}
        r = drift.adjudicate(same, same, same, same, same, H)
        self.assertEqual(r["verdict"], drift.VERDICT_IDENTICAL)
        self.assertEqual(r["old_to_new"], {"added": 0, "lost": 0})

    def test_old_not_stable_means_no_evidence(self):
        base = {u("/a"), u("/b")}
        old1 = {u("/a"), u("/b")}
        old2 = {u("/a")}          # 旧代码第二次自己就丢了 /b
        new1 = {u("/a")}
        new2 = {u("/a")}
        r = drift.adjudicate(base, old1, new1, old2, new2, H)
        self.assertEqual(r["verdict"], drift.VERDICT_NO_EVIDENCE)
        self.assertEqual(r["hard_lost"], [])

    def test_stable_loss_is_suspect(self):
        stable = {u("/a"), u("/b"), u("/c")}
        shrunk = {u("/a"), u("/b")}
        r = drift.adjudicate(stable, stable, shrunk, stable, shrunk, H)
        self.assertEqual(r["verdict"], drift.VERDICT_SUSPECT)
        self.assertEqual(r["hard_lost"], [u("/c")])

    def test_subdomain_rotation_is_not_regression(self):
        old = {u("/a"), u("/assets/x.js", "v3.example.com")}
        new = {u("/a"), u("/assets/x.js", "v5.example.com")}
        r = drift.adjudicate(old, old, new, old, new, H)
        self.assertEqual(r["verdict"], drift.VERDICT_NO_EVIDENCE)
        self.assertTrue(any("子域" in x for x in r["reasons"]))

    def test_noise_ceiling_downgrades_to_review(self):
        base = {u(f"/p{i}") for i in range(12)}
        old1 = set(base)
        old2 = {u(f"/p{i}") for i in range(11)}   # 连跑噪声 1
        new1 = {u(f"/p{i}") for i in range(11)}
        new2 = {u(f"/p{i}") for i in range(10)}
        r = drift.adjudicate(base, old1, new1, old2, new2, H)
        self.assertEqual(r["verdict"], drift.VERDICT_REVIEW)
        self.assertIn("连跑噪声", " ".join(r["reasons"]))

    def test_baseline_loss_identical_is_site_drift(self):
        base = {u("/a"), u("/b"), u("/c"), u("/d")}
        old = {u("/a"), u("/b"), u("/x")}
        new = {u("/a"), u("/b"), u("/y")}
        r = drift.adjudicate(base, old, new, old, new, H)
        self.assertTrue(r["baseline_lost_matches"])
        self.assertEqual(r["verdict"], drift.VERDICT_NO_EVIDENCE)
        self.assertTrue(any("完全一致" in x for x in r["reasons"]))

    def test_different_baseline_loss_keeps_suspect(self):
        base = {u("/a"), u("/b"), u("/c")}
        old = {u("/a"), u("/b"), u("/c"), u("/x")}
        new = {u("/a"), u("/b")}
        r = drift.adjudicate(base, old, new, old, new, H)
        self.assertFalse(r["baseline_lost_matches"])
        self.assertEqual(r["verdict"], drift.VERDICT_SUSPECT)

    def test_quota_note(self):
        base = {u(f"/p{i}") for i in range(100)}
        r = drift.adjudicate(base, base, base, base, base, H, quota=100)
        self.assertTrue(any("配额" in x for x in r["reasons"]))


if __name__ == "__main__":
    unittest.main()
