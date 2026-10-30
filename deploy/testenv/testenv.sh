#!/usr/bin/env bash
# Sub2API 本地测试环境管理脚本。
#
# 测试数据（上游账号、分组、用户、API Key）由使用者在管理后台手动维护，
# 本脚本只负责环境生命周期、数据快照和回归冒烟。
#
# 用法：deploy/testenv/testenv.sh <command>
#   up        启动环境（数据卷保留；启动前自动导出 pre-up 快照）。设置 SUB2API_TESTENV_IMAGE 时
#             直接运行该镜像（如发布候选），否则用当前源码构建本地镜像（仅开发自测）
#   smoke     用 local.env 里配置的 Key 跑网关回归冒烟
#   e2e       用 local.env 里的 Key 跑 backend/internal/integration 的 e2e 测试
#   snapshot  把当前数据库导出到 <快照目录>/<name>.sql.gz（默认 baseline）
#   restore   从 <快照目录>/<name>.sql.gz 恢复数据库（默认 baseline）
#   snapshots 列出已有快照
#   down      停止环境，保留数据卷
#   destroy   删除容器和所有数据卷（不可恢复，快照文件不受影响）
#   status    查看容器状态
#   logs      跟随日志，可追加服务名
#   info      打印连接信息
#   image-info 打印应用容器的 image ID 与发布 label（JSON），供各环境报告比对
#
# 生产数据复刻与模拟升级只在独立的 uat 环境里做，见 deploy/uat/README.md。
set -euo pipefail
# 让 $(...) 里的失败也中止脚本。
shopt -s inherit_errexit

# 本文件同时是 deploy/uat/uat.sh 的实现：uat.sh 先设 env_name=uat 再 source 本文件。
# 直接执行时固定为 testenv，不受外部变量影响。
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  env_name=testenv
fi
env_name=${env_name:-}

log() { printf '\033[1;34m[%s]\033[0m %s\n' "$env_name" "$*" >&2; }
die() { printf '\033[1;31m[%s]\033[0m %s\n' "$env_name" "$*" >&2; exit 1; }

lib_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$lib_dir/../.." && pwd)
case "$env_name" in
  testenv)
    env_prefix=TESTENV
    default_ports=(18080 15432 16379)
    app_svc=sub2api
    # 外部网络依赖（uat 用于接入生产桥接），testenv 没有。
    bridge_network=
    ;;
  uat)
    env_prefix=UAT
    default_ports=(28080 25432 26379)
    app_svc=sub2api-uat
    bridge_network=${UAT_BRIDGE_NETWORK:-sub2api-testenv_testenv}
    # 运行态目录与文件都含生产数据：目录 0700，文件 0600。
    umask 077
    ;;
  *) die "未知环境: ${env_name:-<空>}（只能通过 testenv.sh 或 uat.sh 调用）" ;;
esac
here="$repo_root/deploy/$env_name"
compose=(docker compose -f "$here/docker-compose.yml")
app_container="sub2api-$env_name-app"
local_env="$here/local.env"

# 读取本环境的配置变量：cfg NAME DEFAULT 返回 ${TESTENV_|UAT_}NAME，未设置或为空时返回 DEFAULT。
cfg() {
  local v="${env_prefix}_$1"
  printf '%s' "${!v:-${2:-}}"
}

# local.env（已被 .gitignore 忽略）存放冒烟用的 Key 和模型，见 local.env.example。
if [ -f "$local_env" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$local_env"
  set +a
fi

# 快照属于长期状态，放在仓库外，避免 git clean 误删。
snapshot_dir=$(cfg SNAPSHOT_DIR "/data/sre/bsj/sub2api/$env_name/snapshots")
image_var="SUB2API_${env_prefix}_IMAGE"
# 设置了 SUB2API_<ENV>_IMAGE 即“运行指定镜像”，不构建。
image_pinned=${!image_var:+1}
app_image=${!image_var:-sub2api-$env_name:local}
app_port=$(cfg APP_PORT "${default_ports[0]}")
pg_port=$(cfg PG_PORT "${default_ports[1]}")
redis_port=$(cfg REDIS_PORT "${default_ports[2]}")
app_url="http://127.0.0.1:$app_port"

require_tools() {
  for tool in docker curl jq; do
    command -v "$tool" >/dev/null || die "缺少依赖: $tool"
  done
}

wait_healthy() {
  log "等待 sub2api 就绪 ($app_url/health)..."
  for _ in $(seq 1 120); do
    if curl -fsS -o /dev/null "$app_url/health" 2>/dev/null; then
      return 0
    fi
    sleep 2
  done
  "${compose[@]}" logs --tail 80 "$app_svc" >&2 || true
  die "sub2api 在 240 秒内未就绪"
}

# 用 docker build 构建镜像：compose v5 的 build 要求 buildx >= 0.17，
# 而发行版自带的 buildx 可能更旧。
# 版本号沿用 prod release.py 的口径：<HEAD 可达的最新稳定上游 tag 去掉 v>-<HEAD 7 位短 commit>，
# 工作区有未提交改动时再加 -dirty。后台检查更新只比较 "-" 前的 x.y.z，后缀不影响判定。
build_version() {
  local tag short
  tag=$(git -C "$repo_root" describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' HEAD 2>/dev/null) \
    || die "找不到 HEAD 可达的稳定版本 tag（v<x.y.z>），请先 git fetch --tags"
  short=$(git -C "$repo_root" rev-parse --short=7 HEAD)
  printf '%s-%s' "${tag#v}" "$short"
  if [ -n "$(git -C "$repo_root" status --porcelain --untracked-files=no 2>/dev/null)" ]; then
    printf -- '-dirty'
  fi
}

build_image() {
  local version commit date
  version=$(build_version)
  commit=$(git -C "$repo_root" rev-parse HEAD)
  date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  log "构建 sub2api 镜像 $version（首次需要编译前后端，耗时较长）..."
  # 本机 DNS 常为 127.0.0.1（宿主机代理），默认 bridge 网络的构建容器解析不到外网，默认用 host 网络。
  docker build --network="$(cfg BUILD_NETWORK host)" -t "$app_image" \
    --build-arg VERSION="$version" \
    --build-arg COMMIT="$commit" \
    --build-arg DATE="$date" \
    --build-arg NPM_CONFIG_REGISTRY="${NPM_CONFIG_REGISTRY:-https://registry.npmmirror.com}" \
    --build-arg ALPINE_MIRROR="${ALPINE_MIRROR:-https://mirrors.aliyun.com}" \
    -f "$repo_root/Dockerfile" "$repo_root"
}

# image_meta IMAGE [TAG]：输出镜像 ID 与发布构建写入的 label（JSON，缺失为 null）。
image_meta() {
  docker image inspect "$1" | jq -c --arg tag "${2:-$1}" '.[0] | (.Config.Labels // {}) as $l | {
    image_id: .Id, tag: $tag,
    lock: $l["io.sub2api.release.lock"], tree: $l["io.sub2api.release.tree"],
    revision: $l["org.opencontainers.image.revision"]}'
}

log_image_meta() {
  local meta=$1
  if [ -n "$(jq -r '.lock // empty' <<<"$meta")" ]; then
    log "镜像 $(jq -r '"\(.tag) id=\(.image_id) lock=\(.lock) tree=\(.tree) revision=\(.revision)"' <<<"$meta")"
  else
    log "镜像 $(jq -r '"\(.tag) id=\(.image_id)"' <<<"$meta")（无 io.sub2api.release.lock label：非发布候选，仅开发自测）"
  fi
}

# 确定本次运行的应用镜像，结果留在 $app_image_meta。testenv → uat → prod 用同一镜像：
# 候选只由 /data/sre/bsj/sub2api/prod/release/release.py build 构建一次，各环境通过
# SUB2API_<ENV>_IMAGE 复用；未指定时 testenv 用当前源码构建本地镜像（仅开发自测），
# uat 不构建，且只接受带发布 label 的候选。
prepare_image() {
  local hint="先用 /data/sre/bsj/sub2api/prod/release/release.py build 产出候选，在 testenv 用 SUB2API_TESTENV_IMAGE=<候选 tag> deploy/testenv/testenv.sh up 验证，再执行 SUB2API_UAT_IMAGE=<同一 tag> $0 $1"
  if [ -z "$image_pinned" ]; then
    [ "$env_name" != uat ] || die "uat 不构建镜像，必须设置 $image_var。$hint"
    if [ "$(cfg SKIP_BUILD)" != 1 ]; then build_image; fi
  fi
  docker image inspect "$app_image" >/dev/null 2>&1 || die "镜像不存在: $app_image"
  app_image_meta=$(image_meta "$app_image")
  log_image_meta "$app_image_meta"
  if [ "$env_name" = uat ] && [ -z "$(jq -r '.lock // empty' <<<"$app_image_meta")" ]; then
    die "uat 只运行发布候选，$app_image 缺少 io.sub2api.release.lock label。$hint"
  fi
}

# 一致性以 image ID 为准：确认应用容器实际运行的就是 prepare_image 检查过的镜像。
require_app_image() {
  local want running
  want=$(jq -r .image_id <<<"$app_image_meta")
  running=$(docker inspect "$app_container" --format '{{.Image}}')
  [ "$running" = "$want" ] || die "应用容器运行的镜像 $running 不是预期的 $want（$app_image 在启动期间被改指向？）"
}

# 启动新镜像会自动执行数据库迁移且不可回退，先留一份升级前快照。
# 只在 postgres 已运行（即已有数据）时执行；保留最近 <前缀>_PRE_UP_KEEP 份（默认 10）。
pre_up_snapshot() {
  if [ "$(cfg SKIP_PRE_UP_SNAPSHOT)" = "1" ]; then
    return 0
  fi
  if [ -z "$("${compose[@]}" ps -q --status running postgres 2>/dev/null)" ]; then
    return 0
  fi
  local rev
  rev=$(git -C "$repo_root" rev-parse --short HEAD 2>/dev/null || echo unknown)
  if [ -n "$(git -C "$repo_root" status --porcelain --untracked-files=no 2>/dev/null)" ]; then
    rev="$rev-dirty"
  fi
  cmd_snapshot "pre-up-$(date +%Y%m%d-%H%M%S)-$rev"

  local keep old
  keep=$(cfg PRE_UP_KEEP 10)
  # 文件名里带时间戳，按名称倒序即新到旧。
  while IFS= read -r old; do
    rm -f -- "$old"
    log "清理旧快照 $(basename -- "$old")"
  done < <(find "$snapshot_dir" -maxdepth 1 -name 'pre-up-*.sql.gz' -type f | sort -r | tail -n +"$((keep + 1))")
}

# 输出库里所有未删除账号的 "id|sha256(refresh_token)"。
refresh_token_hashes_sql="select id || '|' || encode(sha256(convert_to(credentials->>'refresh_token', 'UTF8')), 'hex')
  from accounts where deleted_at is null and coalesce(credentials->>'refresh_token', '') <> ''"

# OAuth 的 refresh token 刷新后旧值即失效（Kiro 等会轮换 RT），测试环境和生产共用同一个
# RT 时，谁先刷新另一边就失效。启动 sub2api 前比对两边 RT 的哈希，发现重复就拒绝启动。
# 生产库通过 kubectl 访问，在 local.env 里设置 <前缀>_PROD_KUBECONFIG 启用（前缀 TESTENV 或 UAT）；
# <前缀>_PROD_CONTEXT（默认 default）、_PROD_NAMESPACE（默认 sub2api）、_PROD_PG_DEPLOY（默认 deploy/postgres）可覆盖。
guard_shared_refresh_tokens() {
  if [ "$(cfg SKIP_RT_GUARD)" = "1" ]; then
    log "已跳过 RT 冲突检查（${env_prefix}_SKIP_RT_GUARD=1）"
    return 0
  fi
  if [ -z "$(cfg PROD_KUBECONFIG)" ]; then
    log "未设置 ${env_prefix}_PROD_KUBECONFIG，跳过 RT 冲突检查（不要直接导入生产 OAuth 账号）"
    return 0
  fi
  command -v kubectl >/dev/null || die "缺少依赖: kubectl（RT 冲突检查需要）"

  local testenv_hashes prod_hashes shared
  # 全新空库首次启动时应用尚未建表，没有账号可比对。
  if [ -z "$("${compose[@]}" exec -T postgres psql -U sub2api -d sub2api -At -c "select to_regclass('public.accounts')")" ]; then
    log "测试库尚无 accounts 表（空库首次启动），跳过 RT 冲突检查"
    return 0
  fi
  testenv_hashes=$("${compose[@]}" exec -T postgres psql -U sub2api -d sub2api -At -c "$refresh_token_hashes_sql")
  [ -n "$testenv_hashes" ] || return 0
  prod_hashes=$(prod_psql <<<"$refresh_token_hashes_sql") ||
    die "无法读取生产库做 RT 冲突检查；确认生产没有共用账号后可设 ${env_prefix}_SKIP_RT_GUARD=1 跳过"

  shared=$(join -t '|' -1 2 -2 2 -o 1.1,2.1 \
    <(sort -t '|' -k2,2 <<<"$testenv_hashes") <(sort -t '|' -k2,2 <<<"$prod_hashes") |
    awk -F '|' '{printf "测试#%s=生产#%s ", $1, $2}')
  if [ -n "$shared" ]; then
    die "测试库与生产共用 refresh token：${shared}。启动会互相刷掉对方的 RT，请先在测试库禁用这些账号并清除 token"
  fi
  log "RT 冲突检查通过"
}

# uat 的应用容器要加入 testenv 网络才能访问生产桥接（mihomo-hop 等），网络不存在时 compose 会报错。
require_bridge_network() {
  [ -n "$bridge_network" ] || return 0
  docker network inspect "$bridge_network" >/dev/null 2>&1 ||
    die "外部网络 $bridge_network 不存在：先启动 testenv（它创建该网络）与生产桥接，见 deploy/uat/README.md"
}

cmd_up() {
  require_tools
  prepare_image up
  require_bridge_network
  pre_up_snapshot
  log "启动容器..."
  "${compose[@]}" up -d --no-build --remove-orphans --wait postgres redis
  guard_shared_refresh_tokens
  "${compose[@]}" up -d --no-build --remove-orphans
  wait_healthy
  require_app_image
  cmd_info
}

cmd_image_info() {
  local id tag
  id=$(docker inspect "$app_container" --format '{{.Image}}' 2>/dev/null) || die "应用容器 $app_container 不存在"
  tag=$(docker inspect "$app_container" --format '{{.Config.Image}}')
  app_image_meta=$(image_meta "$id" "$tag" | jq -c --arg c "$app_container" '{container: $c} + .')
  log_image_meta "$app_image_meta"
  jq . <<<"$app_image_meta"
}

cmd_info() {
  cat >&2 <<EOF

  管理后台   $app_url   admin@sub2api.test / Admin@Testenv123
  Postgres   127.0.0.1:$pg_port  sub2api / sub2api-$env_name，库 sub2api
  Redis      127.0.0.1:$redis_port
  冒烟配置   $local_env $([ -f "$local_env" ] && echo "(已存在)" || echo "(不存在，参考 local.env.example)")

EOF
}

# check NAME EXPECTED_STATUS CURL_ARGS...：断言 HTTP 状态码，并把响应体留在 $body。
smoke_failures=0
smoke_passed=0
check() {
  local name=$1 expected=$2; shift 2
  local out status
  if ! out=$(curl -sS --max-time "${SMOKE_TIMEOUT:-120}" -w '\n%{http_code}' "$@"); then
    log "FAIL $name: curl 出错"
    smoke_failures=$((smoke_failures + 1))
    return 1
  fi
  status=${out##*$'\n'}
  body=${out%$'\n'*}
  if [ "$status" != "$expected" ]; then
    log "FAIL $name: 期望 HTTP $expected，实际 $status: ${body:0:300}"
    smoke_failures=$((smoke_failures + 1))
    return 1
  fi
}

expect_contains() {
  local name=$1 needle=$2
  if [[ $body != *"$needle"* ]]; then
    log "FAIL $name: 响应缺少 \"$needle\": ${body:0:300}"
    smoke_failures=$((smoke_failures + 1))
    return 1
  fi
  log "ok   $name"
  smoke_passed=$((smoke_passed + 1))
}

smoke_anthropic() {
  local key=$SMOKE_ANTHROPIC_KEY model=${SMOKE_ANTHROPIC_MODEL:-claude-sonnet-4-5}
  local headers=(-H "x-api-key: $key" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json')
  local msg
  msg=$(jq -nc --arg m "$model" '{model:$m, max_tokens:32, messages:[{role:"user", content:"Reply with the single word: pong"}]}')

  check anthropic-messages 200 -X POST "$app_url/v1/messages" "${headers[@]}" --data "$msg" &&
    expect_contains anthropic-messages '"type":"message"'
  check anthropic-messages-stream 200 -N -X POST "$app_url/v1/messages" "${headers[@]}" \
    --data "$(jq -c '.stream=true' <<<"$msg")" && expect_contains anthropic-messages-stream message_stop
}

smoke_openai() {
  local key=$SMOKE_OPENAI_KEY model=${SMOKE_OPENAI_MODEL:-gpt-5}
  local headers=(-H "Authorization: Bearer $key" -H 'Content-Type: application/json')
  local chat resp
  chat=$(jq -nc --arg m "$model" '{model:$m, messages:[{role:"user", content:"Reply with the single word: pong"}]}')
  resp=$(jq -nc --arg m "$model" '{model:$m, input:"Reply with the single word: pong"}')

  check openai-chat 200 -X POST "$app_url/v1/chat/completions" "${headers[@]}" --data "$chat" &&
    expect_contains openai-chat '"choices"'
  check openai-chat-stream 200 -N -X POST "$app_url/v1/chat/completions" "${headers[@]}" \
    --data "$(jq -c '.stream=true' <<<"$chat")" && expect_contains openai-chat-stream '[DONE]'
  check openai-responses 200 -X POST "$app_url/v1/responses" "${headers[@]}" --data "$resp" &&
    expect_contains openai-responses '"output"'
}

cmd_smoke() {
  require_tools
  wait_healthy

  check health 200 "$app_url/health" && expect_contains health ok
  check invalid-key 401 -X POST "$app_url/v1/messages" -H 'x-api-key: sk-testenv-invalid-000000000000' \
    -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' \
    --data '{"model":"claude-sonnet-4-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}' &&
    { log "ok   invalid-key"; smoke_passed=$((smoke_passed + 1)); }

  if [ -n "${SMOKE_ANTHROPIC_KEY:-}" ]; then smoke_anthropic; else log "skip anthropic: 未设置 SMOKE_ANTHROPIC_KEY"; fi
  if [ -n "${SMOKE_OPENAI_KEY:-}" ]; then smoke_openai; else log "skip openai: 未设置 SMOKE_OPENAI_KEY"; fi

  if [ "$smoke_failures" -ne 0 ]; then
    die "冒烟失败 $smoke_failures 项，通过 $smoke_passed 项"
  fi
  log "冒烟通过 $smoke_passed 项"
}

cmd_e2e() {
  wait_healthy
  [ -n "${SMOKE_ANTHROPIC_KEY:-}" ] || log "未设置 SMOKE_ANTHROPIC_KEY，Claude 相关 e2e 用例会被跳过"
  (cd "$repo_root/backend" && BASE_URL="$app_url" \
    CLAUDE_API_KEY="${SMOKE_ANTHROPIC_KEY:-}" GEMINI_API_KEY="${SMOKE_GEMINI_KEY:-}" \
    go test -tags=e2e -count=1 -timeout=300s ./internal/integration/... "$@")
}

cmd_snapshot() {
  local name=${1:-baseline}
  [[ $name =~ ^[A-Za-z0-9._-]+$ ]] || die "快照名只允许字母、数字、点、下划线和连字符"
  mkdir -p -m 700 "$snapshot_dir"
  local tmp="$snapshot_dir/.$name.sql.gz.tmp"
  "${compose[@]}" exec -T postgres pg_dump -U sub2api -d sub2api --clean --if-exists --no-owner | gzip >"$tmp"
  mv "$tmp" "$snapshot_dir/$name.sql.gz"
  chmod 600 "$snapshot_dir/$name.sql.gz"
  log "已导出 $snapshot_dir/$name.sql.gz（含上游账号凭据，勿外传）"
}

cmd_restore() {
  local name=${1:-baseline} file
  file="$snapshot_dir/$name.sql.gz"
  [ -f "$file" ] || die "快照不存在: $file"
  log "从 $file 恢复数据库（覆盖当前测试库）..."
  "${compose[@]}" stop "$app_svc"
  gunzip -c "$file" | "${compose[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U sub2api -d sub2api >/dev/null
  # 调度快照、限流计数等缓存以数据库为准重建。
  "${compose[@]}" exec -T redis redis-cli FLUSHALL >/dev/null
  # 旧快照可能还带着生产账号的 RT。
  guard_shared_refresh_tokens
  "${compose[@]}" start "$app_svc"
  wait_healthy
  log "恢复完成"
}

cmd_snapshots() {
  if compgen -G "$snapshot_dir/*.sql.gz" >/dev/null; then
    ls -lht "$snapshot_dir"/*.sql.gz
  else
    log "暂无快照（$snapshot_dir）"
  fi
}

# ---------------------------------------------------------------------------
# 生产复刻与模拟升级：只在 uat 运行（SRE 工作流，见 deploy/uat/README.md）
# ---------------------------------------------------------------------------

require_uat() {
  [ "$env_name" = uat ] || die "$1 只能在 uat 运行，testenv 只放合成数据。改用: deploy/uat/uat.sh $1"
}

prod_kubectl() {
  local kubeconfig
  kubeconfig=$(cfg PROD_KUBECONFIG)
  [ -n "$kubeconfig" ] || die "未设置 ${env_prefix}_PROD_KUBECONFIG（$local_env）"
  KUBECONFIG=$kubeconfig kubectl --context "$(cfg PROD_CONTEXT default)" -n "$(cfg PROD_NAMESPACE sub2api)" "$@"
}

# 生产库会话固定为只读事务。
prod_psql() {
  # shellcheck disable=SC2016 # 变量在 pod 内展开
  prod_kubectl exec -i "$(cfg PROD_PG_DEPLOY deploy/postgres)" -- \
    sh -c 'PGOPTIONS="-c default_transaction_read_only=on" psql -U "$POSTGRES_USER" -d "${POSTGRES_DB:-sub2api}" -v ON_ERROR_STOP=1 -At -f -'
}

env_psql() { "${compose[@]}" exec -T postgres psql -U sub2api -d sub2api -v ON_ERROR_STOP=1 -At "$@"; }

prod_work_dir() { echo "$(cfg PROD_WORK_DIR "/data/sre/bsj/sub2api/$env_name/prod-import")/$1"; }

# 只跳过日志、时序指标和计费去重这类大表的数据；配置、规则、模板、水位表照常导出。
prod_skip_data_tables=(usage_logs usage_billing_dedup usage_billing_dedup_archive
  ops_system_logs ops_error_logs ops_system_metrics ops_metrics_hourly ops_metrics_daily ops_ingress_reject_aggregates
  channel_monitor_histories channel_monitor_daily_rollups
  channel_monitor_v2_metrics_1m channel_monitor_v2_metrics_rollup
  channel_monitor_v2_user_metrics_1m channel_monitor_v2_user_metrics_rollup
  channel_monitor_v2_error_metrics_1m channel_monitor_v2_error_metrics_rollup
  channel_monitor_v2_latency_histograms_1m channel_monitor_v2_latency_histograms_rollup)

# 每行 "键|值"：@migrations 是 schema_migrations 的 "条数 最大文件名"，其余是 public 下
# 未跳过数据的表的行数。settings 不计脱敏会删掉的那一行。只有 select，生产与 uat 共用。
prod_counts_sql() {
  local excluded
  excluded=$(printf "'%s'," "${prod_skip_data_tables[@]}")
  cat <<SQL
select '@migrations|' || count(*) || ' ' || coalesce(max(filename), '') from schema_migrations;
select table_name || '|' || (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from public.%I%s', table_name,
         case when table_name = 'settings' then ' where key <> ''ops_email_notification_config''' else '' end),
       false, true, '')))[1]::text
  from information_schema.tables
 where table_schema = 'public' and table_type = 'BASE TABLE' and table_name not in (${excluded%,})
 order by 1;
SQL
}

prod_counts() { prod_counts_sql | prod_psql; }

# 导入生产数据后必须执行：去掉轮换生产 RT 与对外发信的途径，管理员换成本环境固定账号。
prod_sanitize() {
  local hash
  hash=$(docker run --rm httpd:alpine htpasswd -nbBC 10 x 'Admin@Testenv123' | cut -d: -f2 | sed 's/^\$2y\$/$2a$/')
  [ -n "$hash" ] || die "生成管理员密码哈希失败"
  env_psql -v h="$hash" -v jwt="$env_name-jwt-secret-0123456789abcdef0123456789abcdef" >/dev/null <<'SQL'
begin;
-- RT 与可刷新凭据：清空后无法轮换生产 RT（access token 保留，到期前可测推理）。
update accounts set credentials = credentials - 'refresh_token' - 'client_secret' - 'id_token'
 where coalesce(credentials->>'refresh_token','') <> '' or credentials ? 'client_secret';
update accounts set extra = jsonb_set(extra, '{ollama_cloud_usage_auto_refresh}', 'false') where extra ? 'ollama_cloud_usage_auto_refresh';
update accounts set extra = jsonb_set(extra, '{opencode_go_usage_auto_refresh}', 'false') where extra ? 'opencode_go_usage_auto_refresh';
-- 通知与邮件：不从本环境发出。
update settings set value='false' where key in ('subscription_expiry_notify_enabled','account_quota_notify_enabled','balance_low_notify_enabled');
update settings set value='' where key in ('smtp_host','smtp_username','smtp_password');
delete from settings where key='ops_email_notification_config';
update scheduled_test_plans set enabled=false where enabled;
-- 管理员与 JWT 换成本环境固定值（与 docker-compose.yml 一致）。
update users set email='admin@sub2api.test', password_hash=:'h', totp_enabled=false
 where id=(select min(id) from users where role='admin');
update security_secrets set value=:'jwt' where key='jwt_secret';
commit;
SQL
  local left
  left=$(env_psql -c "select count(*) from accounts where coalesce(credentials->>'refresh_token','')<>''")
  [ "$left" = 0 ] || die "脱敏后仍有 $left 个账号带 refresh token"
  log "脱敏完成：refresh token 0 个，通知/SMTP/定时测试已关，管理员 admin@sub2api.test"
}

# token 刷新在 uat 的 docker-compose.yml 里写死为 false，这里再从 compose 配置和容器两侧确认。
require_token_refresh_off() {
  [ "$("${compose[@]}" config --format json | jq -r --arg s "$app_svc" '.services[$s].environment.TOKEN_REFRESH_ENABLED')" = false ] ||
    die "docker-compose.yml 里 TOKEN_REFRESH_ENABLED 不是 false"
  if [ -n "$(docker ps -q --filter "name=^$app_container\$")" ]; then
    [ "$(docker exec "$app_container" printenv TOKEN_REFRESH_ENABLED)" = false ] || die "容器内 TOKEN_REFRESH_ENABLED 不是 false"
  fi
}

# 用指定镜像重建应用容器（不构建）。
recreate_app() {
  local image=$1
  "${compose[@]}" exec -T redis redis-cli FLUSHALL >/dev/null
  env "$image_var=$image" "${compose[@]}" up -d --no-build --force-recreate "$app_svc"
  wait_healthy
  require_token_refresh_off
}

# 只读比对生产与 uat：迁移账本条数/最大值，以及未跳过数据的表的行数。
# 迁移账本必须与本次生产读数相同。prod-clone 导出前后各读一次行数（文件留在导出目录），本次再读
# 一次；uat 的行数落在这几次生产读数的范围内即视为一致（导出期间仍在写入的表允许偏差）。
cmd_prod_check() {
  require_uat prod-check
  require_tools
  command -v kubectl >/dev/null || die "缺少依赖: kubectl"
  umask 077
  local name=${1:-$(date +%Y%m%d)} dir diff f prod_files=()
  dir=$(prod_work_dir "$name")
  mkdir -p -m 700 "$dir"
  prod_counts >"$dir/prod-counts-now.txt"
  prod_counts_sql | env_psql -f - >"$dir/uat-counts.txt"
  for f in "$dir/prod-counts-before.txt" "$dir/prod-counts-after.txt" "$dir/prod-counts-now.txt"; do
    [ -s "$f" ] && prod_files+=("$f")
  done
  [ -s "$dir/uat-counts.txt" ] || die "未读到 uat 行数"

  diff=$(awk -F '|' '
    { k = $1; v = substr($0, length($1) + 2) }
    FILENAME == ARGV[1] { uat[k] = v; keys[k]; next }
    {
      keys[k]; seen[k]
      if (k ~ /^@/) { pv[k] = v; next }
      if (!(k in lo) || v + 0 < lo[k]) lo[k] = v + 0
      if (!(k in hi) || v + 0 > hi[k]) hi[k] = v + 0
    }
    END {
      for (k in keys) {
        if (!(k in uat)) { print k ": uat 缺失"; continue }
        if (!(k in seen)) { print k ": 生产缺失（uat " uat[k] "）"; continue }
        if (k ~ /^@/) { if (pv[k] != uat[k]) print k ": 生产 " pv[k] "，uat " uat[k]; continue }
        if (uat[k] + 0 < lo[k] || uat[k] + 0 > hi[k])
          print k ": 生产 " (lo[k] == hi[k] ? lo[k] : lo[k] "~" hi[k]) "，uat " uat[k]
      }
    }' "$dir/uat-counts.txt" "${prod_files[@]}" | sort)
  if [ -n "$diff" ]; then
    printf '%s\n' "$diff" >"$dir/prod-check.diff"
    die "uat 与生产不一致（$(wc -l <<<"$diff") 项，已写入 $dir/prod-check.diff）：
$diff"
  fi
  rm -f -- "$dir/prod-check.diff"
  log "prod-check 通过：迁移 $(sed -n 's/^@migrations|//p' "$dir/uat-counts.txt")，$(grep -vc '^@' "$dir/uat-counts.txt") 张表行数一致"
}

cmd_prod_clone() {
  require_uat prod-clone
  require_tools
  command -v kubectl >/dev/null || die "缺少依赖: kubectl"
  umask 077
  local name=${1:-$(date +%Y%m%d)} dir image migrations
  dir=$(prod_work_dir "$name")
  mkdir -p -m 700 "$dir"
  require_token_refresh_off
  require_bridge_network

  # 1. 生产只读：镜像 digest、迁移账本、业务数据；导出前后各记一次行数供 prod-check 比对。
  image=$(prod_kubectl get deploy sub2api -o jsonpath='{.spec.template.spec.containers[0].image}')
  migrations=$(prod_psql <<<"select count(*)||' '||max(filename) from schema_migrations")
  printf 'image=%s\nmigrations=%s\nexported_at=%s\n' "$image" "$migrations" "$(date -Is)" >"$dir/prod.info"
  log "生产镜像 $image，迁移 $migrations"
  prod_counts >"$dir/prod-counts-before.txt"
  local t skip_data=()
  for t in "${prod_skip_data_tables[@]}"; do
    skip_data+=("--exclude-table-data=$t")
  done
  # shellcheck disable=SC2016
  prod_kubectl exec -i "$(cfg PROD_PG_DEPLOY deploy/postgres)" -- sh -c 'pg_dump -U "$POSTGRES_USER" -d "${POSTGRES_DB:-sub2api}" -Fc --no-owner --no-privileges "$@"' \
    sh "${skip_data[@]}" >"$dir/prod.dump"
  prod_counts >"$dir/prod-counts-after.txt"
  chmod 600 "$dir"/*
  log "已导出 $dir/prod.dump（$(du -h "$dir/prod.dump" | cut -f1)，含凭据，勿外传）"

  # 2. 当前库留快照，再整库替换。
  "${compose[@]}" up -d --no-build --wait postgres redis
  cmd_snapshot "pre-prod-clone-$name"
  "${compose[@]}" stop "$app_svc"
  "${compose[@]}" exec -T postgres psql -U sub2api -d postgres -v ON_ERROR_STOP=1 -At -c "select pg_terminate_backend(pid) from pg_stat_activity where datname='sub2api' and pid<>pg_backend_pid()" \
    -c "drop database sub2api" -c "create database sub2api owner sub2api" >/dev/null
  "${compose[@]}" exec -T postgres pg_restore -U sub2api -d sub2api --no-owner --no-privileges --exit-on-error <"$dir/prod.dump"
  [ "$(env_psql -c "select count(*) from channel_monitor_v2_config")" = 1 ] ||
    die "导入后 channel_monitor_v2_config 不是 1 行，配置表数据没有导出"
  prod_sanitize
  UAT_SKIP_RT_GUARD='' guard_shared_refresh_tokens
  cmd_prod_check "$name"

  # 3. 用生产同一镜像启动，确认旧版在复刻数据上正常，固化旧态快照。
  docker pull -q "$image" >/dev/null
  echo "$image" >"$dir/old-image"
  recreate_app "$image"
  cmd_snapshot "prod-$name-old"
  log "旧版已在复刻数据上运行；下一步：$0 prod-upgrade $name"
}

cmd_prod_upgrade() {
  require_uat prod-upgrade
  require_tools
  umask 077
  local name=${1:-$(date +%Y%m%d)} dir old_image new_image=$app_image
  prepare_image prod-upgrade
  dir=$(prod_work_dir "$name")
  [ -f "$dir/old-image" ] || die "未找到 $dir/old-image，先执行 prod-clone $name"
  old_image=$(cat "$dir/old-image")
  require_token_refresh_off

  # 1. 与生产同口径的 expand：旧版仍在运行，按新源码补齐迁移账本。预检失败即停止。
  log "expand（dry-run）..."
  python3 -I "$lib_dir/expand_migrations.py" "$repo_root/backend/migrations" \
    "${compose[@]}" exec -T postgres psql -U sub2api -d sub2api | tee "$dir/expand-plan.txt" >&2
  python3 -I "$lib_dir/expand_migrations.py" "$repo_root/backend/migrations" \
    "${compose[@]}" exec -T postgres psql -U sub2api -d sub2api --apply | tee "$dir/expand-apply.txt" >&2
  curl -fsS -o /dev/null "$app_url/health" || die "expand 后旧版不健康"
  cmd_snapshot "prod-$name-expanded"

  # 2. 切到新镜像（相当于生产换镜像）。
  jq . <<<"$app_image_meta" >"$dir/new-image.json"
  log "新镜像已记录到 $dir/new-image.json"
  recreate_app "$new_image"
  require_app_image
  log_image_meta "$app_image_meta"
  cmd_snapshot "prod-$name-upgraded"
  log "升级完成：旧 $old_image → 新 $new_image；回到升级前：$0 restore prod-$name-old 后用旧镜像启动"
}

cmd_destroy() {
  if [ "$(cfg CONFIRM_DESTROY)" != "1" ]; then
    die "destroy 会删除 $env_name 的数据库、Redis 和应用数据卷（库内数据会丢失，快照文件保留）。确认后执行: ${env_prefix}_CONFIRM_DESTROY=1 $0 destroy"
  fi
  "${compose[@]}" down -v --remove-orphans
  log "已删除 $env_name 容器与数据卷"
}

main() {
  local cmd=${1:-}
  [ $# -gt 0 ] && shift
  case "$cmd" in
    up) cmd_up ;;
    smoke) cmd_smoke ;;
    e2e) cmd_e2e "$@" ;;
    snapshot) cmd_snapshot "$@" ;;
    restore) cmd_restore "$@" ;;
    snapshots) cmd_snapshots ;;
    prod-clone) cmd_prod_clone "$@" ;;
    prod-upgrade) cmd_prod_upgrade "$@" ;;
    prod-check) cmd_prod_check "$@" ;;
    down) "${compose[@]}" down ;;
    destroy) cmd_destroy ;;
    status) "${compose[@]}" ps ;;
    logs) "${compose[@]}" logs -f --tail 100 "$@" ;;
    info) cmd_info ;;
    image-info) cmd_image_info ;;
    *) sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; [ -z "$cmd" ] || exit 1 ;;
  esac
}

# 被 uat.sh source 时由它调用 main。
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
