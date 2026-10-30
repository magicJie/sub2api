#!/usr/bin/env bash
# Sub2API UAT 环境管理脚本：用脱敏后的生产数据演练发版升级，只由 SRE 会话操作。
#
# 与 testenv 是两套独立实例（端口 28080/25432/26379、独立卷），实现复用 deploy/testenv/testenv.sh。
#
# 用法：deploy/uatenv/uatenv.sh <command>
#   up        用当前源码构建镜像并启动（数据卷保留；启动前自动导出 pre-up 快照）
#   smoke     用 local.env 里配置的 Key 跑网关回归冒烟
#   snapshot  把当前数据库导出到 <快照目录>/<name>.sql.gz（默认 baseline）
#   restore   从 <快照目录>/<name>.sql.gz 恢复数据库（默认 baseline）
#   snapshots 列出已有快照
#   prod-clone [name]    从生产只读导出业务数据，导入并脱敏，比对后用生产同一镜像启动，保存快照 prod-<name>-old
#   prod-upgrade [name]  在旧版镜像上 expand 迁移，再切到当前源码镜像，保存 prod-<name>-upgraded
#   prod-check [name]    只读比对生产与本库的迁移账本和表行数，不一致即失败
#   down      停止环境，保留数据卷
#   destroy   删除容器和所有数据卷（不可恢复，快照文件不受影响）
#   status    查看容器状态
#   logs      跟随日志，可追加服务名
#   info      打印连接信息
set -euo pipefail
shopt -s inherit_errexit

env_name=uatenv
# shellcheck source-path=SCRIPTDIR source=../testenv/testenv.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../testenv" && pwd)/testenv.sh"

case "${1:-}" in
  e2e) die "uatenv 不跑 e2e，用 deploy/testenv/testenv.sh e2e" ;;
esac
main "$@"
