#!/usr/bin/env bash
# 安装/更新「每 6 小时跑新闻简报并推送到 QQ」的 launchd 定时任务。
#
# 需要在普通终端或 openclaw 的 exec 里运行（DSH agent 沙箱内会被拒绝）。
# 幂等，可重复执行。
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LABEL="com.litou.news-summary-push"
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

# 卸载旧实例（没装过就忽略）
launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
for _ in $(seq 1 30); do
  launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1 || break
  sleep 1
done

launchctl bootstrap "$DOMAIN" "$DST"
launchctl enable "$DOMAIN/$LABEL"

echo "已安装并启动 $LABEL"
launchctl print "$DOMAIN/$LABEL" | grep -E "^\s+(state|pid|runs|last exit code|program)" || true
echo
echo "看日志:   tail -f $PROJECT_DIR/runlogs/cron_*.log"
echo "立即跑一次: launchctl kickstart -k $DOMAIN/$LABEL"
echo "停止并卸载: launchctl bootout $DOMAIN/$LABEL && rm -f $DST"
