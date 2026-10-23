# 本地测试环境（deploy/testenv）

一套独立的 Docker 环境，用于集成测试和回归验证。和 `deploy/docker-compose.dev.yml` 的区别：

- 使用独立的 compose project `sub2api-testenv`、独立命名卷和端口，不影响开发实例。
- 管理员、数据库口令、JWT/TOTP 密钥都是固定值，快照恢复后可以直接登录使用。
- 测试数据（上游账号、分组、用户、API Key）在管理后台手动维护，用 `snapshot` 固化成基线，回归前用 `restore` 回到基线。

## 依赖

需要 `docker`（含 compose v2）、`curl`、`jq`。`e2e` 子命令还需要本机装有 Go。

## 典型流程

```bash
deploy/testenv/testenv.sh up                 # 首次：构建并启动
# 浏览器打开 http://127.0.0.1:18080，用 admin@sub2api.test / Admin@Testenv123 登录，
# 完成合规确认，按需添加上游账号、分组、用户和 API Key
deploy/testenv/testenv.sh snapshot baseline  # 固化成基线

cp deploy/testenv/local.env.example deploy/testenv/local.env   # 填入冒烟用的 API Key 和模型
deploy/testenv/testenv.sh smoke

# 之后每次改完代码：
deploy/testenv/testenv.sh up                 # 重建镜像，数据卷保留
deploy/testenv/testenv.sh restore baseline   # 需要干净数据时恢复基线
deploy/testenv/testenv.sh smoke
```

也可以用 `make testenv-up` / `testenv-smoke` / `testenv-down`。

## 命令

| 命令 | 作用 |
|---|---|
| `up` | 用当前源码构建镜像并启动；启动前自动导出 `pre-up-<时间>-<提交>` 快照（见下文）；设 `TESTENV_SKIP_BUILD=1` 跳过构建 |
| `smoke` | 回归冒烟：健康检查、无效 Key 返回 401，再按 `local.env` 测试 Anthropic Messages（流式/非流式）和 OpenAI Chat/Responses |
| `e2e` | 用 `local.env` 里的 Key 跑 `backend/internal/integration` 的 e2e 测试，可追加 `-run` 等参数 |
| `snapshot [name]` / `restore [name]` / `snapshots` | 导出、恢复、列出数据库快照（`<快照目录>/<name>.sql.gz`，默认 `baseline`），恢复时会清空 Redis |
| `down` | 停止环境，保留数据 |
| `destroy` | 删除容器和所有数据卷，需设 `TESTENV_CONFIRM_DESTROY=1`；快照文件不受影响 |
| `status` / `logs [svc]` / `info` | 查看状态、日志、连接信息 |

## 固定配置

| 项 | 值 |
|---|---|
| 应用 | `http://127.0.0.1:18080` |
| 初始管理员 | `admin@sub2api.test` / `Admin@Testenv123`（只在首次初始化时创建） |
| Postgres | `127.0.0.1:15432`，用户 `sub2api` / 密码 `sub2api-testenv`，库 `sub2api` |
| Redis | `127.0.0.1:16379`，无密码 |
| 出站代理 | 默认 `http://host.docker.internal:7897`，可用 `TESTENV_HTTP_PROXY` / `TESTENV_HTTPS_PROXY` / `TESTENV_ALL_PROXY` / `TESTENV_NO_PROXY` 覆盖 |

端口可以用 `TESTENV_APP_PORT`、`TESTENV_PG_PORT`、`TESTENV_REDIS_PORT` 覆盖。

## 持久化

- 数据库、Redis、`/app/data` 存在 Docker 命名卷 `sub2api-testenv_{postgres,redis,app}_data` 中。`up`、`down`、重启 Docker 或主机都不影响这些卷；只有 `destroy`、`docker compose down -v` 和 `docker volume prune` 会删除它们（环境停止时执行 prune 同样会删）。
- 快照目录默认为 `/data/workspaces/sub2api-testenv/snapshots`（权限 700，文件权限 600），放在仓库外，`git clean` 不会删到。可以用 `TESTENV_SNAPSHOT_DIR` 覆盖。
- 新镜像启动时会自动执行数据库迁移，迁移只能往前升，不能回退。所以 `up` 在启动前会先导出 `pre-up-<时间>-<提交>[-dirty]` 快照，只保留最近 `TESTENV_PRE_UP_KEEP` 份（默认 10）。`baseline` 等手动快照不会被自动清理。设 `TESTENV_SKIP_PRE_UP_SNAPSHOT=1` 可以跳过这一步。
- 切到较旧分支前，先用 `restore` 恢复到该版本对应的 pre-up 快照，再执行 `up`，避免旧代码读到已经升级过的库。

## 注意

- 快照和 `local.env` 都包含真实凭据（上游账号密钥、API Key），已被 git 忽略，不要外传。
- 冒烟会真实调用上游并消耗额度，每次运行约 5 个请求。
- `go test -tags=unit` 和 `go test -tags=integration` 不依赖本环境：integration 测试用 testcontainers 自己起临时库。本环境负责整机层面的验证，覆盖真实镜像、迁移和 HTTP 链路。
