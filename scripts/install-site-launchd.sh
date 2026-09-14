#!/usr/bin/env bash
# 安装/更新「新闻网页服务」的 launchd 常驻任务（监听 9000）。
#
# 需要在普通终端运行（DSH agent 沙箱内写不了 ~/Library/LaunchAgents）。幂等，可重复执行。
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LABEL="com.litou.news-site"
DOMAIN="gui/$(id -u)"
SRC="$PROJECT_DIR/launchd/${LABEL}.plist"
DST="$HOME/Library/LaunchAgents/${LABEL}.plist"

if [ ! -f "$SRC" ]; then
  echo "找不到 plist: $SRC" >&2
  exit 1
fi

plutil -lint "$SRC" >/dev/null

mkdir -p "$HOME/Library/LaunchAgents" "$PROJECT_DIR/runlogs"
cp "$SRC" "$DST"
chmod 644 "$DST"

# 先构建站点，避免服务因为 public/ 不存在而反复重启
if [ ! -f "$PROJECT_DIR/public/index.html" ]; then
  echo "构建站点…"
  (cd "$PROJECT_DIR" && ./news-summary-agent -mode site -data ./data -public ./public)
fi

launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
for _ in $(seq 1 30); do
  launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1 || break
  sleep 1
done

launchctl bootstrap "$DOMAIN" "$DST"
launchctl enable "$DOMAIN/$LABEL"

echo "已安装并启动 $LABEL"
launchctl print "$DOMAIN/$LABEL" | grep -E "^\s+(state|pid|runs|last exit code|program)" || true

addr="$(grep -E '^NEWS_SITE_ADDR=' "$PROJECT_DIR/.env" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '"'"'"' \r')"
addr="${addr:-0.0.0.0:9000}"
echo
echo "本机验证:   curl -sI http://127.0.0.1:${addr##*:}/ | head -1"
echo "健康检查:   curl -s http://127.0.0.1:${addr##*:}/healthz"
echo "看日志:     tail -f $PROJECT_DIR/runlogs/news-site.out.log"
echo "停止并卸载: launchctl bootout $DOMAIN/$LABEL && rm -f $DST"
