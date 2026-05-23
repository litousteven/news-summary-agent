# 项目结构

## Pipeline 总览（按执行顺序）

整个管线是一条 Eino Graph 线性链，共 7 个节点，分 4 个阶段。节点之间通过 `compose.ProcessState` 共享 `PipelineState`。

```
START
  │
  │  阶段一：采集与标注
  │
  ├─ 1. FetchRSS              ── 抓取所有启用的 RSS 源，写入 state.RawItems
  ├─ 2. ParallelTag           ── 分批并行 LLM 标注（今日已缓存项直接复用）
  │      内部子流程：formatTagPrompt → TagChatTemplate → TagChatModel → parseTagResult
  │      输出：[]TaggedNewsItem（含 category、topic_tags、interest_score 等）
  │
  │  阶段二：历史去重与编排
  │
  ├─ 3. MergeHistory          ── 与历史推送对比（embedding 语义相似度），标注 seen_before/进展/反转
  ├─ 4. BuildDigest           ── 按分类选稿、排序、去重，生成 DigestData；写入 state.DigestItems
  │
  │  阶段三：翻译与摘要生成（均为逐条并发 + JSON 格式）
  │
  ├─ 5. TranslateItems        ── 逐条并发将 en 新闻标题+摘要翻译为 zh，更新 state.DigestItems
  ├─ 6. SummarizePerItem      ── 逐条并发为每条新闻生成摘要（含 refs 上下文），JSON 输出写入 DigestItem.ItemSummary
  │
  │  阶段四：持久化与后处理
  │
  ├─ 7. RecordHistory         ── 拼装最终简报文本 + 写入 push_history_*.jsonl（含 embedding 向量）
  ├─ 8. UpdateTaggingGuide    ── 分析标注结果，自动建议 categories.json 更新
  END
```

### 核心数据流

| 阶段 | 输入 → 输出 | 关键文件 |
|------|------------|---------|
| 采集 | `NewsSummaryRequest` → `[]RawNewsItem` | fetch_rss.go |
| 标注 | `[]RawNewsItem` → `[]TaggedNewsItem` | parallel_tag.go, tag_subgraph.go, parse_tag_result.go |
| 去重 | `[]TaggedNewsItem` → `[]MergedNewsItem` | merge_history.go, dedup.go, embedding.go |
| 编排 | `[]MergedNewsItem` → `*DigestData` | build_digest.go |
| 翻译 | `*DigestData` → `*DigestData`（原地修改 DisplayTitle/Summary） | translate_items.go |
| 摘要 | `*DigestData` → `*DigestData`（原地写入 ItemSummary） | format_summary_prompt.go |
| 持久化 | `*DigestData` → `*NewsSummaryResult`（拼装最终 Message + 写历史） | record_history.go |
| 分类更新 | 分析标注质量，更新 `categories.json` | update_tagging_guide.go |

### ParallelTag 内部子图（节点 2 的子流程）

节点 2 内部是一个 Eino 子图（tag_subgraph.go），4 步串联：
```
FormatTagPrompt → TagChatTemplate → TagChatModel → ParseTagResult
```
- **缓存机制**：同一天同一 URL 只标注一次，存入 `tagged_cache_YYYYMMDD.jsonl`
- **批次并发**：每批 N 条（可配置），最多 M 个并发批次，单批超时+重试
- **独立 ChatModel**：标注阶段使用 `TagChatModel`（强制 JSON mode），与后续摘要/翻译的 `ChatModel` 隔离

### 翻译节点（节点 5）

- 逐条并发翻译（semaphore=3），单条失败不影响其他条
- 强制 JSON 格式返回 `{"title": "...", "summary": "..."}`
- 兼容容错：直接 JSON → markdown 代码块提取 → 花括号对象提取

### 摘要节点（节点 6）

- 逐条并发生成摘要（semaphore=3），单条失败不影响其他条
- 每条新闻自带其 `Refs`（相关参考）作为上下文，LLM 在摘要中关联前情/反转
- 强制 JSON 格式返回 `{"summary": "一段话的新闻摘要"}`
- 兼容容错：直接 JSON → markdown 代码块提取 → 花括号对象提取
- 结果写入 `DigestItem.ItemSummary`，失败时 `ItemSummary` 为空

### 最终简报拼装（节点 7 RecordHistory 内部）

`recordHistory` 接受 `*DigestData`，内部调用 `buildFinalMessage()`：
- 按 `CategoryOrder` 排序分类
- 每个分类下列出其下所有新闻的 `ItemSummary`（空则用 `FactParagraph`）
- 每条新闻下方缩进显示 `Refs`（相关参考）
- 最终 `NewsSummaryResult.Message` 是一个按分类组织的 markdown 文本
- 同时写入 `push_history_*.jsonl`（per-item 记录 + embedding）

---

```
news-summary-agent/
├── main.go                 # 程序入口
├── config/
│   ├── config.yaml         # 运行时配置（标注批次参数、RSS限制等）
│   ├── feeds.yaml          # RSS 源配置
│   ├── categories.json     # 分类体系定义
│   ├── tagging_guide.md    # 标注规范文档
│   └── .gitignore
├── pipeline/               # 核心业务逻辑
│   ├── pipeline.go         # 管道构建（BuildGraph 定义 7 节点+边）和配置加载
│   ├── types.go            # 核心数据结构、常量、默认分类体系
│   ├── templates.go        # 标注 ChatTemplate prompt 模板定义
│   ├── fetch_rss.go        # RSS 抓取与 source 配置加载
│   ├── parallel_tag.go     # 并行标注入口（缓存+分批+重试+超时）
│   ├── merge_history.go    # 推送历史合并、LLM 去重判定（llmVerifyDuplicatesMerged）
│   ├── build_digest.go     # 简报编排：分类选稿、排序、FactParagraph 构造
│   ├── translate_items.go  # 外语新闻逐条并发翻译（JSON 格式）
│   ├── format_summary_prompt.go  # 逐条并发摘要生成（JSON 格式，含 refs 上下文）
│   ├── record_history.go   # 拼装最终简报文本 + 推送历史记录写入（含 embedding 向量）
│   ├── update_tagging_guide.go   # 分类体系自动更新（LLM 分析 → categories.json）
│   ├── cleanup.go          # 过期文件清理（tagged_cache、embedding_cache）
│   ├── dedup.go            # 语义去重：同一批次内按 embedding 相似度去重
│   ├── embedding.go        # OpenAI-compatible embedding API 调用
│   ├── embedding_cache.go  # embedding 向量按天缓存
│   ├── tag_subgraph.go     # 标注子图构建（FormatTagPrompt → ChatTemplate → ChatModel → ParseTagResult）
│   ├── parse_tag_result.go # LLM 标注输出解析（JSON 容错：markdown/单对象/字符串兼容）
│   ├── format_tag_prompt.go # 标注 prompt 格式化（加载 guide、examples、categories）
│   └── util/               # 公共工具方法
└── data/                   # 运行时数据（gitignore）
    ├── push_history_*.jsonl
    ├── tagged_cache_*.jsonl
    ├── embedding_cache_*.jsonl
    └── digest_*.md
```

## pipeline/util 目录

公共工具方法统一放置于 `pipeline/util/` 目录下，避免重复代码和循环依赖。
