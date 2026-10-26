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

- 不要把生产在用的 OAuth 账号（Kiro、Claude、OpenAI 等）导入测试环境。refresh token 刷新后会轮换，两边谁先刷新另一边就失效。测试环境要用单独的账号。`up` 和 `restore` 在启动 sub2api 前会比对测试库和生产库的 refresh token 哈希，有重复就拒绝启动；在 `local.env` 设置 `TESTENV_PROD_KUBECONFIG` 启用，`TESTENV_SKIP_RT_GUARD=1` 可跳过。

- 快照和 `local.env` 都包含真实凭据（上游账号密钥、API Key），已被 git 忽略，不要外传。
- 冒烟会真实调用上游并消耗额度，每次运行约 5 个请求。
- `go test -tags=unit` 和 `go test -tags=integration` 不依赖本环境：integration 测试用 testcontainers 自己起临时库。本环境负责整机层面的验证，覆盖真实镜像、迁移和 HTTP 链路。

## mihomo 首跳组实测（链式代理，fork）

以下涉及后台数据的操作**由使用者手动完成**。自动验证只启动本地 SOCKS/HTTP 服务器，不会在本测试环境新建或修改代理、分组、账号。

1. 执行 `deploy/testenv/testenv.sh up`，确认 `/health` 返回成功。新镜像自动执行幂等迁移 `249_proxy_hop_groups.sql`；既有代理默认行为不变。构建使用与仓库一致的 Go 1.27.3、pnpm 9。
2. 登录测试后台，进入**代理管理 → 首跳组 → 新建首跳组**。输入名称，选择 `fallback`（优先第一个健康节点）或 `url-test`（按延迟、容差切换）。默认检测地址 `https://www.gstatic.com/generate_204`、检测间隔 60 秒、容差 50 ms、订阅更新间隔 3600 秒。
3. 在订阅框每行粘贴一个自己的 Clash YAML 订阅 URL，或勾选手动成员。手动成员必须是已有的直连代理，不能引用另一个前置代理/首跳组；订阅中的 AnyTLS 等协议由 mihomo 解析。至少配置一个订阅或手动成员。Fallback 的手动成员按勾选顺序排列，之后是订阅节点。
4. 保存后查看组详情，等健康检查完成。应看到当前选中节点、各节点延迟/存活状态、订阅最近成功更新时间与节点数；页面每 10 秒刷新详情，也可点“刷新状态”。手动节点在运行时以 `hop-group-<组ID>-manual-<顺序>` 标识。
5. 编辑现有 chain 代理，将其**前置代理模式**改成**首跳组**，选择刚建立的组，保存。原链为 `[bwg → socks5 落地]` 时，修改落地代理即可得到 `[首跳组 → socks5 落地]`；更长链路修改原首跳之后那个代理，后续链路保持不变。不能给仍作为组内手动成员的代理配置前置。
6. 若希望全局跟随，打开**链式代理设置**，选择“默认首跳组”。它与“默认前置代理”二选一；业务代理需设置为“跟随默认”。既有显式前置代理不会被隐式改写。

### 验证自动切换与 fail-closed

- 先通过落地代理的“测试连接”确认出口可用，记录组当前节点和落地出口 IP。
- 在节点所在的测试机器上，**手动停止当前首跳节点服务，或临时阻断其服务端口**；不要通过修改后台成员列表代替网络故障，否则验证的是配置重建而非组内自动切换。
- 等待一个健康检查周期（加拨号超时），刷新组详情；当前节点应切换到下一个健康节点。再次测试同一个落地代理，应仍经该落地出口成功。检测窗口内的请求及已有 TCP 流可能失败；已有流不会自动迁移，新连接使用新选中节点。组内切换不需要重新保存 chain 代理，也不重建 Xray 链。
- 将所有首跳节点服务停掉后再测试：应连接失败，不能绕过组直接拨落地或目标。恢复一个节点并等待健康检查后，新连接应恢复。
- 修改组配置/切换为另一组时，旧桥入口与旧连接会关闭，并由现有 `Retain` 机制注销旧链；此情形与单纯组内健康切换不同。

### 订阅凭据与导入导出

- 订阅抓取遵守进程环境的 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`；testenv 容器的宿主代理可继续用于机场订阅抓取。该抓取代理不会成为业务链路的额外一跳。
- 管理接口只返回订阅掩码 `[REDACTED]` 和数量；编辑时默认保留地址，不会把掩码写回订阅。要修改地址，勾选“替换已保存的订阅地址”并输入完整的新列表；勾选后留空表示清除全部订阅（仍需保留至少一个手动成员）。
- 不要把订阅原文放进截图、日志、工单或版本库。日志不输出 mihomo 原始 payload，审计日志也会擦除 `subscription_urls`。
- 普通代理导出只携带组名称，不携带组配置或订阅凭据。跨实例导入前，先在目标实例手动建立同名组；组不存在时导入失败，不回退直连。

### 无外网回归

```bash
cd backend
go build ./...
go vet ./internal/pkg/...
go test -tags=unit ./internal/pkg/proxychain/ ./internal/pkg/mihomohop/ ./internal/service/
go test -tags=unit ./internal/handler/admin/ -run TestHopGroup
cd ../frontend
corepack pnpm@9 typecheck
corepack pnpm@9 test:run
```

首跳组单测使用本地 SOCKS5/HTTP 服务器，覆盖首节点不可用、全部不可用/空组不直连、mihomo→Xray→SOCKS 落地端到端、Clash YAML/AnyTLS 订阅解析、环境代理抓取、配置切换断开旧连接，以及 API/日志/审计订阅脱敏。

### 本次自动实测证据

| 检查 | 结果 |
|---|---|
| 后端 `go build ./...`、`go vet ./internal/pkg/...` | 通过 |
| `go test -tags=unit ./internal/pkg/proxychain/ ./internal/pkg/mihomohop/ ./internal/service/ -count=1` | 全部通过，包含真实 `Proxy.URL()` → mihomo → Xray → SOCKS 落地及 `Retain` 旧链失效 |
| 管理 API 的 `TestHopGroup` 脱敏/省略字段语义回归 | 通过 |
| 前端 `corepack pnpm@9 typecheck`、首跳组目录 ESLint | 通过 |
| 前端 `corepack pnpm@9 test:run` | 375 个文件、2913 个用例通过 |
| 迁移独立 PostgreSQL 冒烟 | 在临时隔离容器中执行迁移 249 两次成功；删除组后引用保留 `hop_group` 模式并清空 ID，仍 fail-closed |
| 独立 Go 程序真实网络冒烟 | 本地 `[mihomo fallback → Xray SOCKS 落地]` 返回预期响应；全不可用时失败，目标没有收到绕过请求 |
| Chromium 实际 Vue 界面 | 桌面与 390px 移动布局无页面横向溢出；隔离 API fixture 下完成组创建/编辑/删除、状态展示、订阅保留、默认代理/首跳组互斥及空选项保留另一默认值 |

界面冒烟使用实际组件和内存 API fixture，不登录或修改用户维护的 testenv 数据；真实机场订阅与远端节点故障演练仍由使用者按上述步骤执行。所有临时冒烟程序、页面和生成的性能分析测试二进制已从 worktree 清除。

运行环境的 Docker 默认网络曾无法解析 `goproxy.cn`；按同一 Dockerfile 使用 `docker build --network=host` 构建成功。验证启动使用 `TESTENV_SKIP_BUILD=1`，并仅对该次启动临时跳过 RT 生产比对，避免访问生产；没有改动脚本或默认保护行为。

最终运行镜像为 `sha256:239f795d150ec7e5aecc055a3ce3298438d9aff49c9b9e8f76b0e00dedc13686`：`testenv.sh up` 退出 0，`/health` 返回 `{"status":"ok"}`，Docker 容器健康状态为 `healthy`。只读 schema 元数据检查确认首跳组表、代理组引用列及 `ON DELETE SET NULL` 外键均已生效；未查询或修改用户维护的数据内容。

模块版本实测保持 `mihomo v1.19.33`、`xray-core v1.260206.0`、`refraction-networking/utls v1.8.2`，没有升级后两者。
