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
- 多账号调度（借鉴 yetone/magpie，MIT）：`service/kiro_pace_scheduling.go`，挂在 `gateway_scheduling.go` Layer 2（优先级之后、负载率之前）与 Layer 1.5 粘性检查。
  按已用比例分档（<90 / 90–98 / ≥98），正常档按 `pace = (100−used%)/max(距重置小时,1)` 优先，10% 内视为同档；粘住的账号 ≥98% 且有替补时放开粘性。
  用量来自进程内快照，超过 5 分钟后台刷新。`SUB2API_KIRO_PACE_SCHEDULING=off` 关闭。
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
| `service/gateway_scheduling.go` | 冷却中的 Kiro 账号不参与调度；候选为空时尝试恢复冷却池 |
| `service/gateway_usage_billing.go` | 定价失败时 Kiro 保守计费兜底 |
| `service/gateway_count_tokens.go` | Kiro 不支持 count_tokens |
| `service/account.go` | 模型映射、请求模型归一化、`KiroAccountRuntime` 嵌入 |
| `service/account_usage_service.go` | Kiro 用量查询分支、缓存字段、`KiroUsageInfo` 嵌入 |
| `service/account_test_service.go` | Kiro 连通性测试分支 |
| `service/admin_account.go` | 更新凭据时保留 Kiro 身份/设备字段（`kiroPreserveCredentialFields`） |
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

## 代理池（fork）

代理按代理池分组；创建账号时可指定代理池，从池中分配代理并锁定到账号；账号与代理的绑定全部留痕，用于按平台限制每个代理的账号数（代理退役）。

- 逻辑：`service/proxy_pool.go`、`repository/proxy_pool_repo.go`、`handler/admin/proxy_pool_handler.go`、`server/routes/proxy_pool.go`；前端 `frontend/src/proxyPool/`（管理弹窗由代理页“代理池”按钮打开）。
- 迁移 `248_proxy_pools.sql`：`proxy_pools`（含唯一的默认池 `default`）、`proxies.pool_id`（NULL 即默认池，删池时回到默认池）、`proxy_account_bindings`，以及 `accounts` 上的触发器 `trg_accounts_proxy_binding`。触发器在 `proxy_id` 变化或账号软删除时写绑定记录，所以任何改代理的路径（手动、过期回退、CRS 同步）都会被记录；spark 影子账号不计。上游占用同号迁移时同样改名为更大编号。
- 平台上限配置在代理池上（`platform_limits`，如 `{"kiro": 5}`），按**历史累计**计数：一个代理被该平台多少个不同账号绑定过（含已解绑、已删除）。达到上限即对该平台退役，不再分配。未配置的平台不限。
- 分配策略：池内 active、未过期、未退役的代理里，选该平台历史计数最少的，同数取 id 小的。挑选到落库在进程内串行（`ProxyPoolService.mu`），多实例同时建号仍可能超出上限 1 个。
- 锁定：从池中分配的绑定 `locked=true`，管理端单个/批量编辑改代理会被拒绝（`ACCOUNT_PROXY_LOCKED`），需先调 `POST /admin/accounts/:id/proxy-unbind` 解锁（代理不变，历史仍计数）。系统改投（过期回退）沿用原锁定。手动直接选代理的账号不锁定，行为与上游一致。
- OAuth 建号：前端选池后先调 `GET /admin/proxy-pools/:id/preview?platform=` 取将分配的代理，用它完成授权并作为 `proxy_id` 随 `proxy_pool_id` 提交；建号时若该代理已不可用则改分配池内其他代理。
- 上游文件挂钩：`git grep -n '代理池（fork）' -- backend frontend/src`。

## 未移植 / 待办

- 前端：旧 fork 的分组级 cache emulation 设置（已改为账号级）、用量页 Kiro 会话指纹列未移植（依赖未移植的后端表结构）。
- 刻意不做：改写下游系统提示的“提示词过滤”（Kiro-Go / kiro-account-manager 的替换 Claude Code 提示、去环境噪音），
  以及按会话复用上一次回答的“响应缓存”。本 fork 定位是纯净的模型 API，下游提示与回答不应被网关改动。
- 旧 fork 中与 Kiro 无关的改动（流失速 watchdog、harmony token 中和、OpenAI refresh token 复用处理等）未带入。
- 旧 fork 的分组级 cache emulation 列、usage_logs 会话指纹列及其审计 UI 未移植（需要改上游表结构）。
- Kiro 专用 keepalive 配置 `gateway.kiro_stream_keepalive_interval` 未移植，使用全局 `stream_keepalive_interval`。
