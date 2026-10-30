#!/usr/bin/env python3
"""生成服务端通用的 AIO（mihomo）配置。

桌面与服务端是两套在线配置：桌面订阅带分流规则；本脚本从桌面订阅提取节点与机场，生成服务端配置。
服务端配置只提供按出口来源划分的 listener 槽位（xyy / bwg / laomao），sub2api、Ant 等服务共用，
主备与槽位由各服务自己配置（sub2api 见 docs/PROXY_IP_MODEL.md 第 2.2 节）。每个 listener 固定指向一个分组，
规则兜底 REJECT，不放 DIRECT。

节点与机场订阅地址从一份 mihomo 配置（通常是在线订阅的下载副本）里按名字提取，仓库里不存凭据：

    python3 -I deploy/aio/render_upstream_config.py --source /path/to/subscription.yaml \
        --output /path/to/server-upstream.yaml

listener 不开认证：默认容器化部署，AIO 与各服务只经内部网络通信，不要把 listener 端口发布到宿主或公网。
输出文件权限 0600，含节点凭据，不要提交。
"""

import argparse
import os
import sys
from urllib.parse import urlsplit

import yaml

# 机场节点里的非业务条目（流量、到期提示、官网等）。
AIRPORT_EXCLUDE = "游戏|bt|剩余|过期|到期|重置|官网|有效期|谷歌学术|不保证可用|请更新|只显示|订阅|发布页|最新域名|推荐"
PROBE_URL = "https://www.gstatic.com/generate_204"
ANYTLS_SESSION_DEFAULTS = {
    "idle-session-check-interval": 60,
    "idle-session-timeout": 300,
    "min-idle-session": 1,
}


def parse_args():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--source", required=True, help="提取节点与机场订阅地址的 mihomo 配置")
    p.add_argument("--output", required=True, help="输出的服务端配置路径")
    p.add_argument("--xyy-node", default="[自建]xyy", help="源配置中 xyy 节点名")
    p.add_argument("--bwg-node", default="[自建]bwg", help="源配置中 bwg 节点名")
    p.add_argument("--airport-provider", default="laomao", help="源配置中机场 proxy-provider 名")
    p.add_argument("--listen", default="0.0.0.0", help="listener 监听地址")
    p.add_argument("--xyy-port", type=int, default=17911)
    p.add_argument("--bwg-port", type=int, default=17912)
    p.add_argument("--laomao-port", type=int, default=17913)
    return p.parse_args()


def fail(msg):
    print(f"render_upstream_config: {msg}", file=sys.stderr)
    sys.exit(1)


def pick_node(source, name, alias):
    for node in source.get("proxies") or []:
        if node.get("name") == name:
            node = dict(node)
            node["name"] = alias
            node.pop("dialer-proxy", None)  # 前置节点本身直连，不再串其他节点
            if node.get("type") == "anytls":
                # 服务端探测周期约 30s，与 mihomo 默认空闲回收（30s）同频，几乎每次探测都要新建 TLS 会话；
                # 新握手在部分链路上会成段被阻断（2026-10-29 实测）。保留常驻会话并延长空闲时间，订阅里已写的值优先。
                for key, value in ANYTLS_SESSION_DEFAULTS.items():
                    node.setdefault(key, value)
            return node
    fail(f"源配置中没有节点 {name!r}")


def build(args, source):
    provider = (source.get("proxy-providers") or {}).get(args.airport_provider)
    if not provider or not provider.get("url"):
        fail(f"源配置中没有带 url 的 proxy-provider {args.airport_provider!r}")

    # 抓取组按订阅域名本身判断可达（只取 scheme+host，不带 token；不限状态码，能建立 HTTPS 即可）。
    sub = urlsplit(provider["url"])
    fetch_probe = f"{sub.scheme}://{sub.netloc.rsplit('@', 1)[-1]}/"

    def listener(name, port, group):
        return {"name": f"upstream-{name}", "type": "mixed", "listen": args.listen, "port": port, "proxy": group, "udp": False}

    airport = {
        "type": "http",
        "url": provider["url"],
        "path": "./proxy_providers/upstream-laomao.yaml",
        "interval": 28800,
        "exclude-filter": AIRPORT_EXCLUDE,
        "override": {"additional-prefix": "[laomao] "},
        "health-check": {"enable": True, "url": PROBE_URL, "interval": 300, "timeout": 5000, "lazy": False,
                         "expected-status": 204},
        "proxy": "subscription-fetch",
    }

    return {
        "mode": "rule",
        "log-level": "warning",
        "ipv6": False,
        "allow-lan": True,
        "bind-address": "*",
        "unified-delay": True,
        "tcp-concurrent": True,
        "profile": {"store-selected": False, "store-fake-ip": False},
        "proxies": [
            pick_node(source, args.xyy_node, "node-xyy"),
            pick_node(source, args.bwg_node, "node-bwg"),
        ],
        "proxy-providers": {"laomao": airport},
        "proxy-groups": [
            # 自建节点各一组，组内只有一个节点：节点故障时 listener 直接失败，由服务侧槽位切到备用端点。
            {"name": "xyy", "type": "select", "proxies": ["node-xyy"]},
            {"name": "bwg", "type": "select", "proxies": ["node-bwg"]},
            # 机场：按目标（落地 IP）软绑定节点；成员为空时拒绝，不回退直连。
            {"name": "laomao", "type": "load-balance", "strategy": "consistent-hashing", "use": ["laomao"],
             "url": PROBE_URL, "interval": 300, "lazy": False, "empty-fallback": "REJECT"},
            # 只用于抓取机场订阅，不挂 listener：直连优先，直连不通时依次经自建节点抓。
            {"name": "subscription-fetch", "type": "fallback", "proxies": ["DIRECT", "node-xyy", "node-bwg"],
             "url": fetch_probe, "interval": 300, "lazy": False},
        ],
        "listeners": [
            listener("xyy", args.xyy_port, "xyy"),
            listener("bwg", args.bwg_port, "bwg"),
            listener("laomao", args.laomao_port, "laomao"),
        ],
        # listener 显式指定了出站分组，不经规则；其余入口（AIO 注入的 mixed-port 等）一律拒绝。
        "rules": ["MATCH,REJECT"],
    }


def main():
    args = parse_args()
    with open(args.source, encoding="utf-8") as f:
        source = yaml.safe_load(f) or {}
    config = build(args, source)
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write("# 服务端通用 AIO 配置，由 deploy/aio/render_upstream_config.py 从桌面订阅生成；含节点凭据，勿提交，不要导入桌面客户端。\n")
        yaml.safe_dump(config, f, allow_unicode=True, sort_keys=False)
    print(f"已生成 {args.output}：listener xyy={args.xyy_port} bwg={args.bwg_port} laomao={args.laomao_port}")


if __name__ == "__main__":
    main()
