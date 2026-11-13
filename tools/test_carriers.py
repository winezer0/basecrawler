#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""carriers.py 的纯函数单测：不联网，全部用内联 HTML / 头。"""
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import carriers  # noqa: E402

BASE = "https://example.com/"


class ParseTest(unittest.TestCase):
    def test_link_header(self):
        got = carriers.parse_link_header(
            '</a.js>; rel=preload; as=script, </b.js>; rel="modulepreload"'
        )
        self.assertEqual(got, [("/a.js", "preload"), ("/b.js", "modulepreload")])

    def test_importmap_urls(self):
        text = '{"imports":{"vue":"/assets/vue.js","x":"https://cdn.example/x.js"},' \
               '"scopes":{"/s/":{"k":"/assets/k.js"}}}'
        self.assertEqual(
            carriers.importmap_urls(text),
            {"/assets/vue.js", "https://cdn.example/x.js", "/assets/k.js"},
        )

    def test_manifest_urls(self):
        text = '{"start_url":"/app","icons":[{"src":"/i.png"}],' \
               '"serviceworker":{"src":"/sw.js"}}'
        self.assertEqual(
            carriers.manifest_urls(text), {"/app", "/i.png", "/sw.js"}
        )

    def test_absolute(self):
        self.assertEqual(carriers.absolute("/a.js", BASE), "https://example.com/a.js")
        self.assertEqual(carriers.absolute("../a.js", BASE + "x/y"), "https://example.com/a.js")
        self.assertEqual(carriers.absolute("data:foo", BASE), "")


class CollectTest(unittest.TestCase):
    def test_link_and_hydration(self):
        html = """<html><head>
          <link rel="preload" as="script" href="/a.js">
          <link rel="modulepreload" href="/b.js">
          <link rel="manifest" href="/m.webmanifest">
          <script type="importmap">{"imports":{"x":"/c.js"}}</script>
          <script id="__NUXT_DATA__" type="application/json">[]</script>
          <script id="__remixContext" type="application/json">{}</script>
        </head></html>"""
        headers = {"Link": '</head.js>; rel=preload; as=script'}
        found = carriers.collect(BASE, headers, html,
                                 '{"start_url":"/start","icons":[{"src":"/i.png"}]}')
        self.assertIn("https://example.com/head.js", found["link-header"])
        self.assertIn("https://example.com/a.js", found["link-preload"])
        self.assertIn("https://example.com/b.js", found["link-modulepreload"])
        self.assertIn("https://example.com/m.webmanifest", found["link-manifest"])
        self.assertIn("https://example.com/c.js", found["importmap"])
        self.assertIn("https://example.com/start", found["manifest-content"])
        self.assertEqual(
            found["hydration-script"], {"__NUXT_DATA__", "__remixContext"}
        )

    def test_serviceworker_and_data(self):
        html = """<html><script>
          navigator.serviceWorker.register('/sw.js');
          const u = "/blog/__data.json";
        </script></html>"""
        found = carriers.collect(BASE, {}, html)
        self.assertIn("https://example.com/sw.js", found["serviceworker"])
        self.assertIn("https://example.com/blog/__data.json", found["sveltekit-data"])

    def test_ignores_javascript_urls(self):
        found = carriers.collect(BASE, {}, '<a href="javascript:void(0)">x</a>')
        self.assertEqual(sum(len(v) for v in found.values()), 0)


if __name__ == "__main__":
    unittest.main()
