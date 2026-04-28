# News Summary Agent

基于 [CloudWeGo Eino](https://github.com/cloudwego/eino) 框架实现的国际新闻简报 Agent。

## 背景

原始版本通过 AI Skill（四段流水线）执行，脚本负责抓取/编排，AI Agent 负责理解/判断。实际运行中存在以下不稳定性：

| 问题 | 原因 |
|------|------|
| AI 直接读写文件 | CSV 解析/生成对 LLM 极不可靠（引号转义、编码、列对齐） |
| AI 决定流程走向 | Agent 自由度太大，可能跳步、改格式、加私货 |
| 非结构化输出 | LLM 输出 CSV / 自由文本，无法程序化校验 |
| 大任务单次调用 | 50 条新闻一次性标注输出完整 CSV，失败率高 |
| 跨文件状态依赖 | AI 同时操作多个文件，上下文窗口压力大 |

本方案用 Eino Graph 将流程固定为确定性管线，AI 只负责"理解"和"判断"，所有 I/O 和流程控制由 Go 代码完成。

---

## 设计原则

1. **AI 只做"理解"，不做"I/O"**：所有文件读写、CSV 解析、JSONL 操作由 Go Lambda 完成
2. **AI 只做"判断"，不做"决策"**：流程走向由图结构决定，AI 不选择执行哪一步
3. **结构化输入/输出**：LLM 输入预格式化，输出要求 JSON，Go 代码做解析和校验
4. **容错兜底**：LLM 输出不可能 100% 合法，Go 代码负责解析失败时的默认值填充

---

## Graph 流程

```
START
  |
  v
[FetchRSS] ──── Lambda (Go)
  |  []RawNewsItem
  v
[FormatTagPrompt] ──── Lambda (Go，拼模板变量)
  |  map[string]any
  v
[TagPromptTemplate] ──── ChatTemplate (Eino 组件)
  |  []*schema.Message
  v
[TagChatModel] ──── ChatModel (Eino 组件)
  |  *schema.Message (JSON)
  v
[ParseTagResult] ──── Lambda (Go，JSON 解析 + 校验)
  |  []TaggedNewsItem
  v
[MergeHistory] ──── Lambda (Go，读 push_history.jsonl)
  |  []MergedNewsItem
  v
[BuildDigest] ──── Lambda (Go，分类/排序/限额)
  |  *DigestData
  v
[FormatSummaryPrompt] ──── Lambda (Go，拼模板变量)
  |  map[string]any
  v
[SummaryPromptTemplate] ──── ChatTemplate (Eino 组件)
  |  []*schema.Message
  v
[SummaryChatModel] ──── ChatModel (Eino 组件)
  |  *schema.Message
  v
[RecordHistory] ──── Lambda (Go，写 push_history.jsonl)
  |
  v
END → *NewsSummaryResult
```

---

## 各节点设计

### 1. FetchRSS — Lambda

**组件类型**：`compose.InvokableLambda`

**职责**：拉取 5 个 RSS 源，解析为结构化数据

**输入**：`*NewsSummaryRequest`（含 slot 时间、代理配置）

**输出**：`[]RawNewsItem`

**实现要点**：
- 移植原 `fetch_feeds_to_csv.py` 的逻辑到 Go
- 用 Go 的 `net/http` + RSS 解析库
- 去掉 CSV 中转，直接输出 `[]RawNewsItem` 结构体
- 每个 item 字段：`ID, Source, Title, Summary, Link, PublishedAt, Lang, FetchedAt`

**当前 RSS 源**：
| 源 | 地址 | 语言 | 代理 |
|----|------|------|------|
| 中新网国际 | chinanews.com.cn | 中文 | 不需要 |
| BBC World | bbci.co.uk | 英文 | 需要 |
| NPR | npr.org | 英文 | 需要 |
| NYT World | nytimes.com | 英文 | 需要 |
| Al Jazeera | aljazeera.com | 英文 | 需要 |

---

### 2. FormatTagPrompt — Lambda

**组件类型**：`compose.InvokableLambda`

**职责**：将 `[]RawNewsItem` + 标注规范 + 示例转为 ChatTemplate 的模板变量

**输入**：`[]RawNewsItem`

**输出**：`map[string]any`，含以下 key：
- `news_items`：格式化后的新闻列表（编号 + 标题 + 摘要）
- `tagging_guide`：标注规范文本（来自 `tagging_guide.md`）
- `tagging_examples`：示例标注结果（来自 `tagging_examples.csv`）
- `total_count`：新闻条数

**关键设计**：把标注规范以模板变量注入 ChatTemplate，而非让 AI 自己读文件

---

### 3. TagPromptTemplate — ChatTemplate

**组件类型**：`compose.ChatTemplateNode`

**职责**：组装标注任务的 System + User Prompt

**模板变量**：`{news_items}`, `{tagging_guide}`, `{tagging_examples}`, `{total_count}`

**Prompt 核心要求**（硬编码在模板中）：
- 输出 JSON 数组，每个元素对应一条新闻
- 必填字段：`id, display_title, category, topic_tags, region, event_key, interest_score, is_duplicate, selected, why_selected`
- category 枚举值限定 7 个：战争与地缘 / 航空航天 / 军事装备 / AI与数码 / 新能源与汽车 / 全球经济 / 其他重要动态
- 同事件必须共用 event_key
- event_key 格式：`2026-04-20-us-seizes-iran-ship`

---

### 4. TagChatModel — ChatModel

**组件类型**：`compose.ChatModelNode`

**职责**：LLM 推理，输出标注结果

**输出**：`*schema.Message`（内容为 JSON 字符串）

**建议**：开启 JSON Mode（如果模型支持），强制输出合法 JSON

---

### 5. ParseTagResult — Lambda

**组件类型**：`compose.InvokableLambda`

**职责**：解析 LLM 输出的 JSON，校验字段，补全默认值

**输入**：`*schema.Message`

**输出**：`[]TaggedNewsItem`

**容错设计**：
- JSON 解析失败 → 尝试提取 markdown code block 中的 JSON
- 字段缺失 → 用默认值填充（category 默认 "其他重要动态"，interest_score 默认 6）
- category 不在枚举内 → 映射到 "其他重要动态"
- event_key 缺失 → 根据 `date-source-title` 自动生成

---

### 6. MergeHistory — Lambda

**组件类型**：`compose.InvokableLambda`

**职责**：与推送历史做去重，等价于原 `merge_with_history.py`

**输入**：`[]TaggedNewsItem`

**输出**：`[]MergedNewsItem`（增加 `SeenBefore`, `ShouldPush`, `LastPushTime`, `LastFactSummary` 字段）

**实现要点**：
- 读取 `push_history.jsonl`
- 过滤最近 24 小时内的记录
- 按 `event_key` 匹配去重
- 已推送的设 `ShouldPush=false`

---

### 7. BuildDigest — Lambda

**组件类型**：`compose.InvokableLambda`

**职责**：分类、排序、限额，等价于原 `build_digest_from_tags.py`

**输入**：`[]MergedNewsItem`

**输出**：`*DigestData`（含分类后的条目、统计信息）

**规则（硬编码在 Go 中）**：
- 只选 `ShouldPush=true`
- 同 `event_key` 只保留最优源（中新网 > BBC > NPR > NYT > Al Jazeera）
- 按分类优先级排序：战争与地缘 > 航空航天 > 军事装备 > AI与数码 > 新能源与汽车 > 全球经济 > 其他重要动态
- 每类最多 3 条，总计最多 10 条
- 为每条生成 `FactParagraph`（复用原 `build_fact_paragraph` 逻辑）

---

### 8. FormatSummaryPrompt — Lambda

**组件类型**：`compose.InvokableLambda`

**职责**：将 DigestData 转为摘要模板变量

**输入**：`*DigestData`

**输出**：`map[string]any`，含：
- `digest_content`：编排好的新闻段落
- `slot_label`：当前档位标签（午间版/晚间版/凌晨版）
- `current_time`：当前时间
- `history_items`：需要做前情提要的历史条目

---

### 9. SummaryPromptTemplate — ChatTemplate

**组件类型**：`compose.ChatTemplateNode`

**Prompt 核心约束**（硬编码在模板中）：
- 每条新闻写成一段话，包含：主体、地点、时间、事件、关键原话/数字
- 禁止加入 agent 分析、判断、风险点评
- 禁止"局势升级""持续发酵"等空泛概括
- seen_before 的条目默认不重复，除非明确要求前情提要
- 前情提要只能引用历史记录中的事实，禁止新增分析性衔接语

---

### 10. SummaryChatModel — ChatModel

**组件类型**：`compose.ChatModelNode`

**输出**：`*schema.Message`（最终消息文本）

---

### 11. RecordHistory — Lambda

**组件类型**：`compose.InvokableLambda`

**职责**：将本次推送条目追加到 `push_history.jsonl`

**输入**：`*schema.Message`（摘要结果）

**输出**：`*NewsSummaryResult`

**记录字段**：`push_time, slot, event_key, display_title, category, source, published_at, link, fact_summary, raw_title`

---

## 核心数据结构

```go
// 请求输入
type NewsSummaryRequest struct {
    Slot      string // "00:00" / "12:00" / "18:00"
    ProxyAddr string // 代理地址，如 "http://127.0.0.1:7890"
}

// RSS 抓取结果
type RawNewsItem struct {
    ID          string
    Source      string // "中新网" / "BBC" / "NPR" / "NYT" / "Al Jazeera"
    Title       string
    Summary     string
    Link        string
    PublishedAt string
    Lang        string // "zh" / "en"
    FetchedAt   string
}

// LLM 标注结果
type TaggedNewsItem struct {
    RawNewsItem
    DisplayTitle  string
    Category      string   // 7 个枚举之一
    TopicTags     []string
    Region        string
    EventKey      string
    InterestScore int      // 0-10
    IsDuplicate   bool
    Selected      bool
    WhySelected   string
}

// 合并推送历史后
type MergedNewsItem struct {
    TaggedNewsItem
    SeenBefore      bool
    ShouldPush      bool
    LastPushTime    string
    LastFactSummary string
    HistoryNote     string
}

// 编排后的摘要数据
type DigestData struct {
    Items       []MergedNewsItem // 按分类排序后的入选条目
    SlotLabel   string           // "午间版" / "晚间版" / "凌晨版"
    CurrentTime string
    Stats       DigestStats
}

type DigestStats struct {
    TotalFetched int
    TotalTagged  int
    TotalSelected int
    ByCategory   map[string]int
}

// 最终结果
type NewsSummaryResult struct {
    Message string        // 最终 QQ 消息文本
    Stats   DigestStats
}
```

---

## 组件清单

| 组件类型 | 使用次数 | 具体节点 |
|---------|---------|---------|
| **Lambda** | 7 | FetchRSS, FormatTagPrompt, ParseTagResult, MergeHistory, BuildDigest, FormatSummaryPrompt, RecordHistory |
| **ChatTemplate** | 2 | TagPromptTemplate, SummaryPromptTemplate |
| **ChatModel** | 2 | TagChatModel, SummaryChatModel |

---

## 与原 Skill 的对比

| 维度 | 原 Skill (AI Agent) | Eino Flow (本方案) |
|------|-------------------|-------------------|
| 流程控制 | AI 自己决定执行哪一步 | 图结构固定，确定性执行 |
| 文件 I/O | AI 读写 CSV/JSONL | Go Lambda 处理，AI 不碰文件 |
| 标注输出 | AI 写 CSV（极易出错） | AI 输出 JSON + Go 校验兜底 |
| Prompt 来源 | AI 自己读 guide 文件 | ChatTemplate 硬编码注入 |
| 去重逻辑 | AI 判断重复 + 脚本辅助 | 完全由 Go 程序化处理 |
| 摘要约束 | 靠 SKILL.md 文本约束 | ChatTemplate 强约束 + 模板固定 |
| 容错 | 无 | ParseTagResult 层做 JSON 解析 + 默认值填充 |
| 可观测性 | 只有日志文件 | Eino Graph 每个节点可追踪 |

---

## 后续迭代方向

1. **标注分批**：50 条新闻上下文太长时，拆成多批独立标注，再用 Lambda 做 event_key 对齐
2. **ReAct Agent 增强**：在 BuildDigest 之后可选接入 ReAct Agent，根据新闻内容主动搜索更多细节
3. **向量语义去重**：push_history 积累较长后，用 Redis Vector Store 做语义去重替代精确 event_key 匹配
4. **重试机制**：对 TagChatModel 节点加 JSON 解析失败时的重试逻辑
5. **定时调度**：对接 cron 或消息队列，自动触发 00:00 / 12:00 / 18:00 三个档位
6. **多渠道输出**：除 QQ 外支持微信、钉钉、邮件等推送渠道
