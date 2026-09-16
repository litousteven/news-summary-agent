# News Summary Agent

基于 [CloudWeGo Eino](https://github.com/cloudwego/eino) 框架实现的国际新闻简报 Agent。自动抓取 RSS 新闻源，通过 LLM 标注/去重/摘要，生成结构化新闻简报。

---

## 使用指南

### 环境要求

- Go 1.22+
- OpenAI 兼容的 ChatModel API（用于标注和摘要）
- OpenAI 兼容的 Embedding API（可选，用于语义去重）

### 安装

```bash
git clone <repo-url> && cd news-summary-agent
go mod download
```

### 配置

**1. API 密钥** — 复制并编辑 `.env`

```bash
cp .env.example .env
```

`.env` 文件说明：

| 变量 | 必填 | 说明 |
|------|------|------|
| `CHAT_MODEL_API_KEY` | 是 | ChatModel API 密钥 |
| `CHAT_MODEL_BASE_URL` | 否 | API 地址（默认 OpenAI） |
| `CHAT_MODEL_NAME` | 是 | 模型名称（如 `gpt-4o`、`deepseek-v3`） |
| `EMBEDDING_API_KEY` | 否 | Embedding API 密钥，不配置则跳过语义去重 |
| `EMBEDDING_BASE_URL` | 否 | Embedding API 地址 |
| `EMBEDDING_MODEL_NAME` | 否 | Embedding 模型名称 |
| `PROXY_ADDR` | 否 | HTTP 代理（访问海外 RSS 源用） |

**2. 运行参数** — 编辑 `config/config.yaml`

```yaml
max_items_per_feed: 16    # 每个 RSS 源最大抓取条目数
max_total_items: 180      # 总共最大抓取条目数（166 < 180，当前不构成约束）
max_digest_items: 10      # 简报最大入选条目数
max_per_category: 3       # 每个分类最大条目数
cluster_threshold: 0.75   # 语义去重相似度阈值 (0.0~1.0)
file_expiry_days: 2       # data/ 目录文件过期天数
max_news_age_days: 3      # 新闻时效窗口：发布超过此天数的条目直接丢弃
```

`max_news_age_days` 是防止**停更源**污染简报的闸门：一个 RSS 源停止更新后仍会持续返回
200 和它的最后一批条目，没有这道过滤就会被当成新内容反复标注、反复推送。发布时间的解析
失败时条目会被放行（年龄未知不等于过期），但会在日志里计数。推送历史的去重窗口自动取该值
+1 天。

**3. RSS 源** — 编辑 `config/feeds.yaml`

```yaml
- name: 中新网
  url: https://www.chinanews.com.cn/rss/world.xml
  lang: zh
  use_proxy: false
  enabled: true

- name: BBC
  url: https://feeds.bbci.co.uk/news/world/rss.xml
  lang: en
  use_proxy: true
  enabled: true
```

设置 `enabled: false` 可临时关闭某个源，无需删除配置。

> **停更源排查**：RSS 源停止更新后仍会返回 200 和旧的条目列表，从抓取层面看不出来。
> 每次运行可在日志里看每个源的健康状况：
> `[FetchRSS] source=XX count=N 跳过过期=K 无日期=M`，以及源疑似停更时的
> `⚠ 源可能已停更: source=XX 最新条目为 N 天前`。2026-09 曾有两个 zaobao 代理源
> 静默停更 47 天，确认后已停用（原因见 `config/feeds.yaml` 注释）。

#### 源质量档位（SourceRank）

`pipeline/fetch_rss/fetch.go` 的 `SourceRank` 给每个源一个质量档位，**数值越小越优先**。
它决定两件事：

1. **抓取顺序**——总条目上限是"先到先得"，排在前面的源先占预算。以前这个优先级
   靠 `feeds.yaml` 的行序**隐式**表达，调整排版就会意外改变优先级；现在它由档位
   显式决定（同档位保持文件顺序）。
2. **同一事件保留哪家**——`dedup.go` 合并重复报道时，保留档位更高的那个源。

**这张表必须显式覆盖每一个在用源。** 未登记的源会取 map 零值 `0`，而 `0` 恰好是
**最优档**（中新网 = 0），于是漏配的源会静默插到所有已评级源前面。2026-09 之前
21 个源里只有 5 个登记，其余 17 个都处于"顶配"状态，导致规则大部分时候是反的。

未登记的源现在得到 `UnknownSourceRank = 20`（偏后但非末尾，含义是"尚未评估"），
运行时会打印一条告警列出它们。

当前档位（0–11 沿用 `feeds.yaml` 已表达的偏好顺序）：

| 档 | 源 |
|---|---|
| 0–3 | 中新网 / BBC / NPR / NYT |
| 4 | Al Jazeera（已配档位，当前未启用） |
| 5–6 | 中新网-中国 / 中新网-财经 |
| 7–11 | ABC News / FOX News / Financial Times / France24 / Japan Times |
| 20 | 其余全部（含从未启用过的 WaPo/NBC/Guardian/DW/Sky，与已停更的 CNN/香港电台/The Independent/联合早报）——**尚未评估** |

要调整优先级，直接改 `SourceRank` 里的数字即可；新增源请同时补一个档位，
否则它会落在 20 档并触发告警。

### 运行

```bash
# 默认运行
go run .

# 指定推送档位（午间版/晚间版/凌晨版）
go run . -slot 12:00

# 自定义配置和数据目录
go run . -config ./config -data ./data
```

命令行参数：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-slot` | `manual` | 推送档位：`00:00` / `12:00` / `18:00` / `manual` |
| `-config` | `./config` | 配置目录路径 |
| `-data` | `./data` | 数据目录路径（运行时产出） |

### 输出

运行后输出到终端和 `data/` 目录：

| 文件 | 说明 |
|------|------|
| `data/digest_YYYYMMDD_HHMMSS.md` | 本次简报全文 |
| `data/brief_YYYYMMDD_HHMMSS.md` | 推送用《要点》（仅走推送流程时产出，见下节） |
| `data/push_history_YYYYMMDD.jsonl` | 推送历史（按天，用于去重） |
| `data/tagged_cache_YYYYMMDD.jsonl` | 标注缓存（按天，避免重复标注） |

超过 `file_expiry_days` 天的文件会在下次运行时自动清理。

### 去重机制

程序采用四级去重策略（优先级从高到低）：

1. **链接精确匹配** — 同一 URL 视为重复
2. **标题精确匹配** — display_title 完全一致视为重复
3. **向量相似度筛查** — embedding 相似度 >= 阈值时，进入 LLM 核查
4. **LLM 核查** — 由 LLM 根据内容判断是否描述同一事件

此外，在简报编排阶段还会对同分类内的新闻做**批内语义去重**（union-find 聚类）。

### 分类体系

新闻会被归入以下分类（由 `config/categories.json` 管理，首次运行自动从模板创建）：

- 战争与地缘
- 航空航天
- 军事装备
- AI与数码
- 新能源与汽车
- 全球经济
- 国内事务
- 其他重要动态

分类定义包含 `keywords`（用于模糊匹配）、`boundary`（适合归入的说明）和 `not_boundary`（不适合归入的情况说明）。`UpdateTaggingGuide` 节点会根据新闻数据自动分析并直接增删 `categories.json` 中的分类。

### 项目结构

```
.
├── config/                  # 配置文件（版本控制）
│   ├── config.yaml          # 运行参数
│   ├── feeds.yaml           # RSS 源
│   ├── categories.json      # 分类定义（自动初始化）
│   ├── tagging_guide.md     # 标注规范
│   └── tagging_examples.csv # 标注示例
├── data/                    # 运行时产出（.gitignore）
│   ├── push_history_*.jsonl
│   ├── tagged_cache_*.jsonl
│   └── digest_*.md
├── pipeline/                # 核心管线代码
├── .env                     # API 密钥（.gitignore）
├── .env.example             # API 密钥模板
└── main.go
```

---

## 设计说明

### 设计原则

1. **AI 只做"理解"，不做"I/O"**：所有文件读写、JSONL 操作由 Go Lambda 完成
2. **AI 只做"判断"，不做"决策"**：流程走向由图结构决定，AI 不选择执行哪一步
3. **结构化输入/输出**：LLM 输入预格式化，输出要求 JSON，Go 代码做解析和校验
4. **容错兜底**：LLM 输出不可能 100% 合法，Go 代码负责解析失败时的默认值填充

### Pipeline 流程

```
START
  │
  ▼
[FetchRSS] ─── 抓取 RSS 新闻
  │  []RawNewsItem
  ▼
[ParallelTag] ─── 分批并行标注（带缓存）
  │  ┌─────────────────────────────────┐
  │  │ TagSubGraph (eino SubGraph)     │
  │  │                                 │
  │  │  TagTemplate → TagChatModel     │
  │  │       → ParseTagResult         │
  │  │                                 │
  │  │  每批独立执行，失败直接丢弃     │
  │  └─────────────────────────────────┘
  │  []TaggedNewsItem
  ▼
[MergeHistory] ─── 与推送历史去重（链接→标题→向量→LLM）
  │  []MergedNewsItem
  ▼
[BuildDigest] ─── 分类/排序/限额 + 批内语义去重
  │  *DigestData
  ▼
[TranslateItems] ─── 翻译外文新闻
  │  *DigestData
  ▼
[FormatSummaryPrompt] ─── 拼装摘要模板变量
  │  map[string]any
  ▼
[SummaryPromptTemplate] ─── 摘要 Prompt 模板
  │  []*schema.Message
  ▼
[SummaryChatModel] ─── LLM 生成摘要
  │  *schema.Message
  ▼
[RecordHistory] ─── 记录推送历史
  │
  ▼
[UpdateTaggingGuide] ─── 动态更新分类体系
  │
  ▼
END → *NewsSummaryResult
```

### 关键节点说明

#### FetchRSS

从 `config/feeds.yaml` 加载 RSS 源，按质量档位排序后逐源抓取并解析。每个源最多取
`max_items_per_feed` 条，总计不超过 `max_total_items` 条（先到先得，所以顺序有意义）。
需要代理的源根据 `use_proxy` 配置自动走 `PROXY_ADDR`；瞬时失败会退避重试，4xx 不重试。

#### ParallelTag

将新闻分批（每批 15 条）并行调用 LLM 标注。每条新闻生成：display_title、category、topic_tags、region、interest_score、is_duplicate、selected 等字段。

标注通过 eino SubGraph（TagSubGraph）执行，内部流程为：TagTemplate → TagChatModel → ParseTagResult。SubGraph 提供节点级错误追踪和回调支持。批次失败时直接丢弃对应新闻，不重试。

标注结果按天缓存到 `tagged_cache_YYYYMMDD.jsonl`，下次运行时相同链接的新闻直接命中缓存，避免重复标注。

> **不变量：简报里每一条都必须能回溯到一条真实抓取到的新闻。**
> 模型返回的标注结果要与原始条目绑定（`ID` → `display_title` → `title` → `ID 哈希后缀` 四级匹配）。
> 四级全失败的结果会被**丢弃并告警**，绝不保留——否则会流出一条 source/link/title 全空、
> 只有模型生成内容的条目，等于凭空发布一条读者无法自查的「新闻」。
> `BuildDigest` 还会再过滤一次 `Source` 为空的条目作为第二道防线。
> （该漏洞 2026-09-14、09-15 各发生一次，回归用例见 `pipeline/tag/parse_test.go`。）

#### MergeHistory

加载最近 `max_news_age_days + 1` 天的推送历史（`push_history_YYYYMMDD.jsonl`），四级去重：

| 优先级 | 策略 | 说明 |
|--------|------|------|
| 1 | 链接精确匹配 | 同 URL 必定重复 |
| 2 | 标题精确匹配 | display_title 完全一致 |
| 3 | 向量筛查 + LLM 核查 | embedding 相似度 >= 阈值时，由 LLM 判断是否同一事件 |
| 4 | 回退 | LLM 不可用时信任向量相似度 |

命中任一级的条目标记为 `SeenBefore=true`，**不进入简报**——代码中没有"追踪更新"通道。

> **去重窗口必须 ≥ 时效窗口。** 窗口如果比 `max_news_age_days` 窄，一条推送满 N 天的新闻会
> 出现「仍然够新、能被选中」但「已经掉出历史、去重看不见」的状态，于是被当成新条目重复推送。
> 历史窗口固定为时效窗口 +1 天正是为了避免这种错位。

#### BuildDigest

对未被历史去重命中的候选新闻进行编排：

1. 按 link/display_title 精确去重，保留最优源
2. 同分类内做向量聚类去重（union-find），保留最优源
3. 按 CategoryOrder 排序（战争与地缘 > 航空航天 > ... > 其他重要动态）
4. 每分类最多 `max_per_category` 条，总计最多 `max_digest_items` 条

#### RecordHistory

将本次入选的新闻追加到 `push_history_YYYYMMDD.jsonl`，记录 push_time、display_title、category、link、fact_summary、embedding 等字段，供下次去重使用。按天切割文件，加载窗口见 MergeHistory。

#### UpdateTaggingGuide

分析本次标注的新闻分类和 topic_tags 分布，调用 LLM 判断是否需要新增/删除/拆分/合并分类。LLM 输出 JSON 格式的调整方案，直接修改 `config/categories.json`，无需人工审核。此节点为非关键路径，失败不影响简报输出。

### 数据结构

```go
// 核心数据流
RawNewsItem           // RSS 抓取结果
  → TaggedNewsItem    // + LLM 标注（category, display_title, interest_score 等）
    → MergedNewsItem  // + 推送历史去重（SeenBefore, ShouldPush）
      → DigestItem    // + 事实段落（FactParagraph）
        → NewsSummaryResult  // 最终摘要文本 + 统计

// 历史记录
PushHistoryRecord     // push_time, display_title, category, link, fact_summary, embedding
```

### 文件清理

程序每次运行前自动清理 `data/` 目录中超过 `file_expiry_days` 天的文件（基于文件修改时间），仅清理以下模式：

- `push_history_*.jsonl`
- `tagged_cache_*.jsonl`
- `digest_*.md`

非匹配文件不会被删除。清理操作有详细日志，错误仅记录不中断运行。

---

## 定时运行与 QQ 推送

`scripts/news_push_cron.sh` 把「跑管线 → 生成要点 → 推 QQ」串成一条链路，
由 launchd (`com.litou.news-summary-push`) 在 00:00 / 06:00 / 12:00 / 18:00 调用。

推送内容分两条：**先发《要点》文本，再把简报原文 `.md` 作为文件附件发出**。

```
news_push_cron.sh
  ├─ ./news-summary-agent -slot manual       # 阶段一~四：产出 data/digest_*.md
  ├─ scripts/news_brief.sh <digest> <brief>  # dsh --profile headless 压缩成要点
  └─ scripts/qq_push.mjs <brief> --file <digest>
         ├─ 要点文本 → msg_type=0（超 900 字自动分块）
         └─ 原文附件 → 富媒体上传拿 file_info → msg_type=7
```

### scripts/news_brief.sh

调用 `dsh --profile headless "<task>"`（一次性任务模式：不监听端口，最终答案写 stdout 后退出）
把 digest 压成 3-5 条要点 + 一句话主线，纯文本、不带 markdown 表格。

要点生成**失败或超时不影响新闻推送**：脚本会回退为只推简报原文。

### 手动调试

```bash
# 全链路跑一遍（管线 + 要点 + 推送，约 3-6 分钟）
bash scripts/news_push_cron.sh

# 跳过管线，用现有最新 digest 验证「要点 + 附件」推送
SKIP_PIPELINE=1 bash scripts/news_push_cron.sh

# 只打印不发送
SKIP_PIPELINE=1 DRY_RUN=1 bash scripts/news_push_cron.sh

# 跳过要点生成，回退到只推原文
SKIP_PIPELINE=1 GENERATE_BRIEF=0 bash scripts/news_push_cron.sh

# 单独验证要点生成
bash scripts/news_brief.sh data/digest_20260913_131600.md /tmp/brief.md && cat /tmp/brief.md
```

### 相关环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SKIP_PIPELINE` | `0` | `1` = 不跑 Go 管线，直接推最新 digest |
| `DRY_RUN` | `0` | `1` = 只打印，不发 QQ |
| `GENERATE_BRIEF` | `1` | `0` = 跳过要点生成，只推原文 |
| `BRIEF_TIMEOUT_SECONDS` | `300` | 单次要点生成超时（`news_brief.sh`） |
| `BRIEF_PROFILE` | `headless` | 生成要点用的 dsh profile |
| `DSH_CHECKOUT` | `/Users/litou/DSH/deepseek-harness` | dsh 源码 checkout |
| `QQ_TARGET_OPENID` | 项目根 `.env` | 推送目标 openid，不写入仓库 |

### 注意

`dsh --profile headless` 必须在普通终端或 launchd 下运行。从另一个 agent 会话内部嵌套调用会被
文件沙箱挡在 `~/.dsh/profiles/<profile>/` 的写入上（`EPERM`）。

---

## 新闻网页

把 `data/digest_*.md` 渲染成一个静态新闻站点，由一个极小的 HTTP 服务对外提供。

```
news_push_cron.sh
  └─ ./news-summary-agent -mode site -data ./data -public ./public
        ├─ public/index.html          最新一期（完整内容）
        ├─ public/d/<slug>.html       每期一个页面（历史归档）
        ├─ public/style.css
        └─ public/feed.xml            Atom 订阅

./news-summary-agent -mode serve -addr 0.0.0.0:9000 -public ./public
```

### 三个运行模式

| `-mode` | 作用 | 生命周期 |
|---------|------|---------|
| `pipeline`（默认） | 跑新闻管线 | 一次性 |
| `site` | 从 `data/` 渲染静态站点到 `public/` | 一次性，由 cron 调用 |
| `serve` | 提供 `public/` 的 HTTP 服务 | 常驻（launchd） |

`site` / `serve` 在创建 ChatModel **之前**分流，所以两者都不需要任何 API key。

### 归档为什么能留住

管线会按 `file_expiry_days` 清理 `data/digest_*.md`，但归档列表是从**已生成的
`public/d/*.html`** 反推的，页面一旦生成就不再删除。因此网页的历史长度不受
`file_expiry_days` 限制。

### 手动使用

```bash
# 生成站点（不推送、不联网）
./news-summary-agent -mode site -data ./data -public ./public

# 本地起服务（前台）
./news-summary-agent -mode serve -addr 127.0.0.1:9000 -public ./public

# 用 launchd 常驻（在普通终端运行，DSH 沙箱内写不了 ~/Library/LaunchAgents）
./scripts/install-site-launchd.sh

# 验证
curl -s http://127.0.0.1:9000/healthz
```

### 站点相关配置

放在项目根 `.env`（已 gitignore）：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `NEWS_SITE_ADDR` | `0.0.0.0:9000` | 监听地址 |
| `NEWS_SITE_TOKEN` | 空 | 访问口令；空 = 完全公开。口令可用 `?t=xxx` 或 `Authorization: Bearer xxx` |
| `NEWS_SITE_BASE_URL` | 空 | 对外地址，如 `https://news.example.com/`，用于 RSS 的绝对链接 |

命令行对应 `-addr` / `-site-token` / `-base-url`；cron 里可用 `GENERATE_SITE=0` 跳过网页重建。

### 暴露面（对公网开放前请确认）

服务只把 `public/` 当作根目录，且：

- 只接受 `GET` / `HEAD`，其余方法一律 `405`
- **永不列目录**：目录下没有 `index.html` 就返回 `404`（`/d/` 也不可遍历）
- 拒绝任何以 `.` 开头的路径段（`.env`、`.git`、`.DS_Store` 都取不到）
- 路径以 `path.Clean("/"+p)` 消毒，`/../../etc/passwd` 与 `%2e%2e` 编码穿越均返回 `404`
- 响应头带 `X-Content-Type-Options` / `Referrer-Policy` / `X-Frame-Options` / CSP
- 所有内容经 `html/template` 转义，新闻正文与 LLM 输出无法注入标记
- 空口令时页面完全公开；需要限制访问就设 `NEWS_SITE_TOKEN`

> **仓库、`data/`、`.env`、`config/` 都不在 `public/` 内**，服务端也无法跳出该目录。
> 但端口一旦暴露到公网，任何人都能读到这些新闻页面——内容本身是公开新闻，
> 如果简报里出现你不希望公开的内容，请改用 `NEWS_SITE_TOKEN`。

---

## DDNS（让域名跟上公网 IP）

`-mode serve` 会顺带把域名保活：`ddns` 包定时对比本机公网 IP 与 GoDaddy 上的 DNS
记录，不一致就改写记录。移植自 `~/.openclaw/workspace-main/video-server/pkg/ddns`，
但修掉了三个会安静失效的问题。

```
serve 进程
  └─ 后台 goroutine（每 DDNS_CHECK_INTERVAL，默认 300s）
        ├─ 探测公网 IP（多个回显源，逐个回退）
        ├─ 读 GoDaddy 记录
        └─ 不一致 → PUT 新记录

./news-summary-agent -mode ddns     # 一次性同步，可手工执行
```

### 相对原版的改动

| 原版 | 现在 | 原因 |
|------|------|------|
| 只抓 `ip.cn` | **ip.cn 仍是首选**，另加回退源 | ip.cn 本身没问题；加回退只是为了单点故障时不至于整个 DDNS 停摆 |
| 只支持 A 记录 | A / AAAA 可选 | 本机有公网 IPv6；且 A 记录遇到纯 IPv6 响应应报错，而不是写错地址族 |
| `updateDNSRecord` 的返回值被丢弃 | 失败会传播 | 原版 PUT 失败也向调用方报「已更新」 |
| 配置文件 `config/ddns.json` | 环境变量（`.env`） | 凭据不能进 git 仓库 |

> **ip.cn 必须带浏览器 UA。** 不带 UA 时它返回的页面里没有 `_ticket`，
> 用裸 `curl https://ip.cn | grep _ticket` 去测会得出「ip.cn 已失效」的**错误**结论。
> 实现里两步请求统一使用 `browserUA`，并有
> `TestIPCNProvider_TwoStepFlowAndUserAgent` 锁住这个前提。

另外两条硬约束：

- **探测公网 IP 时绝不走代理**。走代理拿到的是代理出口地址，写进 DNS 会把域名指向代理。
  只有访问 `api.godaddy.com` 才用 `DDNS_PROXY`。
- **只接受公网单播地址**，回环 / 内网 / 链路本地一律拒绝。

### 配置

放项目根 `.env`（已 gitignore，权限建议 600）：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DDNS_ENABLED` | `0` | `1` 开启；关闭时 serve 不会启动后台检查 |
| `DDNS_DOMAIN` | — | 如 `cozyfish.site` |
| `DDNS_RECORD` | `@` | 子域名；`@` 表示根域 |
| `DDNS_RECORD_TYPE` | `A` | `A` 或 `AAAA` |
| `DDNS_API_KEY` / `DDNS_API_SECRET` | — | GoDaddy API 凭据（以 sso-key 形式发送） |
| `DDNS_TTL` | `600` | 低于 600 会被抬到 600（GoDaddy 限制） |
| `DDNS_CHECK_INTERVAL` | `300` | 秒 |
| `DDNS_PROXY` | 空 | 访问 GoDaddy API 用的代理 |

```bash
./news-summary-agent -mode ddns    # 立即同步一次并打印结果
```

> DNS 改动受 TTL 缓存影响，公共解析器最长要 `DDNS_TTL` 秒后才看得到新地址。

---

## 与原 Skill 方案的对比

| 维度 | 原 Skill (AI Agent) | 本方案 (Eino Graph) |
|------|-------------------|-------------------|
| 流程控制 | AI 自己决定执行哪一步 | 图结构固定，确定性执行 |
| 文件 I/O | AI 读写 CSV/JSONL | Go Lambda 处理，AI 不碰文件 |
| 标注输出 | AI 写 CSV（极易出错） | AI 输出 JSON + Go 校验兜底 |
| Prompt 来源 | AI 自己读 guide 文件 | ChatTemplate 硬编码注入 |
| 去重逻辑 | AI 判断重复 + 脚本辅助 | 四级程序化去重（链接→标题→向量→LLM） |
| 摘要约束 | 靠文本约束 | ChatTemplate 强约束 + 模板固定 |
| 容错 | 无 | JSON 解析 + 默认值填充 |
| 缓存 | 无 | 标注结果按天缓存，避免重复标注 |
| 文件清理 | 无 | 按配置自动清理过期文件 |
