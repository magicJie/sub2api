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
- Kiro 专属状态通过最小挂钩接入上游文件，并带 `Kiro（fork）` 注释；共享协议缺陷在共享管线修复，覆盖原生 provider 回归。
- 不改 ent schema、不加数据库迁移。Kiro 的 prompt cache 计费模拟配置放在账号 `extra`：
  `kiro_cache_emulation_enabled`（bool，默认开启，显式 `false` 才关闭）、`kiro_cache_emulation_ratio`（0~1，默认 1），在账号编辑页设置。
  上游提供缓存计量时保留原生分项；缺少原生缓存计量时按配置模拟，关闭模拟后按普通 input 计费。
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
  - 图片按各轮 user 消息保留，不按历史距离或图片数量主动裁剪；`tool_result` 的图片归入其所在 user 消息的 `images`（Kiro 的 toolResults 只收文本）。
  - 上游 `CONTENT_FILTERED` 停止原因 → Anthropic `stop_reason: refusal`。
  - 推理：透传上游 `reasoningContentEvent` 的 `signature`（推理文本之后单独一帧）与 `redactedContent`（→ `redacted_thinking`）。
    历史里带上游签名的 thinking 以 `assistantResponseMessage.reasoningContent.reasoningText{text,signature}` 回传，
    签名缺失或是本网关旧版的本地 sha256 摘要时丢弃（上游不认）；不再内联成 `<thinking>` 正文。
  - 只含工具结果的 user 消息 content 留空，结果只放在 `toolResults`（不写英文占位，避免长会话回复语言漂移）。
  - 显式 `temperature: 0` / `top_p: 0` 原样发给上游。
  - 工具调用：JSON 完整且满足该工具 `input_schema.required` 才交给客户端，截断或缺必需字段返回错误；
    不同 ID 的调用即使名称和参数相同也保留，只按 ID 去重。XML invoke、`[Called]` 等正文保持文本。
  - 显式 `thinking.type` / `budget_tokens` 优先于模型名后缀；beta 头只声明客户端能力。
  - 输入与缓存用量分别使用上游原生字段，包括显式零输入；输入用量缺失时才回填估算。
  - 推理区域：`api_region` 优先，其次 profileArn 中的区域（仅 `us-east-1` / `eu-central-1`），再按登录区域 `eu-*` → `eu-central-1`，否则 `us-east-1`。
    Builder ID 账号列不出 profile 时使用 Kiro 客户端的固定 profileArn。
- 多账号调度：挂在 `gateway_scheduling.go` Layer 2（优先级之后、负载率之前，入口 `filterKiroCandidates`）与 Layer 1.5 粘性检查。
  `SUB2API_KIRO_SCHEDULING_STRATEGY=drain|pace` 选择策略（默认 drain）；`SUB2API_KIRO_PACE_SCHEDULING=off` 两种都关，回到原有排序。
  - drain（集中耗尽，`service/kiro_drain_scheduling.go`）：Kiro 是 churn 平台，号用完就退役、腾出 IP。
    有可信快照（≤6 小时、未过重置时间）的号先于未知号（未知号排后但不排除）；重置时间越早越先（按分钟比较）；同一重置时间里已用比例越高越先；
    完全相同时交给负载率 / LRU。显示 100% 但未被上游拒绝的号照常调度。首选号并发满（抢槽失败或 LoadRate=100）或在 429 冷却时，
    调度循环移除它并重新收窄，溢出到下一个号，不排队等待；不设单号并发上限。粘性在账号限流有效期内或已被上游额度拒绝且未 reset 时解除，不看百分比。
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

## Kiro 深度审查修复与回滚索引

本轮以 `603905dfc476` 为基线，历史图片及后续 33 项修复分别提交。后续修复提交正文含中文排查说明；协议和账号可观察变化由对应回归覆盖。共享 Anthropic SSE 缓冲器按真实终态判定成功，Kiro OpenAI 桥接通过专用标记保留流错误后的部分用量。

| 提交 | 修复项 | 排查点 |
| --- | --- | --- |
| `03f1ab19f928` | 历史图片保留 | 各轮 user/tool_result 图片逐消息保留，覆盖图片位置、消息距离和历史数量。 |
| `26f626f387b9` | 搜索结果关联 | 搜索结果通过 tool_use_id 关联对应 server_tool_use，覆盖流式、缓冲与同一响应多次搜索。 |
| `212bc894a617` | 搜索流终态 | 隐藏中间搜索轮次的消息头与终态，保留最终轮的用量、停止原因、message_stop 和请求转换上下文。 |
| `a5c8020deb01` | 搜索禁用选择 | tool_choice=none 在原生及通用搜索捷径生效，执行 MCP 前退出。 |
| `57241896087c` | OpenAI 缓冲错误 | Responses/Chat 缓冲响应要求完整终态；流内错误、读取错误或缺终态返回非 2xx，并保留已观察用量。 |
| `d4b1d78ae980` | 搜索数字精度 | 替换搜索描述、追加历史和调整 SSE 索引时保留 JSON 大整数，避免工具参数变值。 |
| `e408da78cc01` | 正文工具语法 | XML invoke 与 [Called] 等正文保持文本；工具调用仅来自结构化上游事件。 |
| `7584a28d7732` | 工具 Schema 保真 | 保留根级及嵌套 enum、联合类型、布尔 schema 等约束，避免清洗时强制改成 object。 |
| `cb9c2209ff7e` | 工具参数精度 | 结构化工具响应与重放历史保持大整数和嵌套数字精度。 |
| `0cf43d5db3fa` | 工具 none 语义 | none 禁用当前工具与历史工具补齐；上游违约调用显式报错。 |
| `b8a30e38d708` | 自定义搜索工具名 | 混合普通工具时将 remote_web_search 还原为客户端声明的自定义工具名。 |
| `2d0ed072cba1` | 截断工具调用 | 工具 JSON 不完整或缺必需字段时显式失败，保留失败语义。 |
| `9a5984f4702a` | Thinking 停止原因 | 只含 thinking 的流式与非流式响应保持真实停止原因；尾随 end_turn 不覆盖已报告截断。 |
| `e94fca8350c8` | Thinking 配置优先级 | 显式 thinking type/budget 优先于模型别名后缀；beta 头只声明能力。 |
| `846330dddc93` | 空工具结果 | 省略 tool_result.content 保持为空结果，由 is_error 决定状态。 |
| `9e727e5316e0` | 原生缓存计量 | 普通输入与 cache read/write 分开计量；协议层保留显式零值，估算仅填补缺失用量。 |
| `4044f7902589` | 账号身份隔离 | token、刷新锁及 cooldown 按稳定账号 ID 隔离。替代注册标识键时，旧条目自然过期；切换和回滚应避免新旧刷新锁并行。 |
| `40d29cdb2cde` | 凭据缓存失效 | 后台与手动刷新、凭据更新和账号恢复共用 Kiro token 缓存失效逻辑。 |
| `6085fa00cb6b` | 刷新锁与快照 | 强制刷新尊重共享锁，拒绝回用失败或空 token；版本更新及刷新竞态后同步 token/profile 完整快照，避免重复 OAuth 与旧元数据重试。 |
| `8068d8e50525` | 模型映射与 thinking 转换 | 先映射实际模型再转换 reasoning 和校验；跨协议转换保留显式 thinking 配置。 |
| `7253e322f709` | 部分用量与流错误 | Kiro 错误/取消保留已观察用量和 typed error；已输出后终止当前尝试。其他平台的 OpenAI 兼容流错误计费保持原策略。 |
| `a37505030be1` | 显式零用量计费 | 上游显式 input_tokens=0 在服务计费边界保持为零，仅缺失用量才回填估算。 |
| `d020a4936771` | 首帧异常与成功时机 | 预读并重放首帧；异常先分类，完整生成成功后才清除冷却计数，覆盖原生搜索。 |
| `c58c1729bb07` | 接受回调 | 真实上游接受后回调一次，覆盖三种协议与原生搜索，释放请求串行队列。 |
| `4b8e477a9c8f` | 文档内容边界 | text document 保留为文本，其他来源在取 token 前返回协议对应的 400，覆盖三种入口。 |
| `407fe420d081` | 限流期间粘性路由 | 瞬时限流期间释放 Kiro 粘性路由，冷却结束后恢复；额度拒绝标记保持独立。 |
| `6b6ad2e5a6d6` | 原生混合搜索边界 | 内置 web_search_*/google_search 与客户端工具混合时明确 400；自定义同名工具、单独内置搜索和 none 旁路保持各自语义。 |
| `64fac3676ea0` | 账号错误状态 | Kiro 专用状态不被通用处理覆盖；瞬时限流与额度耗尽区分，并覆盖真实 RateLimitService 的最终状态和恢复。 |
| `c58d440d68e4` | 模型容量与账号可用性 | 模型容量不足按请求级瞬时错误处理；模型 HTTP/首帧和 MCP 429 只给真实速率限制记账号冷却。 |
| `b3f5bc28e31f` | 永久认证与权限状态 | 永久刷新错误保留账号 error 状态；普通 403 进入有界临时停调，避免通用分类覆盖或漏处理。 |
| `50c2f898b807` | 模型回显与推理计费 | Messages 普通/原生及模拟搜索回显原模型名；OpenAI 桥接按映射后的模型保留 max，计费记录实际模型。 |
| `a7f9ff187182` | 搜索请求头隔离 | 原生搜索区分客户端请求头和上游响应头，保持请求转换上下文来源。 |
| `885822cad5a7` | 搜索流异常与部分用量 | 搜索最终轮异常时转发已生成内容、用量及结构化错误，保留真实限流状态，不伪造正常结束。 |
| `83cadc430868` | 刷新锁竞争降级 | 其他 worker 正在刷新时返回请求级临时切换，保持账号可用，避免把刷新锁竞争记成认证封禁。 |

格式化单独提交：`d974c4229697`。

### 回滚顺序与依赖

- 每个行为修复有独立提交；回滚整批时先撤销文档与格式化，再按上表从下往上执行 `git revert <提交>`。单项回滚前检查所列依赖并重新运行对应回归。
- 原生混合搜索边界依赖文档内容边界中的 `InvalidRequestError` 和工具 none 语义；显式零用量计费依赖原生缓存计量；永久认证状态、刷新锁竞争降级依赖刷新锁与快照。
- 模型容量、永久认证、模型回显的运行时回归复用公共测试夹具；按提交顺序应用、反序回滚，保留这些夹具的依赖关系。
- 账号身份隔离同时切换 token 缓存、刷新锁和冷却键。发布或回滚时停止旧版本接流量后再切换，保持同一账号使用同一套锁键；旧 token 缓存等待 TTL 到期。

### 交叉分析与验证

参考源码固定在以下版本，修复依据本仓库协议契约和实际执行结果：

- [kiro-account-manager `13ff9fdb84ba`](https://github.com/hj01857655/kiro-account-manager/tree/13ff9fdb84ba8c56ee15384880685e663bc5d82e)。
- [Kiro-Go `f8f6071c9298`](https://github.com/Quorinex/Kiro-Go/tree/f8f6071c9298a4266ad3e0c7e483d4a2510cbcaf)。
- [kiro.rs `8fb3dd691d1c`](https://github.com/hank9999/kiro.rs/tree/8fb3dd691d1cedf437bd6a4e9ef9f60b3f217fdd)。
- [kiro2api-rs `3b7a47da18d5`](https://github.com/vagmr/kiro2api-rs/tree/3b7a47da18d5cf41f2fc284be0f55dcdac4cae2e)。

- Kiro 协议完整回归：`go test ./internal/kiro/... -count=1`。
- service 完整回归：`go test -tags=unit ./internal/service -count=1`，候选树最终运行通过（225.229s）；主工作树生产 Go 文件与该候选树逐字节一致。
- 主工作树验证：Kiro 回归及 service 的 `-run Kiro`；临时程序经实际网关入口运行 Messages、Responses、Chat Completions、原生单搜索的成功/中断 8 条路径，以及经本地 HTTP 的搜索模拟流式/非流式 2 条路径。
- 同一组 12 个请求覆盖真实 PNG 的首尾位置、历史距离、七张历史图片、工具结果图片、Schema、大整数、none、空工具结果和文档；原生零输入用量额外检查服务计费边界。

### 支持边界与未验证

- 文档支持 `source.type=text` 的字符串内容；PDF/base64、URL 和其他文档来源返回 400，供客户端显式转换。
- 单独内置搜索及自定义搜索工具各自保留原语义；内置 `web_search_*`/`google_search` 与客户端工具混合返回 400。
- 运行时冒烟使用生成的 AWS EventStream、本地 HTTP 和真实 Redis Store（miniredis），验证网关行为。本轮只做本地提交，线上 Kiro 实流量与发布后 Claude Code 行为尚未验证。

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
