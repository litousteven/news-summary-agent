#!/usr/bin/env bash
# 常驻运行新闻网页服务（launchd 调用；也可在终端手动跑做调试）。
#
#   ./scripts/news_site_serve.sh                    # 用默认 0.0.0.0:9000
#   NEWS_SITE_ADDR=127.0.0.1:9000 ./scripts/news_site_serve.sh
#
# 只对外暴露 public/ 目录：仓库、data/、.env 都不在其中，服务端也禁用了
# 目录列表与非 GET/HEAD 方法（见 site/serve.go）。
#
# 站点配置从项目根 .env 读取（.env 已 gitignore，不写入仓库）：
#   NEWS_SITE_ADDR     监听地址，默认 0.0.0.0:9000
#   NEWS_SITE_TOKEN    访问口令，留空 = 完全公开
#   NEWS_SITE_BASE_URL 对外地址，如 https://news.example.com/（用于 RSS 绝对链接）
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_DIR"

# launchd 的 PATH 极简，显式补齐 node/go 运行时所在目录
export PATH="/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export HOME="${HOME:-/Users/litou}"

read_env() {
  [ -f "$PROJECT_DIR/.env" ] || return 0
  grep -E "^$1=" "$PROJECT_DIR/.env" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '"'"'"' \r'
}

addr="${NEWS_SITE_ADDR:-$(read_env NEWS_SITE_ADDR)}"
addr="${addr:-0.0.0.0:9000}"
token="${NEWS_SITE_TOKEN:-$(read_env NEWS_SITE_TOKEN)}"
base_url="${NEWS_SITE_BASE_URL:-$(read_env NEWS_SITE_BASE_URL)}"

# 服务启动前确保站点存在，否则 serve 会直接退出（launchd 会把 KeepAlive 变成重启风暴）
if [ ! -f "$PROJECT_DIR/public/index.html" ]; then
  echo "[news-site] public/index.html 不存在，先构建一次站点"
  gen_args=(-mode site -data ./data -public ./public)
  [ -n "$base_url" ] && gen_args+=(-base-url "$base_url")
  ./news-summary-agent "${gen_args[@]}" || true
fi

# base_url 只在生成站点时使用（feed 的绝对链接），serve 模式不需要它；
# 页面内部一律用相对链接，因此换端口/域名不必重启服务。
args=(-mode serve -addr "$addr" -public "$PROJECT_DIR/public")
[ -n "$token" ] && args+=(-site-token "$token")

echo "[news-site] 启动: addr=$addr token=$([ -n "$token" ] && echo 已设置 || echo 无)"
exec ./news-summary-agent "${args[@]}"
