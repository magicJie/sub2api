# Kiro fork 说明

本分支在上游 Wei-Shaw/sub2api 之上集成 Kiro（AWS CodeWhisperer / Amazon Q Developer）平台。
目标是让上游同步尽量是“直接 merge、零或极少冲突”。

## 约定

- Kiro 逻辑只放在 Kiro 自有文件里：
  - `backend/internal/kiro/`（协议转换、OAuth、websearch、tokenizer、冷却、continuation）
  - `backend/internal/service/kiro_*.go`
  - `backend/internal/handler/admin/kiro_oauth_handler.go`、`handler/dto/kiro.go`、`server/routes/kiro.go`
  - `backend/internal/domain/platforms_kiro.go`（用 `init` 追加到上游平台清单）
  - `frontend/src/kiro/`（管理端 API、OAuth/导入 composable、模型目录与映射预设、配色、图标）
- 上游文件只允许两类改动：新增字段 / 嵌入结构体，或一行转发调用；都带 `Kiro（fork）` 注释，便于 `grep`。
- 不改 ent schema、不加数据库迁移。Kiro 的 prompt cache 计费模拟配置放在账号 `extra`：
  `kiro_cache_emulation_enabled`（bool，默认开启，显式 `false` 才关闭）、`kiro_cache_emulation_ratio`（0~1，默认 1），在账号编辑页设置。
  Kiro 上游不返回缓存用量，关闭后下游会把整段上下文按普通 input 计费。
- 下游系统提示原样转发，只在前面加协议必需的 thinking 前缀和身份覆盖（`<CRITICAL_OVERRIDE>`，防止上游自称 Kiro）。
  行为引导类追加默认关闭，用环境变量打开（`1/true/on/yes`）：
  - `SUB2API_KIRO_CHUNKED_WRITE_POLICY`：给 Write/Edit 类工具描述追加分块限制（超过 50 行分多次写），并在系统提示里要求静默照做。
  - `SUB2API_KIRO_AUTONOMOUS_COMPLETION_POLICY`：要求有工具时自主做完、不停下来等确认（会覆盖下游“先确认”类要求）。
  - `SUB2API_KIRO_TIME_CONTEXT`（`date`/`precise`）：追加当前日期或时间。
- 协议层（借鉴 magpie-community/plugins packages/kiro，MIT）：
  - `internal/kiro/cache_points.go`：把客户端 `cache_control` 原样转成 Kiro 的 `cachePoint`（消息、工具，最多 4 个保留最后），`SUB2API_KIRO_CACHE_POINTS=off` 关闭。
  - `internal/kiro/stream_error.go`：识别流中 `:message-type=exception` 帧；未输出前按等价 HTTP 状态走错误分类与 failover，已输出则发 SSE `error`，不再伪装成正常结束。
  - `service/kiro_error_classifier.go`：`CONTENT_LENGTH_EXCEEDS_THRESHOLD` → Anthropic `invalid_request_error` “prompt is too long”（Claude Code 据此自动压缩）；`INSUFFICIENT_MODEL_CAPACITY` → 529 换号且不封禁账号。
  - 工具名：不符合 `[a-zA-Z0-9_-]{1,63}` 的（如带 `.`、`:` 的 MCP 工具名，或过长）替换非法字符并追加整名 hash，响应时回映射为原名。
  - `tool_result` 里的图片挪到所在 user 消息的 `images`（Kiro 的 toolResults 只收文本）。
  - 上游 `CONTENT_FILTERED` 停止原因 → Anthropic `stop_reason: refusal`。
  - 推理：透传上游 `reasoningContentEvent` 的 `signature`（推理文本之后单独一帧）与 `redactedContent`（→ `redacted_thinking`）。
    历史里带上游签名的 thinking 以 `assistantResponseMessage.reasoningContent.reasoningText{text,signature}` 回传，
    签名缺失或是本网关旧版的本地 sha256 摘要时丢弃（上游不认）；不再内联成 `<thinking>` 正文。
  - 只含工具结果的 user 消息 content 留空，结果只放在 `toolResults`（不写英文占位，避免长会话回复语言漂移）。
  - 显式 `temperature: 0` / `top_p: 0` 原样发给上游。
  - 工具调用：只把 JSON 完整且满足请求里该工具 `input_schema.required` 的调用交给客户端（不再用写死的工具名必填表）；
    同一轮里 ID 不同的调用即使名称和参数相同也都保留，只按 ID 去重。
  - 客户端显式 `thinking.type: disabled` 优先于模型名 `-thinking` 后缀和 `interleaved-thinking` beta 头。
  - 非流式响应在上游未回报输入用量时，`usage.input_tokens` 用网关估算值填充（与流式一致）。
  - 推理区域：`api_region` 优先，其次 profileArn 中的区域（仅 `us-east-1` / `eu-central-1`），再按登录区域 `eu-*` → `eu-central-1`，否则 `us-east-1`。
    Builder ID 账号列不出 profile 时使用 Kiro 客户端的固定 profileArn。
- 多账号调度：挂在 `gateway_scheduling.go` Layer 2（优先级之后、负载率之前，入口 `filterKiroCandidates`）与 Layer 1.5 粘性检查。
  `SUB2API_KIRO_SCHEDULING_STRATEGY=drain|pace` 选择策略（默认 drain）；`SUB2API_KIRO_PACE_SCHEDULING=off` 两种都关，回到原有排序。
  - drain（集中耗尽，`service/kiro_drain_scheduling.go`）：Kiro 是 churn 平台，号用完就退役、腾出 IP。
    有可信快照（≤6 小时、未过重置时间）的号先于未知号（未知号排后但不排除）；重置时间越早越先（按分钟比较）；同一重置时间里已用比例越高越先；
    完全相同时交给负载率 / LRU。显示 100% 但未被上游拒绝的号照常调度。首选号并发满（抢槽失败或 LoadRate=100）或在 429 冷却时，
    调度循环移除它并重新收窄，溢出到下一个号，不排队等待；不设单号并发上限。粘性只在「已被上游拒绝且未 reset」时解除，不看百分比。
  - pace（均摊，借鉴 yetone/magpie，MIT，`service/kiro_pace_scheduling.go`）：按已用比例分档（<90 / 90–98 / ≥98），
    正常档按 `pace = (100−used%)/max(距重置小时,1)` 优先，10% 内视为同档；粘住的账号 ≥98% 且有替补、或已被上游拒绝时放开粘性。
  - 用量来自进程内快照（由下面的额度后台刷新器从落库快照恢复）。
- 上游额度拒绝与「可退役」标记：`service/kiro_quota_rejection.go`。只有上游真正拒绝才移出调度，不按显示的百分比提前排除。
  - 402 `MONTHLY_REQUEST_COUNT`：限流到下月 1 号 UTC（原有行为）。
  - 402 / 429 的 body 含 quota / credit / overage 类文字（`looksLikeKiroQuotaExhaustedError` / `looksLikeKiroOverageExhaustedError`），或无法识别的 402：
    `SetRateLimited` 到 reset（优先用快照的 `kiro_reset_at`，没有就下月 1 号 UTC），带 body 摘要记 warn，并跳过通用 `HandleUpstreamError`
    （否则 402 会被置为 error、429 被覆盖成秒级冷却）。限流性质的 429（Too many requests）仍走冷却逻辑，不标记。
  - 同时在 extra 写 `kiro_quota_rejected_at` / `kiro_quota_rejected_reason`（专用 repo 方法只改这两个键，不改 `updated_at`、不发调度 outbox；
    普通编辑不可写），经账号 DTO 的同名字段下发，前端 Kiro 额度块在「已被上游拒绝」旁显示「可退役」。账号状态与 IP 退役流程暂不接。
  - 后台额度刷新成功后，若限流已过、新快照晚于拒绝时间且用量下降或账期重置时间推进，自动清除标记。
- 额度后台刷新：`service/kiro_usage_refresher.go`、`kiro_usage_snapshot.go`、`repository/account_repo_kiro_usage.go`。
  只在 leader 实例拉 `getUsageLimits`（全局并发默认 2；默认 15 分钟，已用 ≥90% 或近 30 分钟有请求的号 5 分钟；按账号确定性抖动 ±10%；
  失败指数退避，上限 1 小时），结果只写 `extra.kiro_usage_snapshot`（不改 `updated_at`、不发调度 outbox）。
  所有实例每分钟从快照恢复调度数据。账号页与批量 usage 接口只读快照，没有快照时返回 `kiro_usage_pending` 并唤醒刷新器；
  单号 `force=true` 仍同步刷新。管理接口：`GET/PUT /api/v1/admin/kiro/usage-refresh/settings`（settings 表键 `kiro_usage_refresh_settings`，默认开）、
  `POST /api/v1/admin/kiro/usage-refresh/refresh-all`（只排队、立即返回）。凭据身份字段（不含 access_token）变化或删除账号时快照作废。
- 同步上游模型：`service/kiro_upstream_models.go`，账号编辑页“从上游同步模型”。OAuth 调管理面 `List-Available-Models`（带 profileArn），
  直连 API Key 调控制面 `KiroControlPlaneBearerService.ListAvailableModels`；已知 Claude 模型换成目录里的短横线 ID 及 `-thinking` 变体，
  其余 `claude-*` / `gpt-*` 点号 ID 原样保留，`MapModel` 不能直通的（`auto`、`deepseek-3.2` 等）丢弃。上游挂钩在 `upstream_models.go` 的 `fetchUpstreamModelList`。
- 路由记录：`service/kiro_route_log.go` 在内存保留最近 200 次 Kiro 转发尝试（请求 ID、入口、模型、账号、额度快照、结果 ok/failover/error），
  不含请求与响应内容，重启清空。管理接口 `GET /api/v1/admin/kiro/routes?limit=50`。

## 上游文件中的挂钩点

查看全部：`git grep -n 'Kiro（fork）' -- backend`

| 文件 | 挂钩 |
|---|---|
| `service/gateway_forward*.go` | Kiro 账号转发到 `forwardKiro*` |
| `service/gateway_scheduling.go` | 冷却中的 Kiro 账号不参与调度；候选为空时尝试恢复冷却池；Layer 2 `filterKiroCandidates`、Layer 1.5 `kiroStickyAccountSpent` |
| `service/gateway_usage_billing.go` | 定价失败时 Kiro 保守计费兜底 |
| `service/gateway_count_tokens.go` | Kiro 不支持 count_tokens |
| `service/account.go` | 模型映射、请求模型归一化、`KiroAccountRuntime` 嵌入 |
| `service/account_usage_service.go` | Kiro 用量查询分支、缓存字段、`KiroUsageInfo` 嵌入 |
| `service/account_test_service.go` | Kiro 连通性测试分支 |
| `service/admin_account.go` | 更新凭据时保留 Kiro 身份/设备字段（`kiroPreserveCredentialFields`）；`kiro_usage_snapshot`、`kiro_quota_rejected_*` 受管键剥离与身份变化失效 |
| `service/account_service.go` | `kiro_usage_snapshot`、`kiro_quota_rejected_*` 受管键保留、身份变化失效、删除时清进程内快照 |
| `service/gateway_service.go` | Kiro 依赖字段 |
| `service/ops_upstream_context.go` | `KiroOpsDiagnostics` 嵌入 |
| `service/wire.go` | `KiroProviderSet`；TokenRefreshService 注册 Kiro 刷新器 |
| `service/domain_constants.go`、`admin_group.go` | 常量别名、默认模型列表 |
| `handler/gateway_handler*.go` | Kiro 会话 hash、UMQ |
| `handler/admin/account_handler.go` | 可用模型、运行时状态 |
| `handler/dto/*.go`、`handler/{handler,wire}.go`、`server/routes/admin.go` | DTO 嵌入、handler 与路由注册 |
| `cmd/server/wire_gen.go` | wire 生成产物，冲突时重新生成，不要手工合并 |

测试侧只给上游测试的硬编码平台列表追加了 `kiro`，以及 `platformCatalog.builtin.json`（生成产物）。

### 前端挂钩点

上游前端文件里的 Kiro 分支同样按平台字面量 `'kiro'` 判断，`git grep -n "kiro" -- frontend/src ':!frontend/src/kiro'` 可列出全部：

| 文件 | 挂钩 |
|---|---|
| `components/account/CreateAccountModal.vue` | 平台按钮、OAuth（社交 / IdC）与导入 token、API Key 直连推理区域、默认模型映射 |
| `components/account/EditAccountModal.vue` | OAuth 账号的模型限制（白名单/映射）、推理区域、cache 计费模拟 |
| `components/account/OAuthAuthorizationFlow.vue` | 回调 URL 解析 code/state |
| `components/account/AccountUsageCell.vue`、`AccountStatusIndicator.vue`、`views/admin/AccountsView.vue` | 套餐 / Credit / Bonus / 超额展示与额度状态徽章 |
| `components/common/PlatformIcon.vue`、`utils/platformColors.ts`、`utils/keyGroupProviders.ts`、`components/keys/UseKeyModal.vue` | 图标、配色、Key 分组归类 |
| `composables/useModelWhitelist.ts` | 模型目录与映射预设 |
| `constants/platformCatalog.ts` | `supportsUpstreamModelSync` 放开 Kiro |
| `api/admin/index.ts`、`types/index.ts`、`i18n/locales/{zh,en}/admin/accounts.ts` | API 注册、字段类型、文案 |

## 同步上游

```bash
git fetch upstream
git merge upstream/main
# 冲突时：
#   cmd/server/wire_gen.go  -> cd backend/cmd/server && go run -mod=mod github.com/google/wire/cmd/wire
#                              （之后恢复 go.mod/go.sum 中 wire 工具带入的间接依赖）
#   platformCatalog.builtin.json -> UPDATE_PLATFORM_CATALOG=1 go test -tags unit ./internal/service/ -run TestFrontendBuiltinPlatformCatalogInSync
cd backend && go build ./... && go test -tags=unit ./...
```

建议开启 `git config rerere.enabled true`，同一冲突只需解决一次。

## 链式代理（fork）

代理支持前置代理（链式，最多 5 跳）与 ss / vmess / vless（含 Reality）/ trojan 加密协议，由进程内嵌的 Xray-core 承载：

- 逻辑：`backend/internal/pkg/proxychain/`（Xray 配置、本地 SOCKS5 入口、分享链接解析）、`service/proxy_chain.go`、`handler/admin/proxy_chain_*.go`、`handler/dto/proxy_chain.go`、`repository/proxy_chain_repo.go`、`server/routes/proxy_chain.go`。
- 与 Kiro 不同，这里**改了 ent schema 并新增迁移** `247_proxy_chain.sql`（`proxies.upstream_mode / upstream_proxy_id / options`）。上游若占用同号迁移，需要把本迁移改名为更大的编号（文件名排序执行，内容幂等）。
- `Proxy.URL()` 在需要链路时返回本地 `socks5h://127.0.0.1:<port>` 入口；链路不可用时返回无法拨通的地址（fail-closed，绝不直连）。
- 上游文件挂钩：`git grep -n '链式代理（fork）' -- backend`（`service/proxy.go`、`admin_proxy.go`、`admin_service.go`、`wire.go`，`repository/proxy_repo.go`，`handler/admin/{proxy_handler,proxy_data,account_data}.go`，`handler/{handler,wire}.go`，`dto/{types,mappers}.go`，`routes/admin.go`）。
- Xray 版本固定在 v1.260206.0：更新的版本会把 `refraction-networking/utls` 升到预发布版，影响上游 TLS 指纹功能。

- **mihomo 首跳组（方案 A）**：`github.com/metacubex/mihomo v1.19.33` 只负责链的第一跳，后续跳仍由 Xray 的 `sockopt.dialerProxy` 承载；AnyTLS 等机场协议从订阅加载。`config.Parse` 的 linkname 依赖要求 blank import `hub/executor`，不能删。
- 迁移 **`249_proxy_hop_groups.sql`**（幂等）新增 `proxy_hop_groups` 表及 `proxies.upstream_hop_group_id`，Ent schema / 生成代码同步；`upstream_mode=hop_group` 引用组。手动成员必须是现有直连代理；组不能再有前置，链仍最多 5 跳（首跳组计 1 跳）。
- 运行时：`pkg/mihomohop/` 进程内唯一配置，HTTP proxy-provider + fallback / url-test 组；每组通过复用 `proxychain/socks_bridge.go` 的随机认证回环 SOCKS5 入口成为 Xray 的第 0 跳。空组设 `empty-fallback=REJECT`，拨号前拒绝无健康节点，绝不走 DIRECT。配置变更关闭旧桥与旧连接，快照刷新继续通过 `Retain` 回收旧 Xray 链；组内健康切换/订阅节点更新不重建 Xray 链。
- 新逻辑在 `service/proxy_hop_group*.go`、`repository/proxy_hop_groups_repo.go`、`handler/admin/proxy_hop_groups_handler.go` 与 `frontend/src/proxyHopGroups/`；`service/proxy_chain.go` / fork 路由与 DTO 承接挂钩，`repository/wire.go` 注册仓储，`service/wire.go` 注入仓储，`wire_gen.go` 重新生成。管理入口在代理页“首跳组”，API 为 `/api/v1/admin/proxy-hop-groups`。默认前置设置新增 `proxy_chain_default_upstream_hop_group_id`，与原默认代理设置原子互斥。
- 组合根在 `cmd/server/proxy_hop_groups_wire.go` 将 `mihomohop.Default()` 注入 `pkg/hopgroup.Runtime`；service/DTO 只依赖无内核初始化副作用的契约。不要在 `service` 包重新 import mihomo：这会让所有业务调用方和单测提前装入 mihomo 的全局状态与后台任务。
- 订阅 URL 视为凭据：Ent / service 模型不序列化原文，管理 DTO 只返回 `[REDACTED]` 与数量；更新省略 URL 列表表示保留。`service/audit_log.go` 追加敏感字段挂钩；mihomo 日志只转为不含 payload 的 slog Debug 事件，私有临时缓存目录权限 0700。原生 provider HTTP 客户端不读取环境代理，因此按 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 为订阅抓取生成显式 HTTP(S)/SOCKS 出站，不改变业务链路出口。
- 代理导出只带首跳组名称，不导出组配置或订阅凭据；导入前需在目标实例手动建立同名首跳组，找不到时校验失败（不降级直连）。跨实例的默认首跳组也按同样规则恢复引用。

## 代理池（fork）

代理按代理池分组；创建账号时可指定代理池，从池中分配代理并锁定到账号；账号与代理的绑定全部留痕，用于按平台限制每个代理的账号数（代理退役）。

- 逻辑：`service/proxy_pool.go`、`repository/proxy_pool_repo.go`、`handler/admin/proxy_pool_handler.go`、`server/routes/proxy_pool.go`；前端 `frontend/src/proxyPool/`（管理弹窗由代理页“代理池”按钮打开）。
- 迁移 `248_proxy_pools.sql`：`proxy_pools`（含唯一的默认池 `default`）、`proxies.pool_id`（NULL 即默认池，删池时回到默认池）、`proxy_account_bindings`，以及 `accounts` 上的触发器 `trg_accounts_proxy_binding`。触发器在 `proxy_id` 变化或账号软删除时写绑定记录，所以任何改代理的路径（手动、过期回退、CRS 同步）都会被记录；spark 影子账号不计。上游占用同号迁移时同样改名为更大编号。
- 平台上限配置在代理池上（`platform_limits`，如 `{"kiro": 5}`），按**历史累计**计数：一个代理被该平台多少个不同账号绑定过（含已解绑、已删除）。达到上限即对该平台退役，不再分配。未配置的平台不限。
- 分配策略：池内 active、未过期、未退役的代理里，选该平台历史计数最少的，同数取 id 小的。挑选到落库在进程内串行（`ProxyPoolService.mu`），多实例同时建号仍可能超出上限 1 个。
- 锁定：从池中分配的绑定 `locked=true`，管理端单个/批量编辑改代理会被拒绝（`ACCOUNT_PROXY_LOCKED`），需先调 `POST /admin/accounts/:id/proxy-unbind` 解锁（代理不变，历史仍计数）。系统改投（过期回退）沿用原锁定。手动直接选代理的账号不锁定，行为与上游一致。
- OAuth 建号：前端选池后先调 `GET /admin/proxy-pools/:id/preview?platform=` 取将分配的代理，用它完成授权并作为 `proxy_id` 随 `proxy_pool_id` 提交；建号时若该代理已不可用则改分配池内其他代理。
- 上游文件挂钩：`git grep -n '代理池（fork）' -- backend frontend/src`。

## Ollama Cloud 独立平台（fork）

- 平台登记：`backend/internal/domain/platforms_ollama.go`，ID `ollama`，显示名 `Ollama Cloud`，gateway / LiteLLM provider 均为 `openai`。只支持 API Key，默认地址 `https://ollama.com/v1`；管理端创建、编辑、批量更新校验 HTTPS 与精确主机 `ollama.com`。
- 账号的 `IsOpenAI()` / `IsOpenAIApiKey()` 复用 OpenAI API Key 的归一化、模型映射、请求头覆写、计费、探测和错误处置；OAuth 生命周期仍限定原 OpenAI 平台。分组保留 Messages 调度映射、reasoning 策略与模型目录配置。
- `/v1/chat/completions`、`/v1/responses`、`/v1/messages` 默认都转换为上游 `/v1/chat/completions`，使用 Bearer。默认路由优先于旧 `openai_responses_supported=true` 探测标记；显式 `force_responses` 保留原账号覆盖语义。Codex 工具与历史归一化沿用共享管线，输出 token cap 与 raw CC reasoning 别名按 Ollama 平台生效。
- 用量身份统一为 `platform=ollama && type=apikey`；base URL 用于默认值与校验。仓储用平台查询，SQL URL 正则只保留在一次性迁移的旧数据识别中。用量抓取、429 短冷却与探测、到期提醒继续使用原有机制。CRS 刷新已有账号时保留已迁移的 Ollama 平台。
- `mcp_xml_inject` 原值随分组保留。当前 OpenAI 网关只持久化和投影该字段，实际 MCP XML 注入实现位于 Antigravity transformer；Ollama 维持迁移前的请求处理语义。
- 自动迁移 `backend/migrations/256_ollama_platform.sql`：按旧正则识别六个宿主平台的未删除 API Key 账号；同一存储 key 保留 active 优先、ID 最小的账号，其余设为 `disabled` 并写 `extra.migrated_to_account_id`。保留原关系，为保留账号补齐兄弟账号的分组关系；保留 proxy、key、extra，旧 base URL 规范成默认地址。
- 纯 Ollama 分组改平台；混合分组列出 ID 并报错。受影响平台现存用户额度、冲突兄弟配置、歧义渠道/路由等配置在写入前报错。用户额度的 Redis 脏计数按平台存储，现有停止流程只做有限刷新，因此额度行保留原值，要求管理员先结算并明确配置新预算；独立的默认额度授予配置与渠道定价可以原地迁移。错误规则列表追加 Ollama，代理绑定历史同步平台，Ollama IP 策略继承 OpenAI 池选择与两个账号数上限，采用 `sticky/pause`。
- runner 持有迁移 advisory lock，单文件 SQL 与 checksum 同一事务提交；异常回滚 256 并阻止启动，修复所列数据后重新启动。上线前保存数据库快照；迁移注释含维护窗口内基于快照的反向 SQL，接流量后的回滚使用完整快照。
- 验证：`go test -tags=unit ./internal/service -run 'Ollama|OllamaCloud'` 包含三入口真实本地 TLS 回环、Bearer、模型映射、Codex custom tool、流式/非流式与用量计费断言；`CI=1 go test -tags=integration ./internal/repository -run '^TestMigration256' -count=1 -v` 使用隔离 PostgreSQL 全量 schema 和真实触发器覆盖迁移、失败回滚及重复执行。

## 未移植 / 待办

- 前端：旧 fork 的分组级 cache emulation 设置（已改为账号级）、用量页 Kiro 会话指纹列未移植（依赖未移植的后端表结构）。
- 刻意不做：改写下游系统提示的“提示词过滤”（Kiro-Go / kiro-account-manager 的替换 Claude Code 提示、去环境噪音），
  以及按会话复用上一次回答的“响应缓存”。本 fork 定位是纯净的模型 API，下游提示与回答不应被网关改动。
- 旧 fork 中与 Kiro 无关的改动（流失速 watchdog、harmony token 中和、OpenAI refresh token 复用处理等）未带入。
- 旧 fork 的分组级 cache emulation 列、usage_logs 会话指纹列及其审计 UI 未移植（需要改上游表结构）。
- Kiro 专用 keepalive 配置 `gateway.kiro_stream_keepalive_interval` 未移植，使用全局 `stream_keepalive_interval`。
