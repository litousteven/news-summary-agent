#!/usr/bin/env node
/**
 * 主动推送文本到 QQ 私聊（机器人2，走 qqbot-dsh 桥接的同一套凭据）。
 *
 * 用法:
 *   node scripts/qq_push.mjs <文本文件路径>
 *   node scripts/qq_push.mjs --text "直接发送的文本"
 *   node scripts/qq_push.mjs <文件> --dry        # 只打印，不发送
 *
 * 凭据来源: $QQBOT_DSH_ROOT/config.json（默认 /Users/litou/DSH/qqbot-dsh）
 *           的 qq.appId / qq.clientSecret
 * 目标 openid: 环境变量 QQ_TARGET_OPENID（必填，由 .env 提供；不写入仓库）
 */
import fs from 'node:fs'
import path from 'node:path'

const BRIDGE_ROOT = process.env.QQBOT_DSH_ROOT || '/Users/litou/DSH/qqbot-dsh'
const CONFIG_PATH = path.join(BRIDGE_ROOT, 'config.json')
const TOKEN_URL = 'https://bots.qq.com/app/getAppAccessToken'
const API_BASE = 'https://api.sgroup.qq.com'

const OPENID = process.env.QQ_TARGET_OPENID || ''
/** 单条消息最大字符数，超出分段发送（QQ content 限制按字节算，留足余量）。 */
const CHUNK_CHARS = 900

const argv = process.argv.slice(2)
const dry = argv.includes('--dry')
const textFlag = argv.indexOf('--text')

let text
if (textFlag !== -1) {
  text = argv[textFlag + 1] ?? ''
} else {
  const file = argv.find((a) => !a.startsWith('--'))
  if (!file) {
    console.error('用法: node scripts/qq_push.mjs <文本文件路径> | --text "文本" [--dry]')
    process.exit(2)
  }
  text = fs.readFileSync(file, 'utf8')
}

const chunks = []
for (let i = 0; i < text.length; i += CHUNK_CHARS) {
  chunks.push(text.slice(i, i + CHUNK_CHARS))
}

const openidLabel = OPENID ? `${OPENID.slice(0, 8)}…` : '(未设置)'
console.log(`[qq_push] openid=${openidLabel} chunks=${chunks.length} chars=${text.length} dry=${dry}`)
if (dry) {
  chunks.forEach((c, i) => console.log(`--- chunk ${i + 1}/${chunks.length} ---\n${c}`))
  process.exit(0)
}

if (!OPENID) {
  console.error('[qq_push] 未设置环境变量 QQ_TARGET_OPENID（应写在项目根 .env，已 gitignore）')
  process.exit(1)
}

const cfg = JSON.parse(fs.readFileSync(CONFIG_PATH, 'utf8'))
const { appId, clientSecret } = cfg.qq ?? {}
if (!appId || !clientSecret) {
  console.error(`[qq_push] 无法从 ${CONFIG_PATH} 读取 qq.appId / qq.clientSecret`)
  process.exit(1)
}

async function getToken() {
  const res = await fetch(TOKEN_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ appId, clientSecret }),
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok || !body.access_token) {
    throw new Error(`获取 access_token 失败: HTTP ${res.status} ${JSON.stringify(body)}`)
  }
  return body.access_token
}

const token = await getToken()

// 主动消息（不带 msg_id）。msg_seq 需要在同一 msg_id 下递增；主动消息固定 1。
let failed = 0
for (let i = 0; i < chunks.length; i++) {
  const res = await fetch(`${API_BASE}/v2/users/${encodeURIComponent(OPENID)}/messages`, {
    method: 'POST',
    headers: {
      Authorization: `QQBot ${token}`,
      'Content-Type': 'application/json',
      'User-Agent': 'QQBotDshBridge/0.1 (Node)',
    },
    body: JSON.stringify({ content: chunks[i], msg_type: 0, msg_seq: i + 1 }),
  })
  const bodyText = await res.text()
  if (!res.ok) {
    failed++
    console.error(`[qq_push] chunk ${i + 1}/${chunks.length} 发送失败: HTTP ${res.status} ${bodyText.slice(0, 400)}`)
  } else {
    console.log(`[qq_push] chunk ${i + 1}/${chunks.length} 已发送: ${bodyText.slice(0, 200)}`)
  }
  if (i < chunks.length - 1) await new Promise((r) => setTimeout(r, 600))
}

process.exit(failed > 0 ? 1 : 0)
