# Kiro fork 说明

本分支在上游 Wei-Shaw/sub2api 之上集成 Kiro（AWS CodeWhisperer / Amazon Q Developer）平台。
目标是让上游同步尽量是“直接 merge、零或极少冲突”。

## 约定

- Kiro 逻辑只放在 Kiro 自有文件里：
  - `backend/internal/kiro/`（协议转换、OAuth、websearch、tokenizer、冷却、continuation）
  - `backend/internal/service/kiro_*.go`
  - `backend/internal/handler/admin/kiro_oauth_handler.go`、`handler/dto/kiro.go`、`server/routes/kiro.go`
  - `backend/internal/domain/platforms_kiro.go`（用 `init` 追加到上游平台清单）
- 上游文件只允许两类改动：新增字段 / 嵌入结构体，或一行转发调用；都带 `Kiro（fork）` 注释，便于 `grep`。
- 不改 ent schema、不加数据库迁移。Kiro 的 prompt cache 计费模拟配置放在账号 `extra`：
  `kiro_cache_emulation_enabled`（bool）、`kiro_cache_emulation_ratio`（0~1，默认 1）。

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
| `service/gateway_service.go` | Kiro 依赖字段 |
| `service/ops_upstream_context.go` | `KiroOpsDiagnostics` 嵌入 |
| `service/wire.go` | `KiroProviderSet`；TokenRefreshService 注册 Kiro 刷新器 |
| `service/domain_constants.go`、`admin_group.go` | 常量别名、默认模型列表 |
| `handler/gateway_handler*.go` | Kiro 会话 hash、UMQ |
| `handler/admin/account_handler.go` | 可用模型、运行时状态 |
| `handler/dto/*.go`、`handler/{handler,wire}.go`、`server/routes/admin.go` | DTO 嵌入、handler 与路由注册 |
| `cmd/server/wire_gen.go` | wire 生成产物，冲突时重新生成，不要手工合并 |

测试侧只给上游测试的硬编码平台列表追加了 `kiro`，以及 `platformCatalog.builtin.json`（生成产物）。

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

## 未移植 / 待办

- 前端：Kiro 账号创建/编辑、OAuth 流程、用量展示（第二阶段）。
- 旧 fork 中与 Kiro 无关的改动（流失速 watchdog、harmony token 中和、OpenAI refresh token 复用处理等）未带入。
- 旧 fork 的分组级 cache emulation 列、usage_logs 会话指纹列及其审计 UI 未移植（需要改上游表结构）。
- Kiro 专用 keepalive 配置 `gateway.kiro_stream_keepalive_interval` 未移植，使用全局 `stream_keepalive_interval`。
