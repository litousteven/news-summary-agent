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
max_items_per_feed: 10    # 每个 RSS 源最大抓取条目数
max_total_items: 50       # 总共最大抓取条目数
max_digest_items: 10      # 简报最大入选条目数
max_per_category: 3       # 每个分类最大条目数
cluster_threshold: 0.75   # 语义去重相似度阈值 (0.0~1.0)
file_expiry_days: 2       # data/ 目录文件过期天数
```

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

新闻会被归入以下分类（可在 `config/tagging_guide.md` 中调整）：

- 战争与地缘
- 航空航天
- 军事装备
- AI与数码
- 新能源与汽车
- 全球经济
- 其他重要动态

### 项目结构

```
.
├── config/                  # 配置文件（版本控制）
│   ├── config.yaml          # 运行参数
│   ├── feeds.yaml           # RSS 源
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
  │  []TaggedNewsItem
  ▼
[MergeHistory] ─── 与推送历史去重（链接→标题→向量→LLM）
  │  []MergedNewsItem
  ▼
[BuildDigest] ─── 分类/排序/限额 + 批内语义去重
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

从 `config/feeds.yaml` 加载 RSS 源，逐源抓取并解析。每个源最多取 `max_items_per_feed` 条，总计不超过 `max_total_items` 条。需要代理的源根据 `use_proxy` 配置自动走 `PROXY_ADDR`。

#### ParallelTag

将新闻分批（每批 15 条）并行调用 LLM 标注。每条新闻生成：display_title、category、topic_tags、region、interest_score、is_duplicate、selected 等字段。

标注结果按天缓存到 `tagged_cache_YYYYMMDD.jsonl`，下次运行时相同链接的新闻直接命中缓存，避免重复标注。

#### MergeHistory

加载当天和前一天的推送历史，四级去重：

| 优先级 | 策略 | 说明 |
|--------|------|------|
| 1 | 链接精确匹配 | 同 URL 必定重复 |
| 2 | 标题精确匹配 | display_title 完全一致 |
| 3 | 向量筛查 + LLM 核查 | embedding 相似度 >= 阈值时，由 LLM 判断是否同一事件 |
| 4 | 回退 | LLM 不可用时信任向量相似度 |

已推送但 interest_score >= 8 的新闻仍会以"追踪更新"形式入选。

#### BuildDigest

对 `ShouldPush=true` 的新闻进行编排：

1. 按 link/display_title 精确去重，保留最优源
2. 同分类内做向量聚类去重（union-find），保留最优源
3. 按 CategoryOrder 排序（战争与地缘 > 航空航天 > ... > 其他重要动态）
4. 每分类最多 `max_per_category` 条，总计最多 `max_digest_items` 条

#### RecordHistory

将本次入选的新闻追加到 `push_history_YYYYMMDD.jsonl`，记录 push_time、display_title、category、link、fact_summary、embedding 等字段，供下次去重使用。按天切割文件，只加载当天+前一天。

#### UpdateTaggingGuide

分析本次标注的新闻分类和 topic_tags 分布，调用 LLM 判断是否需要新增/拆分/合并分类。建议以"动态调整记录"追加到 `tagging_guide.md` 末尾，供人工审核。此节点为非关键路径，失败不影响简报输出。

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
