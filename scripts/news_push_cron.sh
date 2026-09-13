#!/usr/bin/env bash
# 每 6 小时运行一次新闻简报管线，并把生成的 digest 主动推送到 QQ 私聊。
#
# 由 launchd (com.litou.news-summary-push) 在 00:00 / 06:00 / 12:00 / 18:00 调用。
# 手动调试:
#   bash scripts/news_push_cron.sh                 # 完整跑一遍（约 3-6 分钟）
#   SKIP_PIPELINE=1 bash scripts/news_push_cron.sh # 跳过管线，直接推送最新 digest
#   DRY_RUN=1 bash scripts/news_push_cron.sh       # 只打印，不真的发 QQ
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_DIR"

SKIP_PIPELINE="${SKIP_PIPELINE:-0}"
DRY_RUN="${DRY_RUN:-0}"

LOGDIR="$PROJECT_DIR/runlogs"
mkdir -p "$LOGDIR"
LOG="$LOGDIR/cron_$(date +%Y%m%d_%H%M%S).log"

# 所有输出同时进日志文件（launchd 的 stdout/stderr 也指向 runlogs/）
exec >>"$LOG" 2>&1

log() { echo "[$(date '+%F %T')] $*"; }

log "=== news_push_cron 启动 (pid=$$, skip_pipeline=$SKIP_PIPELINE, dry_run=$DRY_RUN) ==="

before="$(ls -t data/digest_*.md 2>/dev/null | head -1 || true)"
log "运行前最新 digest: ${before:-（无）}"

if [ "$SKIP_PIPELINE" = "1" ]; then
  log "[SKIP_PIPELINE] 跳过管线执行"
else
  log "开始运行管线: ./news-summary-agent -slot manual"
  start_ts=$(date +%s)
  ./news-summary-agent -slot manual -config ./config -data ./data
  run_exit=$?
  log "管线结束 exit=$run_exit 用时=$(( $(date +%s) - start_ts ))s"
  log "分类体系文件最后修改: $(date -r config/categories.json '+%F %T' 2>/dev/null || echo '?')"
fi

newest="$(ls -t data/digest_*.md 2>/dev/null | head -1 || true)"

if [ -z "$newest" ]; then
  log "没有找到任何 digest 文件，结束（不发消息）"
  exit 0
fi

if [ "$SKIP_PIPELINE" != "1" ] && [ "$newest" = "$before" ]; then
  log "本次没有生成新的 digest（可能无新增入选新闻），结束（不发消息）"
  exit 0
fi

log "准备推送: $newest ($(wc -c <"$newest" | tr -d ' ') bytes)"

push_args=("$newest")
[ "$DRY_RUN" = "1" ] && push_args+=(--dry)

# 推送目标 openid 从项目根 .env 读取（.env 已 gitignore，绝不写进仓库）。
# 只取这一个变量，避免把 API key 等一并 export 给子进程。
if [ -z "${QQ_TARGET_OPENID:-}" ] && [ -f "$PROJECT_DIR/.env" ]; then
  QQ_TARGET_OPENID="$(grep -E '^QQ_TARGET_OPENID=' "$PROJECT_DIR/.env" | tail -1 | cut -d= -f2- | tr -d '"'"'"' \r')"
  export QQ_TARGET_OPENID
fi

if node scripts/qq_push.mjs "${push_args[@]}"; then
  if [ "$DRY_RUN" = "1" ]; then
    log "DRY_RUN：已打印内容，未实际发送"
  else
    log "QQ 推送成功"
  fi
else
  log "QQ 推送失败（详见上方输出）"
  exit 1
fi

log "=== 完成 ==="
