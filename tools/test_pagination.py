#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_pagination.py —— pagination.py 纯函数单测。

只测三类「下一页」解析；网络与子进程由 CLI 薄层承担。
"""
import unittest

import pagination as P


class TestNextFromBody(unittest.TestCase):
    def test_explicit_next(self):
        doc = {"next": "https://pokeapi.co/api/v2/pokemon?limit=20&offset=20", "count": 1}
        got = P.next_from_body(doc, "https://pokeapi.co/api/v2/pokemon?limit=20")
        self.assertEqual(got, ["https://pokeapi.co/api/v2/pokemon?limit=20&offset=20"])

    def test_relative_next(self):
        doc = {"next_url": "/products?skip=10"}
        got = P.next_from_body(doc, "https://dummyjson.com/products?skip=0")
        self.assertEqual(got, ["https://dummyjson.com/products?skip=10"])

    def test_ignores_non_url(self):
        doc = {"next": "load more", "next_cursor": ""}
        self.assertEqual(P.next_from_body(doc, "https://a.com/x"), [])

    def test_nested(self):
        doc = {"pagination": {"nextPage": "/p/2"}}
        got = P.next_from_body(doc, "https://a.com/p/1")
        self.assertEqual(got, ["https://a.com/p/2"])

    def test_last_page_next_null(self):
        doc = {"next": None, "items": []}
        self.assertEqual(P.next_from_body(doc, "https://a.com/x"), [])


class TestNextComputed(unittest.TestCase):
    def test_algolia_style(self):
        doc = {"page": 0, "nbPages": 3, "hits": []}
        url = "https://hn.algolia.com/api/v1/search?query=go&page=0"
        got = P.next_computed(doc, url)
        self.assertEqual(got, ["https://hn.algolia.com/api/v1/search?query=go&page=1"])

    def test_algolia_last_page(self):
        doc = {"page": 2, "nbPages": 3, "hits": []}
        self.assertEqual(P.next_computed(doc, "https://a.com/x?page=2"), [])

    def test_has_more_with_page(self):
        doc = {"has_more": True, "items": [1]}
        got = P.next_computed(doc, "https://api.x.com/2.3/tags?page=1&pagesize=10")
        self.assertEqual(got, ["https://api.x.com/2.3/tags?page=2&pagesize=10"])

    def test_has_more_false(self):
        doc = {"has_more": False, "items": [1]}
        self.assertEqual(P.next_computed(doc, "https://a.com/x?page=1"), [])

    def test_has_more_without_page_param(self):
        # 没有 page 参数时无法构造下一页，不产出
        doc = {"has_more": True}
        self.assertEqual(P.next_computed(doc, "https://a.com/x"), [])

    def test_wikipedia_continue(self):
        doc = {"continue": {"apcontinue": "Aaron", "continue": "gapcontinue||"}}
        url = "https://en.wikipedia.org/w/api.php?action=query&list=allpages&aplimit=5&format=json"
        got = P.next_computed(doc, url)
        self.assertTrue(got and "apcontinue=Aaron" in got[0] and "gapcontinue%7C%7C" in got[0]
                        or got and "apcontinue=Aaron" in got[0])

    def test_no_pattern(self):
        self.assertEqual(P.next_computed({"items": []}, "https://a.com/x"), [])

    def test_skip_limit_total(self):
        doc = {"total": 30, "skip": 0, "limit": 10, "items": []}
        got = P.next_computed(doc, "https://dummyjson.com/products?limit=10&skip=0")
        self.assertEqual(got, ["https://dummyjson.com/products?limit=10&skip=10"])

    def test_skip_limit_last_page(self):
        doc = {"total": 30, "skip": 20, "limit": 10, "items": []}
        self.assertEqual(
            P.next_computed(doc, "https://dummyjson.com/products?limit=10&skip=20"), [])


class TestNextFromLinkHeader(unittest.TestCase):
    def test_rel_next(self):
        hv = '<https://api.github.com/repos/g/go/commits?per_page=5&page=2>; rel="next", ' \
             '<https://api.github.com/repos/g/go/commits?per_page=5&page=57>; rel="last"'
        got = P.next_from_link_header(hv, "https://api.github.com/repos/g/go/commits?per_page=5")
        self.assertEqual(got, ["https://api.github.com/repos/g/go/commits?per_page=5&page=2"])

    def test_ignores_other_rels(self):
        hv = '<https://a.com/p/1>; rel="prev", <https://a.com/p/5>; rel="last"'
        self.assertEqual(P.next_from_link_header(hv, "https://a.com/p/2"), [])

    def test_absolute_only(self):
        # 相对 URL 的 Link 头按基准解析
        hv = '</commits?page=2>; rel="next"'
        got = P.next_from_link_header(hv, "https://api.github.com/repos/g/go")
        self.assertEqual(got, ["https://api.github.com/commits?page=2"])


class TestSameURL(unittest.TestCase):
    def test_query_order_insensitive(self):
        self.assertTrue(P.same_url(
            "https://a.com/x?page=1&query=go",
            "https://a.com/x?query=go&page=1"))

    def test_different_query(self):
        self.assertFalse(P.same_url(
            "https://a.com/x?page=1", "https://a.com/x?page=2"))

    def test_no_query(self):
        self.assertTrue(P.same_url("https://a.com/x", "https://a.com/x"))


class TestClassify(unittest.TestCase):
    def test_classify_table(self):
        self.assertEqual(P.classify("explicit"), "显式字段")
        self.assertEqual(P.classify("computed"), "计算型")
        self.assertEqual(P.classify("link-header"), "Link 头")
        self.assertEqual(P.classify("none"), "无分页")


class TestRender(unittest.TestCase):
    def _row(self, name, kind, chain, got):
        missed = [u for u in chain if u not in got]
        return {"name": name, "kind": kind, "chain_pages": len(chain),
                "chain_urls": chain, "crawled": len(got),
                "covered": len(chain) - len(missed),
                "page2_covered": len(chain) > 1 and chain[1] in got,
                "missed": missed, "page1_next": {}, "url": chain[0]}

    def test_control_confirmed_and_computed_missed(self):
        chain = [f"https://a.com/p?page={i}" for i in range(1, 6)]
        rows = [
            self._row("ctl", "explicit", chain, set(chain[:2])),
            self._row("comp", "computed", chain, {chain[0]}),
        ]
        text = P.render_report(rows)
        self.assertIn("第 2 页未覆盖 0 站", text)
        self.assertIn("漏检合计 4 条", text)
        self.assertIn("立项实现", text)
        ctl_line = next(l for l in text.splitlines() if l.startswith("| ctl"))
        self.assertIn("已覆盖", ctl_line)  # 对照行第 2 页列

    def test_control_gap_flagged(self):
        chain = [f"https://a.com/p?page={i}" for i in range(1, 4)]
        rows = [self._row("ctl", "link-header", chain, {chain[0]})]
        text = P.render_report(rows)
        self.assertIn("第 2 页未覆盖 1 站", text)
        self.assertIn("需复查", text)

    def test_computed_zero_miss_records_no(self):
        chain = ["https://a.com/p?page=1"]
        rows = [self._row("comp", "computed", chain, {chain[0]})]
        text = P.render_report(rows)
        self.assertIn("记「不做」", text)


if __name__ == "__main__":
    unittest.main()
