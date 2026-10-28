#!/usr/bin/env bash
# Sub2API 本地测试环境管理脚本。
#
# 测试数据（上游账号、分组、用户、API Key）由使用者在管理后台手动维护，
# 本脚本只负责环境生命周期、数据快照和回归冒烟。
#
# 用法：deploy/testenv/testenv.sh <command>
#   up        用当前源码构建镜像并启动（数据卷保留；启动前自动导出 pre-up 快照）
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
set -euo pipefail
# 让 $(...) 里的失败也中止脚本。
shopt -s inherit_errexit

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$here/../.." && pwd)
compose=(docker compose -f "$here/docker-compose.yml")
# 快照属于长期状态，放在仓库外，避免 git clean 误删。
snapshot_dir="${TESTENV_SNAPSHOT_DIR:-/data/workspaces/sub2api-testenv/snapshots}"
local_env="$here/local.env"

# local.env（已被 .gitignore 忽略）存放冒烟用的 Key 和模型，见 local.env.example。
if [ -f "$local_env" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$local_env"
  set +a
fi

app_url="http://127.0.0.1:${TESTENV_APP_PORT:-18080}"

log() { printf '\033[1;34m[testenv]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m[testenv]\033[0m %s\n' "$*" >&2; exit 1; }

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
  "${compose[@]}" logs --tail 80 sub2api >&2 || true
  die "sub2api 在 240 秒内未就绪"
}

# 用 docker build 构建镜像：compose v5 的 build 要求 buildx >= 0.17，
# 而发行版自带的 buildx 可能更旧。
build_image() {
  log "构建 sub2api 镜像（首次需要编译前后端，耗时较长）..."
  # 本机 DNS 常为 127.0.0.1（宿主机代理），默认 bridge 网络的构建容器解析不到外网，默认用 host 网络。
  docker build --network="${TESTENV_BUILD_NETWORK:-host}" -t "${SUB2API_TESTENV_IMAGE:-sub2api-testenv:local}" \
    --build-arg NPM_CONFIG_REGISTRY="${NPM_CONFIG_REGISTRY:-https://registry.npmmirror.com}" \
    --build-arg ALPINE_MIRROR="${ALPINE_MIRROR:-https://mirrors.aliyun.com}" \
    -f "$repo_root/Dockerfile" "$repo_root"
}

# 启动新镜像会自动执行数据库迁移且不可回退，先留一份升级前快照。
# 只在 postgres 已运行（即已有数据）时执行；保留最近 TESTENV_PRE_UP_KEEP 份（默认 10）。
pre_up_snapshot() {
  if [ "${TESTENV_SKIP_PRE_UP_SNAPSHOT:-}" = "1" ]; then
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

  local keep=${TESTENV_PRE_UP_KEEP:-10} old
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
# 生产库通过 kubectl 访问，在 local.env 里设置 TESTENV_PROD_KUBECONFIG 启用；
# TESTENV_PROD_NAMESPACE（默认 sub2api）、TESTENV_PROD_PG_DEPLOY（默认 deploy/postgres）可覆盖。
guard_shared_refresh_tokens() {
  if [ "${TESTENV_SKIP_RT_GUARD:-}" = "1" ]; then
    log "已跳过 RT 冲突检查（TESTENV_SKIP_RT_GUARD=1）"
    return 0
  fi
  local kubeconfig=${TESTENV_PROD_KUBECONFIG:-}
  if [ -z "$kubeconfig" ]; then
    log "未设置 TESTENV_PROD_KUBECONFIG，跳过 RT 冲突检查（测试环境不要导入生产 OAuth 账号）"
    return 0
  fi
  command -v kubectl >/dev/null || die "缺少依赖: kubectl（RT 冲突检查需要）"

  local testenv_hashes prod_hashes shared
  testenv_hashes=$("${compose[@]}" exec -T postgres psql -U sub2api -d sub2api -At -c "$refresh_token_hashes_sql")
  [ -n "$testenv_hashes" ] || return 0
  # shellcheck disable=SC2016 # 变量在 pod 内展开
  prod_hashes=$(KUBECONFIG=$kubeconfig kubectl -n "${TESTENV_PROD_NAMESPACE:-sub2api}" exec -i \
    "${TESTENV_PROD_PG_DEPLOY:-deploy/postgres}" -- sh -c 'psql -U "$POSTGRES_USER" -d "${POSTGRES_DB:-sub2api}" -At -f -' \
    <<<"$refresh_token_hashes_sql") ||
    die "无法读取生产库做 RT 冲突检查；确认生产没有共用账号后可设 TESTENV_SKIP_RT_GUARD=1 跳过"

  shared=$(join -t '|' -1 2 -2 2 -o 1.1,2.1 \
    <(sort -t '|' -k2,2 <<<"$testenv_hashes") <(sort -t '|' -k2,2 <<<"$prod_hashes") |
    awk -F '|' '{printf "测试#%s=生产#%s ", $1, $2}')
  if [ -n "$shared" ]; then
    die "测试库与生产共用 refresh token：${shared}。启动会互相刷掉对方的 RT，请先在测试库禁用这些账号并清除 token"
  fi
  log "RT 冲突检查通过"
}

cmd_up() {
  require_tools
  if [ "${TESTENV_SKIP_BUILD:-}" != "1" ]; then
    build_image
  fi
  pre_up_snapshot
  log "启动容器..."
  "${compose[@]}" up -d --no-build --remove-orphans --wait postgres redis
  guard_shared_refresh_tokens
  "${compose[@]}" up -d --no-build --remove-orphans
  wait_healthy
  cmd_info
}

cmd_info() {
  cat >&2 <<EOF

  管理后台   $app_url   admin@sub2api.test / Admin@Testenv123
  Postgres   127.0.0.1:${TESTENV_PG_PORT:-15432}  sub2api / sub2api-testenv，库 sub2api
  Redis      127.0.0.1:${TESTENV_REDIS_PORT:-16379}
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
  "${compose[@]}" stop sub2api
  gunzip -c "$file" | "${compose[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U sub2api -d sub2api >/dev/null
  # 调度快照、限流计数等缓存以数据库为准重建。
  "${compose[@]}" exec -T redis redis-cli FLUSHALL >/dev/null
  # 旧快照可能还带着生产账号的 RT。
  guard_shared_refresh_tokens
  "${compose[@]}" start sub2api
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

cmd_destroy() {
  if [ "${TESTENV_CONFIRM_DESTROY:-}" != "1" ]; then
    die "destroy 会删除测试库、Redis 和应用数据卷（手动维护的测试数据会丢失，快照文件保留）。确认后执行: TESTENV_CONFIRM_DESTROY=1 $0 destroy"
  fi
  "${compose[@]}" down -v --remove-orphans
  log "已删除测试环境容器与数据卷"
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
    down) "${compose[@]}" down ;;
    destroy) cmd_destroy ;;
    status) "${compose[@]}" ps ;;
    logs) "${compose[@]}" logs -f --tail 100 "$@" ;;
    info) cmd_info ;;
    *) sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; [ -z "$cmd" ] || exit 1 ;;
  esac
}

main "$@"
