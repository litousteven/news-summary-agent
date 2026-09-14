#!/usr/bin/env node
/**
 * 主动推送文本 / 文件到 QQ 私聊（机器人2，走 qqbot-dsh 桥接的同一套凭据）。
 *
 * 用法:
 *   node scripts/qq_push.mjs <文本文件路径> [更多文本文件...]
 *   node scripts/qq_push.mjs --text "直接发送的文本"
 *   node scripts/qq_push.mjs <文本文件> --file <附件路径>   # 文本之后追加发送文件附件
 *   node scripts/qq_push.mjs <文件> --dry                  # 只打印，不发送
 *
 * 每个文本文件按内容独立分块发送（CHUNK_CHARS 一块，超出自动拆分，块间 600ms）；
 * 每个 --file 走 QQ 富媒体接口：base64 上传拿 file_info，再发 msg_type=7 消息。
 * 单次上传上限 20MB（QQ 接口限制），超出直接报错不发送。
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
/** QQ 富媒体单次上传上限（base64 走 file_data 的上限，与 SDK 的 MAX_UPLOAD_SIZE 一致）。 */
const MAX_UPLOAD_BYTES = 20 * 1024 * 1024
/** QQ 富媒体消息类型：4 = 文件。 */
const FILE_TYPE_FILE = 4
const USER_AGENT = 'QQBotDshBridge/0.1 (Node)'

const argv = process.argv.slice(2)
const dry = argv.includes('--dry')

const textFiles = []
const attachments = []
let directText
for (let i = 0; i < argv.length; i++) {
  const arg = argv[i]
  if (arg === '--dry') continue
  if (arg === '--text') {
    directText = argv[++i] ?? ''
    continue
  }
  if (arg === '--file') {
    const value = argv[++i]
    if (value === undefined || value.startsWith('--')) {
      console.error('[qq_push] --file 需要一个文件路径参数')
      process.exit(2)
    }
    attachments.push(value)
    continue
  }
  if (arg.startsWith('--')) {
    console.error(`[qq_push] 未知参数: ${arg}`)
    process.exit(2)
  }
  textFiles.push(arg)
}

if (directText === undefined && textFiles.length === 0 && attachments.length === 0) {
  console.error('用法: node scripts/qq_push.mjs <文本文件路径>... | --text "文本" [--file <附件>] [--dry]')
  process.exit(2)
}

/** 待发送的文本消息块，按传入顺序排列。 */
const chunks = []
if (directText !== undefined) {
  for (let i = 0; i < directText.length; i += CHUNK_CHARS) chunks.push(directText.slice(i, i + CHUNK_CHARS))
}
for (const file of textFiles) {
  const text = fs.readFileSync(file, 'utf8')
  for (let i = 0; i < text.length; i += CHUNK_CHARS) chunks.push(text.slice(i, i + CHUNK_CHARS))
}

/** 附件元信息（dry 模式也要能打印出大小，所以先 stat）。 */
const attachmentPlans = []
for (const file of attachments) {
  const abs = path.resolve(file)
  let info
  try {
    info = fs.statSync(abs)
  } catch {
    console.error(`[qq_push] 附件不存在或不可读: ${abs}`)
    process.exit(2)
  }
  if (!info.isFile()) {
    console.error(`[qq_push] 附件不是普通文件: ${abs}`)
    process.exit(2)
  }
  if (info.size > MAX_UPLOAD_BYTES) {
    console.error(`[qq_push] 附件 ${abs} 为 ${info.size} 字节，超过 QQ 单次上传上限 ${MAX_UPLOAD_BYTES} 字节`)
    process.exit(2)
  }
  attachmentPlans.push({ abs, name: path.basename(abs), size: info.size })
}

const openidLabel = OPENID ? `${OPENID.slice(0, 8)}…` : '(未设置)'
const totalChars = chunks.reduce((sum, c) => sum + c.length, 0)
console.log(
  `[qq_push] openid=${openidLabel} chunks=${chunks.length} chars=${totalChars} `
  + `files=${attachmentPlans.length} dry=${dry}`,
)

if (dry) {
  chunks.forEach((c, i) => console.log(`--- chunk ${i + 1}/${chunks.length} ---\n${c}`))
  attachmentPlans.forEach((p, i) =>
    console.log(`--- file ${i + 1}/${attachmentPlans.length} --- ${p.name} (${p.size} bytes)`),
  )
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
const authHeaders = {
  Authorization: `QQBot ${token}`,
  'Content-Type': 'application/json',
  'User-Agent': USER_AGENT,
}

// 主动消息（不带 msg_id）。msg_seq 需要在同一 msg_id 下唯一，这里全程递增。
let msgSeq = 0
let failed = 0

for (let i = 0; i < chunks.length; i++) {
  msgSeq++
  const res = await fetch(`${API_BASE}/v2/users/${encodeURIComponent(OPENID)}/messages`, {
    method: 'POST',
    headers: authHeaders,
    body: JSON.stringify({ content: chunks[i], msg_type: 0, msg_seq: msgSeq }),
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

for (let i = 0; i < attachmentPlans.length; i++) {
  const plan = attachmentPlans[i]
  try {
    // 1) 上传富媒体（file_data=base64，srv_send_msg=false 表示只上传不代发）
    const uploadRes = await fetch(`${API_BASE}/v2/users/${encodeURIComponent(OPENID)}/files`, {
      method: 'POST',
      headers: authHeaders,
      body: JSON.stringify({
        file_type: FILE_TYPE_FILE,
        srv_send_msg: false,
        file_data: fs.readFileSync(plan.abs).toString('base64'),
        file_name: plan.name,
      }),
    })
    const uploadBody = await uploadRes.json().catch(() => ({}))
    if (!uploadRes.ok || !uploadBody.file_info) {
      failed++
      console.error(`[qq_push] file ${i + 1}/${attachmentPlans.length} 上传失败: HTTP ${uploadRes.status} ${JSON.stringify(uploadBody).slice(0, 400)}`)
      continue
    }

    await new Promise((r) => setTimeout(r, 600))

    // 2) 用 file_info 发富媒体消息
    msgSeq++
    const sendRes = await fetch(`${API_BASE}/v2/users/${encodeURIComponent(OPENID)}/messages`, {
      method: 'POST',
      headers: authHeaders,
      body: JSON.stringify({ msg_type: 7, media: { file_info: uploadBody.file_info }, msg_seq: msgSeq }),
    })
    const sendBody = await sendRes.text()
    if (!sendRes.ok) {
      failed++
      console.error(`[qq_push] file ${i + 1}/${attachmentPlans.length} 发送失败: HTTP ${sendRes.status} ${sendBody.slice(0, 400)}`)
    } else {
      console.log(`[qq_push] file ${i + 1}/${attachmentPlans.length} 已发送 (${plan.name}, ${plan.size} bytes): ${sendBody.slice(0, 200)}`)
    }
  } catch (err) {
    failed++
    console.error(`[qq_push] file ${i + 1}/${attachmentPlans.length} 异常: ${err.message}`)
  }
}

process.exit(failed > 0 ? 1 : 0)
