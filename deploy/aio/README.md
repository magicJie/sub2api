# 服务端通用 AIO 配置（前置 listener 槽位）

桌面和服务端用两套在线配置：

- **桌面订阅**：带分流规则、DNS、TUN，给个人客户端用。
- **服务端配置**：本目录脚本从桌面订阅提取节点和机场生成，只提供按出口来源划分的 listener 槽位。sub2api、Ant 等服务端共用这份配置；每个网络环境（home、testenv、集群）各跑一个 AIO 实例。主备与槽位由各服务自己配置，sub2api 的前置组见 `docs/PROXY_IP_MODEL.md` 第 2.2 节。

两份配置的节点和机场同源。改了桌面订阅里的节点或机场后，重新生成服务端配置并发布，不要手工维护两份。

## listener 规则

| listener | 端口 | 分组 | 说明 |
|---|---|---|---|
| `upstream-xyy` | 17911 | `xyy`（select，只有自建 xyy 节点） | 自建节点 |
| `upstream-bwg` | 17912 | `bwg`（select，只有自建 bwg 节点） | 自建节点 |
| `upstream-laomao` | 17913 | `laomao`（load-balance consistent-hashing） | 机场，成员为空时 `REJECT` |

- 每个 listener 固定指向一个分组，一个分组只对应一个出口来源。自建节点组里只有一个节点，节点挂了 listener 直接失败，由服务侧槽位切到备用端点；AIO 侧不做跨来源的 fallback，免得服务看不到真实故障。
- 机场组按目标（即落地 IP）软绑定节点，同一落地固定走同一机场节点；`empty-fallback: REJECT`，组内不放 DIRECT。
- 规则只有 `MATCH,REJECT`：listener 显式指定了分组、不经规则；AIO 自动注入的 mixed-port 等其他入口一律拒绝。
- 机场节点过滤掉流量、到期、官网等提示条目（`exclude-filter` 与自用订阅一致）。
- 与自用订阅的区别：不带分流规则、DNS、TUN、`🔰 节点选择`、链式代理等自用分组，也没有 url-test 自动选择。

## 生成配置

节点与机场订阅地址从一份 mihomo 配置（在线订阅的下载副本）按名字提取，仓库里不存凭据：

```bash
python3 -I deploy/aio/render_upstream_config.py \
  --source /path/to/subscription.yaml \
  --output /path/to/server-upstream.yaml
```

- 节点名默认 `[自建]xyy`、`[自建]bwg`，机场 provider 默认 `laomao`，可用 `--xyy-node`、`--bwg-node`、`--airport-provider` 改。
- 机场订阅经内部分组 `subscription-fetch` 抓取（fallback：xyy → bwg → DIRECT，每 60 秒按订阅域名根路径探测，超时 5 秒，不限状态码，能完成 HTTPS 请求即判可用）。自建节点优先，都不通时才直连，不需要按环境改参数。
  - 为什么不是直连优先：mihomo 的 fallback 在全部成员探测失败时用第一个成员，DIRECT 拨号失败也不会触发组重测。直连被阻断（TLS 握手超时）时，只要自建节点在同一轮探测里也失败一次（例如 anytls 启动后首个连接超时），组就停在 DIRECT 到下一轮探测，机场拉取 503、0 节点（2026-10-30 uat 实测）。DIRECT 放最后，最坏情况落在 xyy，不会卡在已知不通的直连上。
  - 没用 url-test：它在直连可用时仍会因延迟更低选 DIRECT，直连被阻断时行为与 fallback 一致，不解决问题。也没改探测路径加 `expected-status`：问题出在握手失败，与状态码无关，且订阅域名上没有约定的固定状态路径。
  - 启动时首次拉取早于该组的健康检查，可能先失败一次，mihomo 按退避（10 秒起翻倍）重试。

## 生产部署：离线配置 + 机场节点离线缓存

生产（及 uat）按离线方式部署，启动不依赖任何在线拉取：

1. 服务端配置用生成好的文件导入为本地（local）profile，不用 remote profile 指向在线 URL。
2. 同时放一份机场节点离线缓存：把能拉到订阅的环境里的 `proxy_providers/upstream-laomao.yaml`（mihomo 工作目录下，路径由配置里的 `path` 决定）随配置一起放到目标 AIO 的同一路径，权限 0600，含节点凭据，不要提交。
3. mihomo 启动时先加载本地缓存，节点立即可用；缓存比刷新间隔（`interval: 28800`，8 小时）旧时会在启动时强制在线刷新一次。

运行时在线刷新经 `subscription-fetch` 进行，刷新失败只记 `[Provider] laomao pull error`，继续沿用已加载的缓存节点，不清空机场组；下次成功刷新后才覆盖缓存文件。缓存过期太久会让节点失效，刷新持续失败时按日志排查订阅域名可达性，必要时重新生成并下发缓存。
- listener 不开认证：默认容器化部署，AIO 与各服务只经内部网络通信。不要把 17911–17913 发布到宿主或公网，也不要把服务端配置导入桌面客户端。
- 输出文件 0600，含节点凭据，放在运维中心，不要提交。如果发布成在线 URL，链接必须私有，泄露后果与桌面订阅相同。
- 测试：在 `deploy/aio` 目录执行 `python3 -m unittest test_render_upstream_config`。

导入 AIO：在 AIO 控制台新建一个本地（local）profile，内容为生成的配置（或 remote profile 指向发布后的服务端 URL），激活。服务端 AIO 实例里不要再启用 merge / script 覆盖，否则会改掉分组与规则。激活后核心会重启，先等 provider 健康检查完成，再在 sub2api 后台测试落地。

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

2026-10-30 用 mihomo v1.19.27（`ghcr.io/metacubex/metacubexd-server:v1.273.1` 内 `/usr/local/bin/mihomo`）在隔离 docker 网络复现 uat 问题并验证修正（测试订阅域名解析到只接 TCP、不回 TLS 的地址模拟直连握手超时；xyy 用本地 socks5 代替；未动 uat/prod）：

| 场景 | 旧配置（DIRECT 在首位） | 新配置（DIRECT 在末位） |
|---|---|---|
| 直连阻断，xyy 正常 | 切到 node-xyy，50 节点 | node-xyy，50 节点 |
| 直连阻断，xyy 启动后 15 秒才可用 | 停在 DIRECT，90 秒内 0 节点，持续 TLS handshake timeout | 下一轮探测切到 node-xyy，50 节点 |
| 直连正常，自建节点全不通 | — | DIRECT，50 节点 |
| 直连阻断、自建全不通，带离线缓存启动 | — | 刷新失败，沿用缓存 50 节点 |
