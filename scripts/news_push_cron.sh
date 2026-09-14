#!/usr/bin/env bash
# 每 6 小时运行一次新闻简报管线，把最新 digest 压成《要点》后推送到 QQ 私聊：
# 先发要点文本，再把简报原文（.md）作为文件附件发出。同时重建对外新闻网页。
#
# 由 launchd (com.litou.news-summary-push) 在 00:00 / 06:00 / 12:00 / 18:00 调用。
# 手动调试:
#   bash scripts/news_push_cron.sh                  # 完整跑一遍（约 3-6 分钟）
#   SKIP_PIPELINE=1 bash scripts/news_push_cron.sh  # 跳过管线，直接推送最新 digest
#   DRY_RUN=1 bash scripts/news_push_cron.sh        # 只打印，不真的发 QQ
#   GENERATE_BRIEF=0 bash scripts/news_push_cron.sh # 跳过要点生成，直接推原文
#   GENERATE_SITE=0 bash scripts/news_push_cron.sh  # 跳过网页生成
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_DIR"

SKIP_PIPELINE="${SKIP_PIPELINE:-0}"
DRY_RUN="${DRY_RUN:-0}"
GENERATE_BRIEF="${GENERATE_BRIEF:-1}"
GENERATE_SITE="${GENERATE_SITE:-1}"

# 站点对外地址只影响 feed 里的绝对链接；从 .env 取，避免写死在仓库里。
if [ -z "${NEWS_SITE_BASE_URL:-}" ] && [ -f "$PROJECT_DIR/.env" ]; then
  NEWS_SITE_BASE_URL="$(grep -E '^NEWS_SITE_BASE_URL=' "$PROJECT_DIR/.env" | tail -1 | cut -d= -f2- | tr -d '"'"'"' \r')"
  export NEWS_SITE_BASE_URL
fi

# DDNS 平时由常驻的 serve 进程每 5 分钟自查一次；这里再兜一道，防止 serve 没在跑时
# 域名悄悄停在旧地址上（2026-09 就发生过：取 IP 的方法失效，域名一直指向旧 IP）。
DDNS_ENABLED=""
if [ -f "$PROJECT_DIR/.env" ]; then
  DDNS_ENABLED="$(grep -E '^DDNS_ENABLED=' "$PROJECT_DIR/.env" | tail -1 | cut -d= -f2- | tr -d '"'"'"' \r')"
fi

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

# 重建对外新闻网页。放在推送之前，这样即使 QQ 推送失败，网页也已经更新；
# 失败只记日志，绝不影响推送。
if [ "$GENERATE_SITE" = "1" ]; then
  site_args=(-mode site -data ./data -public ./public)
  [ -n "${NEWS_SITE_BASE_URL:-}" ] && site_args+=(-base-url "$NEWS_SITE_BASE_URL")
  if ./news-summary-agent "${site_args[@]}"; then
    log "新闻网页已更新: public/index.html"
  else
    log "新闻网页生成失败(exit=$?)，不影响推送"
  fi
else
  log "[GENERATE_SITE=0] 跳过网页生成"
fi

# 域名保活兜底。失败只记日志，不影响推送。
if [ "$DDNS_ENABLED" = "1" ]; then
  if ./news-summary-agent -mode ddns; then
    log "DDNS 同步完成"
  else
    log "DDNS 同步失败（不影响推送）"
  fi
else
  log "[DDNS] 未启用，跳过"
fi

# 先用 headless 把 digest 压成《要点》。要点生成失败/超时不能挡住新闻本身，
# 所以失败时回退为直接推送简报原文。
brief_ok=0
brief=""
if [ "$GENERATE_BRIEF" = "1" ]; then
  brief="data/brief_$(date +%Y%m%d_%H%M%S).md"
  log "生成要点: bash scripts/news_brief.sh $newest $brief"
  if bash scripts/news_brief.sh "$newest" "$brief"; then
    brief_ok=1
    log "要点生成成功: $brief"
  else
    log "要点生成失败(exit=$?)，回退为直接推送简报原文"
    rm -f "$brief"
    brief=""
  fi
else
  log "[GENERATE_BRIEF=0] 跳过要点生成，直接推送简报原文"
fi

if [ "$brief_ok" = "1" ]; then
  push_args=("$brief" --file "$newest")
else
  push_args=("$newest")
fi
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
  elif [ "$brief_ok" = "1" ]; then
    log "QQ 推送成功（要点 + $newest 附件）"
  else
    log "QQ 推送成功（仅简报原文，要点未生成）"
  fi
else
  log "QQ 推送失败（详见上方输出）"
  exit 1
fi

# brief_*.md / *.stderr.log 不在管线 cleanup 的匹配范围内，这里自行清理 7 天前的残留。
# 刻意不叫 digest_*.md：否则会被本脚本的 `ls -t data/digest_*.md` 误认成简报。
find data -maxdepth 1 \( -name 'brief_*.md' -o -name 'brief_*.stderr.log' \) -mtime +7 -delete 2>/dev/null || true

log "=== 完成 ==="
