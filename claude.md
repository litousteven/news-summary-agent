# 项目结构

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
│   ├── pipeline.go         # 管道构建和配置加载
│   ├── types.go            # 核心数据结构和常量
│   ├── templates.go        # Prompt 模板定义
│   ├── fetch_rss.go        # RSS 抓取
│   ├── parallel_tag.go     # 并行标注（批次控制、重试、超时）
│   ├── merge_history.go   # 推送历史合并与去重
│   ├── build_digest.go     # 简报编排与统计
│   ├── translate_items.go  # 外语新闻翻译
│   ├── format_summary_prompt.go  # 摘要 Prompt 拼装
│   ├── record_history.go   # 推送历史记录
│   ├── update_tagging_guide.go  # 分类体系自动更新
│   ├── cleanup.go          # 过期文件清理
│   ├── dedup.go            # 语义去重（embedding）
│   ├── embedding.go        # embedding API 调用封装
│   ├── embedding_cache.go  # embedding 向量缓存
│   ├── tag_subgraph.go     # 标注子图（FormatTagPrompt → TagTemplate → ChatModel → ParseTagResult）
│   ├── parse_tag_result.go # LLM 输出解析
│   ├── format_tag_prompt.go # 标注 Prompt 格式化（guide/examples 加载）
│   └── util/               # 公共工具方法
└── data/                   # 运行时数据（gitignore）
    ├── push_history_*.jsonl
    ├── tagged_cache_*.jsonl
    ├── embedding_cache_*.jsonl
    └── digest_*.md
```

## pipeline/util 目录

公共工具方法统一放置于 `pipeline/util/` 目录下，避免重复代码和循环依赖。
