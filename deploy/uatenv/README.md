# UAT 环境（deploy/uatenv）

用脱敏后的生产数据演练发版：先在这里按生产相同的步骤升级（先 expand 迁移，再换镜像），问题尽量在这里发现并修掉，确认通过后才去生产执行。只由 SRE 会话操作（见 `/data/sre/bsj/sub2api/SRE.md`）。

## 与 testenv 的边界

| | testenv | uatenv |
|---|---|---|
| 数据 | 合成数据，在后台手动维护 | 生产只读导出，导入后脱敏 |
| 用途 | 日常开发回归、冒烟、e2e | 发版前的生产数据升级演练 |
| 实例 | compose 项目 `sub2api-testenv` | compose 项目 `sub2api-uatenv` |
| 端口 | 18080 / 15432 / 16379 | 28080 / 25432 / 26379 |
| 快照目录 | `/data/sre/bsj/sub2api/testenv/snapshots` | `/data/sre/bsj/sub2api/uatenv/snapshots` |
| token 刷新 | 开启 | compose 里固定为 `false`，不能用环境变量改 |

两者是独立实例，有各自的容器和命名卷。不要把 uatenv 的快照恢复到 testenv，也不要反过来。

两者共用 `deploy/testenv/testenv.sh` 的实现：`uatenv.sh` 设置 `env_name=uatenv` 后 source 该文件，端口、compose 文件、变量前缀（`UATENV_`）和运行态目录随之切换。生产相关的子命令只在 uatenv 下可用，在 testenv 上调用会直接失败并提示改用 `uatenv.sh`。

## 依赖

- `docker`（含 compose v2）、`curl`、`jq`、`kubectl`、`python3`。
- 生产 kubeconfig：在 `deploy/uatenv/local.env`（git 忽略，参考 `local.env.example`）里设置 `UATENV_PROD_KUBECONFIG=<路径>`。可选 `UATENV_PROD_CONTEXT`（默认 `default`）、`UATENV_PROD_NAMESPACE`（默认 `sub2api`）、`UATENV_PROD_PG_DEPLOY`（默认 `deploy/postgres`）。
- 外部 Docker 网络 `sub2api-testenv_testenv` 及其中的生产桥接容器，见下文「桥接网络依赖」。

## 固定配置

| 项 | 值 |
|---|---|
| 应用 | `http://127.0.0.1:28080` |
| 管理员 | `admin@sub2api.test` / `Admin@Testenv123`（导入生产数据后由脱敏步骤改成这个账号） |
| Postgres | `127.0.0.1:25432`，用户 `sub2api` / 密码 `sub2api-uatenv`，库 `sub2api` |
| Redis | `127.0.0.1:26379`，无密码 |
| 容器 | `sub2api-uatenv-{app,postgres,redis}` |
| 命名卷 | `sub2api-uatenv_{app,postgres,redis}_data` |
| 出站代理 | 默认 `http://host.docker.internal:7897`，可用 `UATENV_HTTP_PROXY` / `UATENV_HTTPS_PROXY` / `UATENV_ALL_PROXY` / `UATENV_NO_PROXY` 覆盖 |

端口只绑定 127.0.0.1，可以用 `UATENV_APP_PORT`、`UATENV_PG_PORT`、`UATENV_REDIS_PORT` 覆盖。应用通过容器名 `sub2api-uatenv-postgres`、`sub2api-uatenv-redis` 访问数据库和 Redis：应用同时在 testenv 网络里，那里也有叫 `postgres`、`redis` 的服务，用短名会解析错。

## 命令

```bash
deploy/uatenv/uatenv.sh <command>
```

| 命令 | 作用 |
|---|---|
| `up` | 用当前源码构建镜像（`sub2api-uatenv:local`）并启动；启动前自动导出 `pre-up-*` 快照；`UATENV_SKIP_BUILD=1` 跳过构建 |
| `smoke` | 回归冒烟，Key 来自 `local.env` |
| `snapshot [name]` / `restore [name]` / `snapshots` | 导出、恢复、列出快照；恢复时清空 Redis，并做 RT 冲突检查 |
| `prod-clone [name]` | 生产只读导出 → 导入 → 脱敏 → 只读比对 → 用生产同一镜像启动。`name` 默认当天日期 |
| `prod-upgrade [name]` | 构建当前源码 → 旧版运行中执行 expand 迁移 → 切换到新镜像 |
| `prod-check [name]` | 只读比对生产与 uatenv 的数据一致性，不一致即失败（`prod-clone` 会自动调用） |
| `down` | 停止环境，保留数据 |
| `destroy` | 删除容器和所有数据卷，需设 `UATENV_CONFIRM_DESTROY=1`；快照文件不受影响 |
| `status` / `logs [svc]` / `info` | 查看状态、日志、连接信息 |

uatenv 不提供 `e2e`，e2e 在 testenv 跑。

## 演练流程

```bash
deploy/uatenv/uatenv.sh up                    # 首次：构建并启动空库
deploy/uatenv/uatenv.sh prod-clone 20261030   # 复刻生产，旧版镜像运行
deploy/uatenv/uatenv.sh prod-upgrade 20261030 # 模拟升级
```

| 步骤 | 做了什么 | 快照 |
|---|---|---|
| prod-clone | 记录生产镜像 digest 和迁移账本；对生产执行 `pg_dump`，只读。不导出日志、时序指标和计费去重大表的数据（`usage_logs`、`usage_billing_dedup[_archive]`、`ops_system_logs`、`ops_error_logs`、`ops_system_metrics`、`ops_metrics_hourly/daily`、`ops_ingress_reject_aggregates`、`channel_monitor_histories`、`channel_monitor_daily_rollups`、`channel_monitor_v2_*_1m/_rollup`，清单见 `testenv.sh` 的 `prod_skip_data_tables`）；配置、告警规则、模板、水位表照常导出。导出前后各记一次生产行数 | |
| | 当前 uatenv 库留快照，再整库替换；检查 `channel_monitor_v2_config` 必须有 1 行 | `pre-prod-clone-<name>` |
| | 脱敏（见下节），再做 RT 冲突检查和 `prod-check` | |
| | 用生产当前镜像（同一 digest）启动 | `prod-<name>-old` |
| prod-upgrade | `deploy/testenv/expand_migrations.py` 按应用同一 advisory lock 和 checksum 口径补齐迁移，旧版保持运行。预检失败就停在这里 | `prod-<name>-expanded` |
| | 换成当前源码镜像 | `prod-<name>-upgraded` |

升级完成后至少验证以下几项，结果写进 `/data/sre/bsj/sub2api/reports/<日期>-*.md`：

- 迁移转换结果：端点、前置组、落地前置、平台策略。
- 每条落地的出口 IP 和升级前一致。
- 账号状态、可调度数和生产一致，没有被误暂停。
- 各平台业务请求返回 200：Kiro 走 `/v1/messages`，openai 分组走 `/v1/responses`。
- 一轮巡检后没有新增故障事件或告警。

回到升级前：`restore prod-<name>-old`，再用 `prod-import/<name>/old-image` 里记录的镜像启动。

## 只读比对（prod-check）

生产侧只执行 `select`，psql 会话设为 `default_transaction_read_only=on`。比对项：

- `schema_migrations` 的条数和最大文件名，必须与本次读到的生产值相同。
- `public` 下所有未跳过数据的表的行数（配置、规则、模板、水位等）。`settings` 不计脱敏时删除的 `ops_email_notification_config`。生产在导出期间仍有写入，所以 uatenv 的行数落在 `prod-clone` 导出前、导出后和本次三次生产读数的范围内即算一致。
- 任一侧缺表也算不一致。

不一致时列出差异并失败，差异同时写入 `prod-import/<name>/prod-check.diff`。升级之后迁移账本必然领先生产，这时单独执行 `prod-check` 会报迁移差异，属预期。

## 脱敏

导入生产数据后、启动应用前执行，任何一步失败都中止：

- 账号凭据删掉 `refresh_token`、`client_secret`、`id_token`；access token 保留，到期前可测推理。脱敏后仍有账号带 refresh token 就中止。
- 关闭 Ollama Cloud、OpenCode Go 用量自动刷新；关闭到期、额度、余额通知；清空 SMTP 配置；删除运维邮件通知配置；停用定时测试计划。
- 第一个管理员改为 `admin@sub2api.test` / `Admin@Testenv123` 并关闭 TOTP；JWT 密钥改为 uatenv 固定值。
- `TOKEN_REFRESH_ENABLED` 在 compose 中固定为 `false`，脚本在复刻和升级前后都会从 compose 配置和容器内两侧确认。

注意：

- Kiro 等平台的 access token 过期后无法刷新，Kiro 推理请在复刻后一小时内测完。
- 脱敏只防止 uatenv 轮换生产 RT 和对外发信，库里仍有生产用户、API Key 和上游 access token，快照和导出文件按生产数据对待。

## 运行态目录与快照

- 快照：`/data/sre/bsj/sub2api/uatenv/snapshots/<name>.sql.gz`，可用 `UATENV_SNAPSHOT_DIR` 覆盖。`up` 前自动导出 `pre-up-*`，保留最近 `UATENV_PRE_UP_KEEP` 份（默认 10）。
- 导出：`/data/sre/bsj/sub2api/uatenv/prod-import/<name>/`，可用 `UATENV_PROD_WORK_DIR` 覆盖。包含 `prod.dump`、`prod.info`、`old-image`、`new-image.id`、`prod-counts-*.txt`、`uatenv-counts.txt`、`expand-*.txt`。
- uatenv 的脚本以 umask 077 运行：目录 0700，文件 0600。内容含生产凭据，不要外传，也不要放进 Git。

## 桥接网络依赖

生产集群里迁移后的端点会连 `mihomo-hop.sub2api.svc.cluster.local` 等集群域名。宿主机上用 `/data/sre/bsj/sub2api/prod/prod-bridges/compose.yaml` 复刻了 `mihomo-hop`、`mihomo-hop-pool`（同镜像、同配置，网络别名为生产的集群域名），它们只接在 testenv 的网络 `sub2api-testenv_testenv` 上。

uatenv 的应用容器因此额外加入这个外部网络（compose 中的 `prod_bridge`，可用 `UATENV_BRIDGE_NETWORK` 改名）。后果：

- 必须先有 testenv 网络（testenv 启动过即存在）和桥接容器，`up`、`prod-clone` 在网络不存在时直接失败。
- testenv 执行 `destroy` 或删除该网络会让 uatenv 断开桥接。
- uatenv 不修改、不重启桥接容器。

长期应把桥接容器迁到独立的共享网络，testenv 和 uatenv 各自以 external 方式加入，属 S7 待办。
