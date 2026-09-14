#!/usr/bin/env bash
# 用 `dsh --profile headless` 把一份 digest 简报压缩成 QQ 可读的「要点」。
#
# 用法:
#   bash scripts/news_brief.sh <digest.md> [out.md]
#
# 默认 out.md 为 <digest 同目录>/brief_<时间戳>.md。
# 退出码: 0 = 已写出非空要点；非 0 = 失败（调用方应回退为直接推送原文）。
#
# 环境变量:
#   DSH_CHECKOUT            dsh 源码 checkout（默认 /Users/litou/DSH/deepseek-harness）
#   NODE_BIN                含 node/pnpm 的目录（默认 /usr/local/bin）
#   BRIEF_PROFILE           dsh profile（默认 headless）
#   BRIEF_TIMEOUT_SECONDS   单次生成超时（默认 300）
#
# headless 是「一次性任务」模式：进程不监听端口、打印最终答案到 stdout 后退出。
# 它是普通进程，不要从另一个 agent 会话内部嵌套调用（会被文件沙箱挡在
# ~/.dsh/profiles/<profile>/ 的写入上）；本脚本面向终端与 launchd。
set -uo pipefail

CHECKOUT="${DSH_CHECKOUT:-/Users/litou/DSH/deepseek-harness}"
NODE_BIN="${NODE_BIN:-/usr/local/bin}"
BRIEF_PROFILE="${BRIEF_PROFILE:-headless}"
BRIEF_TIMEOUT_SECONDS="${BRIEF_TIMEOUT_SECONDS:-300}"

# launchd 的 PATH 极简；显式补齐 node/pnpm 所在目录。
export PATH="$NODE_BIN:/usr/bin:/bin:/usr/sbin:/sbin"
export DSH_HOME="${DSH_HOME:-$HOME/.dsh}"
export HOME="${HOME:-/Users/litou}"

digest="${1:-}"
[ -n "$digest" ] || { echo "用法: bash scripts/news_brief.sh <digest.md> [out.md]" >&2; exit 2; }
[ -f "$digest" ] || { echo "[news_brief] 找不到输入文件: $digest" >&2; exit 2; }

if [ -n "${2:-}" ]; then
  out="$2"
else
  out="$(dirname "$digest")/brief_$(date +%Y%m%d_%H%M%S).md"
fi

digest_abs="$(cd "$(dirname "$digest")" && pwd)/$(basename "$digest")"
errlog="${out%.md}.stderr.log"

prompt="读取文件 ${digest_abs}（一份中文新闻简报，markdown 格式），为它生成用于 QQ 推送的《要点》。

硬性要求：
1. 纯文本输出：不要 markdown 表格、不要 # 标题、不要代码块、不要罗列链接。
2. 第一行固定为：📌 新闻要点 · <该简报的日期时间，取自文件首行标题>
3. 紧接着输出 3-5 条要点，每条独立一行，以「· 」开头；每条不超过 45 个字；只保留事实、影响与变化，不要复述原文措辞。
4. 最后一行以「— 主线：」开头，用一句话概括这几条新闻的共同主线。
5. 只输出要点正文本身，不要任何解释、前言、后记、统计数字。"

echo "[news_brief] profile=$BRIEF_PROFILE input=$digest_abs timeout=${BRIEF_TIMEOUT_SECONDS}s" >&2

# macOS 自带工具里没有 timeout(1)，用后台进程 + 轮询实现超时。
# -s 抑制 pnpm 打印的「> 包名 script / > node ...」横幅，否则它会混进 stdout 被当成要点发出。
( cd "$CHECKOUT" && exec "$NODE_BIN/pnpm" -s dsh --profile "$BRIEF_PROFILE" "$prompt" ) \
  >"$out" 2>"$errlog" &
pid=$!

elapsed=0
while kill -0 "$pid" 2>/dev/null; do
  if [ "$elapsed" -ge "$BRIEF_TIMEOUT_SECONDS" ]; then
    echo "[news_brief] 超时 ${BRIEF_TIMEOUT_SECONDS}s，终止 pid=$pid" >&2
    kill -TERM "$pid" 2>/dev/null
    pkill -TERM -P "$pid" 2>/dev/null
    sleep 2
    kill -KILL "$pid" 2>/dev/null
    pkill -KILL -P "$pid" 2>/dev/null
    wait "$pid" 2>/dev/null
    : >"$out"   # 清掉半截输出，避免把残缺要点发出去
    exit 124
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done

wait "$pid"
rc=$?

if [ "$rc" -ne 0 ]; then
  echo "[news_brief] headless 退出码 $rc，stderr 尾部：" >&2
  tail -5 "$errlog" 2>/dev/null >&2
  exit "$rc"
fi

# 兜底清理前导噪声。pnpm 已用 -s 抑制横幅，但若仍混入「> ...」横幅或任务回显，
# 就以要点首行标记「📌」为锚点，丢弃它之前的所有内容；没有该标记时只剥开头的空行与横幅行。
if grep -q '^[[:space:]]*📌' "$out"; then
  awk '/^[[:space:]]*📌/ { found = 1 } found { print }' "$out" >"$out.tmp" && mv "$out.tmp" "$out"
else
  awk 'BEGIN { head = 1 }
       head && (/^[[:space:]]*$/ || /^> / || /^>$/) { next }
       { head = 0; print }' "$out" >"$out.tmp" && mv "$out.tmp" "$out"
fi

# 剥完后必须仍有内容，否则视为失败（回退推送原文）。
if [ -z "$(tr -d '[:space:]' <"$out")" ]; then
  echo "[news_brief] headless 输出为空（可能只产生了 reasoning），stderr 尾部：" >&2
  tail -5 "$errlog" 2>/dev/null >&2
  exit 1
fi

echo "[news_brief] 要点已生成: $out ($(wc -c <"$out" | tr -d ' ') bytes)" >&2
exit 0
