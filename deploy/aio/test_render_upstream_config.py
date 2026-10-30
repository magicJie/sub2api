import importlib.util
import os
import sys
import tempfile
import unittest

import yaml

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("render", os.path.join(HERE, "render_upstream_config.py"))
render = importlib.util.module_from_spec(spec)
spec.loader.exec_module(render)

SOURCE = {
    "proxies": [
        {"name": "[自建]xyy", "type": "anytls", "server": "198.51.100.1", "port": 443, "password": "x",
         "dialer-proxy": "首跳"},
        {"name": "[自建]bwg", "type": "vless", "server": "198.51.100.2", "port": 443, "uuid": "y"},
        {"name": "桌面节点", "type": "ss", "server": "198.51.100.3", "port": 1, "cipher": "aes-128-gcm", "password": "z"},
    ],
    "proxy-providers": {"laomao": {"type": "http", "url": "https://user@sub.example.com/api?token=t"}},
    "proxy-groups": [{"name": "🔰 节点选择", "type": "select", "proxies": ["桌面节点"]}],
    "rules": ["DOMAIN-SUFFIX,example.com,🔰 节点选择", "MATCH,DIRECT"],
    "tun": {"enable": True},
}


def build(**overrides):
    argv = ["--source", "s", "--output", "o"]
    for k, v in overrides.items():
        argv += [f"--{k.replace('_', '-')}", str(v)]
    sys.argv = ["render"] + argv
    return render.build(render.parse_args(), SOURCE)


class BuildTest(unittest.TestCase):
    def test_listeners_are_generic_slots_per_source(self):
        cfg = build()
        self.assertEqual([(l["name"], l["port"], l["proxy"]) for l in cfg["listeners"]],
                         [("upstream-xyy", 17911, "xyy"), ("upstream-bwg", 17912, "bwg"),
                          ("upstream-laomao", 17913, "laomao")])

    def test_no_desktop_rules_groups_or_tun(self):
        cfg = build()
        self.assertEqual(cfg["rules"], ["MATCH,REJECT"])
        self.assertNotIn("tun", cfg)
        self.assertNotIn("dns", cfg)
        names = [g["name"] for g in cfg["proxy-groups"]]
        self.assertNotIn("🔰 节点选择", names)
        self.assertNotIn("桌面节点", [p["name"] for p in cfg["proxies"]])

    def test_nodes_extracted_without_dialer_proxy(self):
        xyy = build()["proxies"][0]
        self.assertEqual((xyy["name"], xyy["server"]), ("node-xyy", "198.51.100.1"))
        self.assertNotIn("dialer-proxy", xyy)

    def test_airport_provider_and_fetch_probe(self):
        cfg = build()
        self.assertEqual(cfg["proxy-providers"]["laomao"]["path"], "./proxy_providers/upstream-laomao.yaml")
        fetch = next(g for g in cfg["proxy-groups"] if g["name"] == "subscription-fetch")
        self.assertEqual(fetch["url"], "https://sub.example.com/")
        self.assertEqual(cfg["proxy-providers"]["laomao"]["proxy"], "subscription-fetch")
        laomao = next(g for g in cfg["proxy-groups"] if g["name"] == "laomao")
        self.assertEqual(laomao["empty-fallback"], "REJECT")
        self.assertNotIn("DIRECT", laomao.get("proxies", []))

    def test_fetch_group_prefers_own_nodes_over_direct(self):
        # 全部成员探测失败时 mihomo fallback 用第一个成员；DIRECT 在首位会在直连被阻断时卡住（2026-10-30 uat）。
        fetch = next(g for g in build()["proxy-groups"] if g["name"] == "subscription-fetch")
        self.assertEqual(fetch["type"], "fallback")
        self.assertEqual(fetch["proxies"], ["node-xyy", "node-bwg", "DIRECT"])
        self.assertEqual((fetch["interval"], fetch["timeout"], fetch["lazy"]), (60, 5000, False))
        self.assertNotIn("expected-status", fetch)

    def test_anytls_session_defaults_only_for_anytls(self):
        xyy, bwg = build()["proxies"]
        self.assertEqual((xyy["idle-session-timeout"], xyy["idle-session-check-interval"], xyy["min-idle-session"]),
                         (300, 60, 1))
        self.assertNotIn("idle-session-timeout", bwg)

    def test_anytls_session_value_from_source_wins(self):
        SOURCE["proxies"][0]["idle-session-timeout"] = 120
        try:
            self.assertEqual(build()["proxies"][0]["idle-session-timeout"], 120)
        finally:
            del SOURCE["proxies"][0]["idle-session-timeout"]

    def test_ports_overridable(self):
        cfg = build(xyy_port=27911)
        self.assertEqual(cfg["listeners"][0]["port"], 27911)

    def test_output_file_mode_0600(self):
        with tempfile.TemporaryDirectory() as d:
            src, out = os.path.join(d, "s.yaml"), os.path.join(d, "o.yaml")
            with open(src, "w", encoding="utf-8") as f:
                yaml.safe_dump(SOURCE, f, allow_unicode=True)
            sys.argv = ["render", "--source", src, "--output", out]
            render.main()
            self.assertEqual(os.stat(out).st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
