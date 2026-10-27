# 代理与出口 IP 业务模型（fork，设计稿）

状态：待 Jay 审阅。审定后按「实施分期」落地，代码以本文为准。

## 1. 背景与原则

上游平台风控只看得到**落地 IP**，看不到前面经过了哪些跳。因此：

- 「IP」专指落地出口；前置链路是另一类资源，可随时替换、自动切换。
- 业务 IP 池只放**静态落地 IP**（通常为高价值、低风控值的 ISP/住宅 IP）。动态代理、机场节点、自部署加密代理只做传输，不进业务池。
- 落地 IP 即静态 IP：一条落地代理记录的 host 就是上游看到的 IP。计数、去重、退役按落地 IP（host）统计，同一落地 IP 不允许重复登记。
- IP 可跨平台共用：一个 IP 对某平台退役后，仍可继续服务其他平台（如 kiro 用满后转给 ollama-cloud 长期用）。
- 退役是永久的，没有冷却后回池。
- 故障默认 fail-closed：宁可暂停账号，也不让长期账号悄悄换 IP。

## 2. 分层

```
账号 ──绑定──▶ 落地代理 egress（host = 静态落地 IP，业务池库存）
                 │ 前置链路（upstream_mode）
              传输层：mihomo 首跳组（机场订阅 + 自部署节点，fallback/url-test）
                     / 单个 transport 代理 / 直连
```

| 概念 | 说明 | 与账号的关系 |
|---|---|---|
| 传输代理 transport | 机场节点、自部署 ss/vless/trojan、动态代理、桥接 socks5 | 从不绑定账号 |
| 首跳组 hop group | 已有（迁移 249），transport 的自动切换组 | 从不绑定账号 |
| 落地代理 egress | 静态落地 IP，可配前置链路；绑定与计数的主体 | 绑定 |
| 业务池 pool | 出口 IP 的库存分组，不含平台规则 | — |
| 平台策略 policy | 某平台如何使用 IP（消耗型/长期型） | 决定分配、退役、故障处理 |

## 3. 数据模型

### 3.1 proxies（扩展）

- `role`：`transport` | `egress`，替代上一轮未提交的 `purpose`。
  - transport：可作为 `upstream_proxy_id`、首跳组成员、默认前置代理；不进池、不可绑账号。
  - egress：静态落地 IP，才可进池、绑账号。动态代理一律登记为 transport。
- `pool_id`：保留，仅 egress 有效。
- 去重：新建/编辑 egress 时，同 host 已有未删除的 egress 则拒绝（不同端口也算同一 IP）。存量重复不在迁移里强行合并，只在界面标出，计数按 host 合并统计，避免绕过上限。
- 国家、ASN 等属性沿用现有探测/质量检测结果，用于换 IP 时同地区优先。

### 3.2 platform_ip_policies（新表，替代 `proxy_pools.platform_limits`）

| 字段 | 说明 |
|---|---|
| `platform` | 唯一 |
| `mode` | `churn`（消耗型，如 kiro）/ `sticky`（长期型，如 opencode、ollama-cloud） |
| `pool_ids` | 该平台可取 IP 的池，空 = 全部池 |
| `max_lifetime_accounts` | churn：每个 IP 对该平台的历史累计账号上限，达到即对该平台永久退役 |
| `max_active_accounts` | 每个 IP 对该平台的同时在用上限（sticky 常为 1） |
| `on_egress_failure` | `pause` / `reassign`；sticky 固定 `pause`，churn 默认 `reassign` |

未配置策略的平台：暂停使用代理池，不能从池中分配（创建账号选池、分配到池都拒绝，并提示先配策略）。
升级迁移会为现有每个平台生成策略（见第 7 节），保证升级后原有用法不受影响。
被策略 `pool_ids` 引用的代理池不能删除：直接移除可能让列表变空、变成“全部池”而放宽限制。

### 3.3 proxy_account_bindings（扩展）

- 历史计数按 `(落地 host, platform)` 统计。
- 新增 `unbind_reason` 取值：`egress_replaced`（人工一键换 IP）、`egress_reassigned`（churn 自动换）。
- 锁定语义不变：从池分配的绑定都锁定，sticky 绑定只能通过「换 IP」操作改变。

## 4. 分配规则

候选 = 策略允许的池内、`role=egress`、代理 active 未过期、对该平台未退役、该平台在用数 < `max_active_accounts`。

- churn：优先该平台历史计数最少的 IP（尽量把每个 IP 的额度均匀用满）。
- sticky：优先**已对其他 churn 平台退役**的 IP，其次总在用账号数少的 IP。这样 kiro 用满的 IP 优先被长期平台吃掉，新 IP 留给 kiro。
- 换 IP（人工）时额外默认过滤同国家，可放宽到不限。

## 5. 健康与故障

区分是哪一层坏了，避免一个机场挂掉导致全部长期账号被暂停：

| 情况 | 判定 | 处理 |
|---|---|---|
| 传输层故障 | 首跳组/前置全部不可用 | 首跳组级告警；账号状态不动（请求本身 fail-closed） |
| 出口代理不可用 | 传输层健康，但经该 egress 探测失败；或代理过期/停用 | sticky：暂停账号 + 告警；churn：按策略自动换 IP |

实现要点（迁移 252，`internal/service/proxy_egress_health.go`）：

- 巡检有账号在用的 egress 代理（Redis 选主，单实例执行），连续失败达到阈值才处置，避免抖动误伤；故障层级变化时重新计数、重新处置。
- 巡检配置存于 settings `proxy_egress_health_config`：开关、间隔（30–3600 秒，默认 120）、失败阈值（1–20，默认 3）。在代理池页故障列表上方修改，下一轮生效；配置损坏时回退默认值。
- 判层：链路无法解析或首跳组没有健康节点 → transport；否则经该代理探测失败 → egress。
- 暂停用账号的 `schedulable=false`，处置记录在 `proxy_egress_incident_accounts`。原本就不可调度的账号只记 `alert_only`，恢复时不会被误打开。
- 告警写入运维告警（`ops_alert_events`，egress P1、transport P2），走现有告警页面与邮件；运维监控关闭时只写日志。
- 探测恢复后故障与告警自动关闭，但暂停的账号不自动恢复。代理被删除时故障自动关闭。

### 一键操作

- 账号「换 IP」：候选为策略允许的池内、对该平台可用、无未恢复故障的 egress，与当前 IP 同国家（探测缓存）优先。换绑后锁定到新 IP 所在池；被故障暂停的账号同时恢复调度。
- 账号「恢复」：只解除故障暂停、恢复调度，不换 IP。
- 代理「迁移全部账号」：逐个换绑，失败的保留原状并返回原因。
- 代理「立即检测」：手动跑一次巡检，用于确认恢复。
- 入口：代理池页面的「落地 IP 故障」列表；账号编辑页锁定绑定处的「换 IP」。

## 待办

- 策略维度目前是 `platform`。ollama-cloud 与 codex 都挂在 `openai` 等平台下会共用一条策略；需要分开配时，把策略细化到「平台 + 上游（base_url）」。

## 6. 参考

- mihomo/Clash：节点、proxy-groups、proxy-providers、`dialer-proxy` 分离，对应本文的 transport 与首跳组。
- 代理服务商（Bright Data、Oxylabs 等）：数据中心 / ISP（静态住宅）/ 动态住宅 / 移动的分类，sticky 与 rotating 会话。
- 指纹浏览器（AdsPower、Multilogin 等）：一个长期账号一个固定 IP，IP 变化即告警，对应 sticky 策略。

## 7. 存量迁移

原则：升级后不改变任何账号当前的代理与调度状态，原来能用的继续能用。

- 代理 `role`：被用作前置/首跳组成员/默认前置且无账号直接使用 → transport；其余（包括有账号在用的）→ egress。有账号在用的代理一律不改成 transport，也不移出池。
- `proxy_pools.platform_limits` 中每个平台 → `platform_ip_policies`（`mode=churn`，`max_lifetime_accounts` 取各池中的最小值，`pool_ids` 为空即全部池，不放宽任何原有限制）。`platform_limits` 列保留供回滚，应用不再读写。
- 现有未删除账号或绑定记录涉及、但没有 `platform_limits` 的平台 → 生成策略：kiro 为 churn 不限上限，其余为 sticky（`max_active_accounts` 不限，保持原有共用关系）。之后由人工收紧。
- 已有绑定、锁定状态原样保留；存量重复登记的同一落地 IP 只标出，不自动合并。
- 迁移带回滚快照：testenv 先演练，生产升级前用同一 SQL 的只读预览确认影响范围。

## 8. 实施分期

1. 代理角色：`role`、落地 IP 去重、按 host 合并计数；池只收 egress，账号不能直接用 transport。上一轮的 `purpose` 改造并入此期。
2. 平台策略：`platform_ip_policies` 替代 `platform_limits`，实现 churn/sticky 分配与跨平台复用。
3. 健康与运维：分层故障判定、暂停与告警、一键换 IP / 恢复 / 批量迁移（迁移 252）。
