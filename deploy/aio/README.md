# sub2api 前置端点（AIO listener）

sub2api 的前置组只认外部 AIO 的 listener（`docs/PROXY_IP_MODEL.md` 第 2.2 节）。本目录生成一份 sub2api 专用的 mihomo 配置：节点和机场与个人自用订阅同源，但分组、规则、listener 独立，不改共享在线订阅。

## listener 规则

| listener | 端口 | 分组 | 说明 |
|---|---|---|---|
| `sub2api-xyy` | 17911 | `xyy`（select，只有自建 xyy 节点） | 自建节点 |
| `sub2api-bwg` | 17912 | `bwg`（select，只有自建 bwg 节点） | 自建节点 |
| `sub2api-laomao` | 17913 | `laomao`（load-balance consistent-hashing） | 机场，成员为空时 `REJECT` |

- 每个 listener 固定指向一个分组，一个分组只对应一个出口来源。自建节点组里只有一个节点，节点挂了 listener 直接失败，由 sub2api 槽位切到备用端点；AIO 侧不做跨来源的 fallback，免得 sub2api 看不到真实故障。
- 机场组按目标（即落地 IP）软绑定节点，同一落地固定走同一机场节点；`empty-fallback: REJECT`，组内不放 DIRECT。
- 规则只有 `MATCH,REJECT`：listener 显式指定了分组、不经规则；AIO 自动注入的 mixed-port 等其他入口一律拒绝。
- 机场节点过滤掉流量、到期、官网等提示条目（`exclude-filter` 与自用订阅一致）。
- 与自用订阅的区别：不带分流规则、DNS、TUN、`🔰 节点选择`、链式代理等自用分组，也没有 url-test 自动选择。

## 生成配置

节点与机场订阅地址从一份 mihomo 配置（在线订阅的下载副本）按名字提取，仓库里不存凭据：

```bash
python3 -I deploy/aio/render_upstream_config.py \
  --source /path/to/subscription.yaml \
  --output /path/to/sub2api-upstream.yaml
```

- 节点名默认 `[自建]xyy`、`[自建]bwg`，机场 provider 默认 `laomao`，可用 `--xyy-node`、`--bwg-node`、`--airport-provider` 改。
- 机场订阅经内部分组 `subscription-fetch` 抓取（fallback：DIRECT → xyy → bwg，按订阅域名探测）。直连抓不到时自动改走自建节点，不需要按环境改参数。启动时首次拉取早于该组的健康检查，可能先失败一次，mihomo 会按退避重试（testenv 实测约 30 秒内切到 xyy 并拉到节点）。
- listener 不开认证：默认容器化部署，AIO 与 sub2api 只经内部网络通信。不要把 17911–17913 发布到宿主或公网。
- 输出文件 0600，含节点凭据，放在运维中心，不要提交。

导入 AIO：在 AIO 控制台新建一个本地（local）profile，内容为生成的配置，激活。sub2api 专用 AIO 实例里不要再启用 merge / script 覆盖，否则会改掉分组与规则。激活后核心会重启，先等 provider 健康检查完成，再在 sub2api 后台测试落地。

## 在 sub2api 登记

前置端点（后台「前置组 / 端点」，协议 socks5，主机为 AIO 地址）：

| 端点 | 端口 |
|---|---|
| `aio-xyy` | 17911 |
| `aio-bwg` | 17912 |
| `aio-laomao` | 17913 |

上线时 bwg 故障，前置组 `aio` 只配一个槽位：

| 槽位 | 端点顺序 |
|---|---|
| `slot-1` | `aio-xyy`（主）→ `aio-laomao`（备） |

bwg 修复后加第二个槽位，组内两个槽位并发，落地按 rendezvous hash 分摊（约一半落地改到新槽位，落地 IP 不变）：

| 槽位 | 端点顺序 |
|---|---|
| `slot-1` | `aio-xyy` → `aio-laomao` |
| `slot-2` | `aio-bwg` → `aio-laomao` |

`aio-bwg` 端点可以先登记好（不放进槽位就不会被使用），也不会被探测。

## 验证记录

2026-10-29 在 testenv 网络用独立 mihomo v1.19.29 容器验证（未改 testenv AIO）：`mihomo -t` 通过；testenv 直连抓不到机场订阅，`subscription-fetch` 自动切到 xyy 后拉到 50 个节点；xyy、laomao listener 出口正常，bwg 超时（与 bwg 当前故障一致）；机场成员过滤为空时 laomao listener 拒绝连接、未直连。xyy 在核心启动后的第一条连接会超时（anytls 建会话），之后稳定；sub2api 需连续 3 次探测失败才判端点不可用，不受影响。
