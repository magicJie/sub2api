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
账号 ──绑定──▶ 落地代理 egress（proxies 只放静态落地 IP，业务池库存）
                 │ 前置：直连 / 前置组
              前置组 ──▶ 槽位 1：[端点 A(主), 端点 C(备)]
                      ──▶ 槽位 2：[端点 B]
                              │
              前置端点 = 外部 AIO 的一个 listener（自建节点或机场组）
```

### 2.1 传输层归属（2026-10-28 决定）

首跳（机场订阅、自建节点、抗封锁协议、节点切换）不属于 sub2api 的功能边界，交给外部 AIO（MetaCubeXD All-in-One，mihomo 内核）统一管理；sub2api 只管静态落地 IP。

- **AIO 负责**：在线订阅、`首跳` 组及健康检查策略。每个环境一个 AIO 实例（home、testenv、生产集群各自运行），使用同一份在线订阅，不跨网络共用实例。该实例额外开一个 listener，`proxy: 首跳`。
- **sub2api 负责**：把这个 listener 登记为一条 `role=transport` 代理，作为落地代理的前置（或默认前置）；拨号时现拼「AIO 首跳 → 落地」。落地 IP 的拉取、分配、计数、退役不进 AIO。
- **不采用整链托管**：Ant 的做法是把落地写进订阅、每个落地开一个 listener，适合 5 个手工维护、与 profile 一一对应的 IP。sub2api 的落地从供应商拉取、频繁增删，整链托管会让 IP 清单在 AIO 与数据库各存一份，计数还要维护端口→IP 映射；落地也不能写进多端共享的在线订阅。
- **首跳负载**：`首跳-机场` 用 `load-balance` + `strategy: consistent-hashing`（软绑定）替代 url-test。经首跳的目标就是落地 host，因此不同落地分散到不同节点，同一落地固定走同一节点，节点故障只改派映射到它的那部分。不用 round-robin：每条新连接换节点，延迟随机场节点质量波动，协议会话复用变差。2026-10-28 已在 mihomo v1.19.29 实测（报告 `/data/workspaces/sub2api-testenv/reports/20261028-firsthop-chash.md`）：`dialer-proxy` 场景哈希键是落地 IP，五个落地分散到五个节点且稳定。注意两点：
  - 局部改派只对「成员列表不变、节点健康失败」成立（实测只改派 1/5）；成员增删或顺序变化（订阅更新、改 `exclude-filter`）会让 jump hash 索引移动，实测删 1 个节点改派 3/5。这类改派只换首跳、不换落地 IP，代价是部分连接重建。
  - 组必须加 `empty-fallback: REJECT`：成员为空时默认回退 `COMPATIBLE`（等效 DIRECT），会绕过 fail-closed。组内也不放 DIRECT。
- **落地不做负载**：一个账号固定一个落地 IP；落地之间的分摊由第 4 节的 churn/sticky 分配完成。
- **延迟**：切换只发生在节点故障时，且只影响新连接，已有 keep-alive 连接不受影响。主要因素是首跳与落地的地理搭配；url-test 只测「节点→测速 URL」，测不到「节点→落地」，必要时按地区筛选首跳成员。
- **约束**：若静态 IP 供应商按来源 IP 白名单认证，首跳出口必须固定，负载策略失效；这类供应商改用账号密码认证或单独固定首跳。
- **内嵌首跳组**（迁移 249）保留为过渡与回退；各环境切到 AIO 并验收后，再决定是否移除（可去掉约 10MB 体积及 mihomo 全局状态、日志接管等绕行处理）。
- 2026-10-28 晚更新：内嵌首跳组由第 2.2 节的前置组取代（第 5 期），sub2api 不再带 mihomo 内核、不再配订阅。

### 2.2 前置组（2026-10-28 晚决定，第 5 期）

- **前置端点**：外部 AIO 的一个 listener，只存名称、`host:port`、协议（socks5 / http）、认证。listener 后面是一个自建节点，还是一个机场组（机场内部怎么选节点由 AIO 决定），sub2api 不感知。端点是独立资源，不在 proxies 表里，不进池、不绑账号。
- **槽位**：前置组里的一条有序端点列表，第一个为主，其余为备，备用可不配。同一端点可出现在多个槽位、多个前置组中。槽位数就是该组的前置并发数。
- **前置组**：槽位的集合，原「首跳组」改名而来。落地代理的前置只有两种：直连，或指向一个前置组。不再有「默认前置代理」，也不再有「落地串在另一条代理后面」。
- **黏滞**：落地按 rendezvous hash（落地 id × 槽位 id，取最大者）在组内**可用槽位**中选一个，不存状态。增删一个槽位只改派约 1/N 的落地。
- **健康**：每个实例定时经每个端点请求固定探测地址（默认 `generate_204`），端点共享时只探一次。连续失败达到阈值才判不可用；不可用后要连续成功才判恢复。
- **切换**：槽位当前端点不可用时，切到列表中下一个可用端点；靠前的端点（含主端点）连续健康满 failback 时长（默认 300 秒）后切回。只影响新连接，已有 keep-alive 连接不受影响。
- **槽位故障**：槽位内所有端点都不可用即槽位不可用（只有一个端点的槽位即「端点故障 = 槽位故障」）。原本落在该槽位的落地按 rendezvous hash 自动改到其余可用槽位，其他落地不动；代价只是正常槽位并发变高，落地 IP 不变，账号与代理池不受影响。槽位故障发 transport P2 告警。
- **槽位恢复**：槽位连续健康满 failback 时长后重新参与选择，改走的落地回到原槽位。
- **整组全挂**：组内没有可用槽位时 fail-closed（不直连），巡检判为 transport 故障，只告警，不动账号。
- **拨号**：沿用 Xray 链，「当前端点（socks5 / http）→ 落地」。切换端点时只换链的第一跳，落地 IP 不变。
- **多实例**：探测与切换状态在每个实例内存中各自维护，不跨实例同步；各实例网络一致时结果相同，短暂不一致只影响首跳选择，不影响落地 IP。
- **AIO 侧约定**（2026-10-29）：sub2api 用专用的 AIO 配置（`deploy/aio/`），节点与机场与自用订阅同源，但不带自用分流规则，不改共享在线订阅。开 3 个 listener，各指向一个分组：`xyy`、`bwg`（各只含一个自建节点）、`laomao`（机场，consistent-hashing，`empty-fallback: REJECT`，组内不放 DIRECT）；规则只有 `MATCH,REJECT`。AIO 侧不做跨来源 fallback，备用关系全部放在 sub2api 槽位里。listener 不开认证（容器化部署，只走内部网络，不发布端口）；机场订阅经内部 fallback 组抓取（DIRECT → xyy → bwg），直连不通自动改走自建节点。
- **上线槽位**：bwg 暂时故障，前置组先配 1 个槽位「xyy 主、机场备」；bwg 修复后加第 2 个槽位「bwg 主、机场备」。

| 概念 | 说明 | 与账号的关系 |
|---|---|---|
| 前置端点 upstream endpoint | AIO listener（自建节点 / 机场组），第 5 期起替代 transport 代理 | 从不绑定账号 |
| 前置组 upstream group | 槽位集合，原首跳组（迁移 249）改名 | 从不绑定账号 |
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

### 3.4 前置组（第 5 期，迁移 257）

- `proxy_upstream_endpoints`：`name`、`protocol`（socks5 / http）、`host`、`port`、`username`、`password`、`status`。被槽位引用时不能删除。
- `proxy_upstream_groups`：`name`、`status`、`probe_url`、`probe_interval_seconds`（默认 30）、`fail_threshold`（默认 3）、`recover_threshold`（默认 2）、`failback_seconds`（默认 300）。被落地引用时不能删除。
- `proxy_upstream_slots`：`group_id`、`name`、`position`、`endpoint_ids`（JSONB 有序数组，至少 1 个，同一槽位内不重复）。
- `proxies`：`upstream_mode` 只剩 `direct` / `group`，新增 `upstream_group_id`。`upstream_proxy_id`、`upstream_hop_group_id` 与 `role` 列保留供回滚，应用不再读写；`role=transport` 的记录迁成端点后软删除。
- `proxy_hop_groups` 表保留供回滚，应用不再读写；settings 中 `proxy_chain_default_upstream_proxy_id`、`proxy_chain_default_upstream_hop_group_id` 不再读取。

## 4. 分配规则

候选 = 策略允许的池内、`role=egress`、代理 active 未过期、对该平台未退役、该平台在用数 < `max_active_accounts`。

- churn：优先该平台历史计数最少的 IP（尽量把每个 IP 的额度均匀用满）。
- sticky：优先**已对其他 churn 平台退役**的 IP，其次总在用账号数少的 IP。这样 kiro 用满的 IP 优先被长期平台吃掉，新 IP 留给 kiro。
- 换 IP（人工）时额外默认过滤同国家，可放宽到不限。

## 5. 健康与故障

区分是哪一层坏了，避免一个机场挂掉导致全部长期账号被暂停：

| 情况 | 判定 | 处理 |
|---|---|---|
| 传输层故障 | 首跳组/前置全部不可用（含 AIO listener 不通） | 首跳组级告警；账号状态不动（请求本身 fail-closed） |
| 出口代理不可用 | 传输层健康，但经该 egress 探测失败；或代理过期/停用 | sticky：暂停账号 + 告警；churn：按策略自动换 IP |

实现要点（迁移 252，`internal/service/proxy_egress_health.go`）：

- 巡检有账号在用的 egress 代理（Redis 选主，单实例执行），连续失败达到阈值才处置，避免抖动误伤；故障层级变化时重新计数、重新处置。
- 巡检配置存于 settings `proxy_egress_health_config`：开关、间隔（30–3600 秒，默认 120）、失败阈值（1–20，默认 3）。在代理池页故障列表上方修改，下一轮生效；配置损坏时回退默认值。
- 判层：链路无法解析或首跳组没有健康节点 → transport；否则经该代理探测失败 → egress。前置为 AIO listener 时，经该 transport 访问固定探测地址失败即判 transport；哪个首跳节点坏了由 AIO 自己处理，sub2api 不感知。第 5 期起：前置组没有可用槽位 → transport；单个槽位故障由组内改派吸收，不产生落地故障；端点可用但经落地失败、直连复测可达 → transport；否则 egress。
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

- 第 5 期稳定后，另起迁移删除 `proxy_hop_groups` 表及 proxies 上的回滚列。

## 6. 参考

- mihomo/Clash：节点、proxy-groups、proxy-providers、`dialer-proxy` 分离，对应本文的 transport 与首跳组；`load-balance` 的 `consistent-hashing` 策略见第 2.1 节。
- home Ant 集成：`/data/workspaces/ops/ant/changes/20261027-dedicated-aio/`（ant-aio 实例、回环 listener、整链托管）。
- 代理服务商（Bright Data、Oxylabs 等）：数据中心 / ISP（静态住宅）/ 动态住宅 / 移动的分类，sticky 与 rotating 会话。
- 指纹浏览器（AdsPower、Multilogin 等）：一个长期账号一个固定 IP，IP 变化即告警，对应 sticky 策略。

## 7. 存量迁移

原则：升级后不改变任何账号当前的代理与调度状态，原来能用的继续能用。

- 代理 `role`：被用作前置/首跳组成员/默认前置且无账号直接使用 → transport；其余（包括有账号在用的）→ egress。有账号在用的代理一律不改成 transport，也不移出池。
- `proxy_pools.platform_limits` 中每个平台 → `platform_ip_policies`（`mode=churn`，`max_lifetime_accounts` 取各池中的最小值，`pool_ids` 为空即全部池，不放宽任何原有限制）。`platform_limits` 列保留供回滚，应用不再读写。
- 现有未删除账号或绑定记录涉及、但没有 `platform_limits` 的平台 → 生成策略：kiro 为 churn 不限上限，其余为 sticky（`max_active_accounts` 不限，保持原有共用关系）。之后由人工收紧。
- 已有绑定、锁定状态原样保留；存量重复登记的同一落地 IP 只标出，不自动合并。
- 第 5 期（迁移 257）前置组转换，同样不改变任何落地当前实际走的路径：
  - `role=transport` 的代理 → 端点（同名、同地址与认证）。协议不是 socks5 / http、或自身还有前置的 transport，迁移拒绝执行并列出。
  - 首跳组 → 同 id 的前置组：`fallback` / `url-test` 组生成一个槽位，端点顺序同成员顺序；`load-balance` 组每个成员生成一个单端点槽位。带订阅 URL 且仍被引用的组，迁移拒绝执行并列出，需先在后台把落地改到 AIO 端点。
  - `upstream_mode=proxy` 指向 transport 的落地 → 每个被引用的 transport 生成一个前置组 `<代理名>`（一个槽位、一个端点）；指向 egress 的落地（落地串落地）迁移拒绝执行并列出。
  - `upstream_mode=default`：默认首跳组 → 该组；否则默认前置代理 → 上一条生成的组；都没有配置的落地原本就 fail-closed，改为 direct 前先拒绝并列出。
  - `upstream_mode=hop_group` → `group`，组 id 不变。
- 迁移带回滚快照：testenv 先演练，生产升级前用同一 SQL 的只读预览确认影响范围。

## 8. 实施分期

1. 代理角色：`role`、落地 IP 去重、按 host 合并计数；池只收 egress，账号不能直接用 transport。上一轮的 `purpose` 改造并入此期。
2. 平台策略：`platform_ip_policies` 替代 `platform_limits`，实现 churn/sticky 分配与跨平台复用。
3. 健康与运维：分层故障判定、暂停与告警、一键换 IP / 恢复 / 批量迁移（迁移 252）。
4. 传输层外置到 AIO（第 2.1 节），以运维变更为主，sub2api 代码基本不动：
   1. 在独立 mihomo 实例验证 `首跳-机场` 改 consistent-hashing：五个落地分散到不同节点、单节点故障只改派部分落地、全挂 fail-closed。通过后再改共享在线订阅（影响 home、laptop、手机）。
   2. testenv：起一个使用同一订阅的 AIO 容器，加 `proxy: 首跳` listener；在后台登记为 transport，替换 bwg-chain-1..5 的首跳组前置（改前快照）；复跑链路测试与 Kiro token 刷新、`/v1/messages`。
   3. 生产：集群内 `mihomo-hop` 桥接换成同订阅 AIO 容器，整理为运维方案由 Jay 执行（不停机：先并行起新端点并登记 transport，逐个切换前置，观察后下线旧桥接）。方案在运维中心 `ops-bsj/sub2api-release/changes/20261028-aio-firsthop/PLAN.md`（含生产内网信息，不进本仓库）。
   4. 全部环境稳定后，评估移除内嵌首跳组（由第 5 期取代）。
5. 前置组（第 2.2 节、第 3.4 节，迁移 257）：
   1. 后端：端点 / 前置组 / 槽位的表、仓储、CRUD 与引用保护；端点探测与槽位切换运行时；链路解析改为「当前端点 → 落地」；巡检判层接槽位状态；去掉默认前置、`proxy` 模式与 transport 角色。
   2. 删除 `internal/pkg/mihomohop`、`internal/pkg/hopgroup`、只为 mihomo 服务的 SOCKS 桥接，`go.mod` 去掉 mihomo。
   3. 前端：首跳组改名前置组，新增端点与槽位编辑、槽位当前端点与端点健康展示；代理页去掉 transport 与默认前置。
   4. 迁移预检 SQL（只读，与迁移同逻辑）；testenv 改前快照后演练，复跑链路测试、Kiro token 刷新、`/v1/messages`，端点故障切换、failback、槽位全挂 fail-closed。
   5. 生产：Jay 用 `deploy/aio/render_upstream_config.py` 生成专用配置并在 AIO 激活（listener xyy / bwg / laomao），用预检 SQL 确认影响后再 expand；登记端点后按上线槽位配置前置组（第 2.2 节）。
