#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""baseline.sites.json 的配置完整性测试。纯离线，不发网络请求。"""
import json
import sys
import unittest
from pathlib import Path
from urllib.parse import urlparse

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(HERE))
import rebaseline  # noqa: E402


class SitesConfigTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cfg = json.loads((HERE / "baseline.sites.json").read_text(encoding="utf-8"))

    def test_site_count(self):
        self.assertGreaterEqual(len(self.cfg["sites"]), 3)

    def test_host_matches_url_and_baseline_exists(self):
        for s in self.cfg["sites"]:
            self.assertEqual(urlparse(s["url"]).hostname, s["host"])
            self.assertTrue((ROOT / s["baseline"]).exists(), s["baseline"])
            self.assertGreater(s["quota"], 0)

    def test_fixed_flags_shut_down_active_behavior(self):
        flags = self.cfg["flags"]
        for required in (
            "--no-fuzz", "--no-probe-openapi", "--no-route-expand",
            "--no-deobf", "--no-route-exec", "--no-deobf-exec",
            "--no-robots-disallow", "--no-testfill", "-s", "all", "-O",
        ):
            self.assertIn(required, flags)

    def test_commands_are_pure(self):
        cmds = list(rebaseline.commands(self.cfg, "bin", "/tmp/out", ""))
        self.assertEqual(len(cmds), len(self.cfg["sites"]))
        for site, cmd, _ in cmds:
            self.assertEqual(cmd[0], "bin")
            self.assertIn(site["url"], cmd)
            self.assertIn("-o", cmd)


if __name__ == "__main__":
    unittest.main()
