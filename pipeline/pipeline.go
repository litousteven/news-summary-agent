package pipeline

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"gopkg.in/yaml.v3"
)

const (
	NodeFetchRSS            = "FetchRSS"
	NodeParallelTag         = "ParallelTag"
	NodeMergeHistory        = "MergeHistory"
	NodeBuildDigest         = "BuildDigest"
	NodeTranslateItems      = "TranslateItems"
	NodeFormatSummaryPrompt = "FormatSummaryPrompt"
	NodeSummaryTemplate     = "SummaryPromptTemplate"
	NodeSummaryChatModel    = "SummaryChatModel"
	NodeRecordHistory       = "RecordHistory"
	NodeUpdateTaggingGuide  = "UpdateTaggingGuide"
)

// PipelineConfig holds configurable limits loaded from config.yaml.
// Zero values fall back to defaults defined in types.go.
type PipelineConfig struct {
	MaxItemsPerFeed  int     `yaml:"max_items_per_feed"`
	MaxTotalItems    int     `yaml:"max_total_items"`
	MaxDigestItems   int     `yaml:"max_digest_items"`
	MaxPerCategory   int     `yaml:"max_per_category"`
	ClusterThreshold float64 `yaml:"cluster_threshold"`
	FileExpiryDays   int     `yaml:"file_expiry_days"`

	// 标注批次相关参数（一般无需调整，除非标注任务频繁失败）
	TagBatchSize             int `yaml:"tag_batch_size"`
	TagMaxConcurrentBatches  int `yaml:"tag_max_concurrent_batches"`
	TagMaxRetries            int `yaml:"tag_max_retries"`
	TagRetryBaseDelaySeconds int `yaml:"tag_retry_base_delay_seconds"`
	TagBatchTimeoutSeconds   int `yaml:"tag_batch_timeout_seconds"`
}

// LoadConfig reads config.yaml from the given config directory.
// Returns a zero-value config if the file doesn't exist (defaults will apply).
func LoadConfig(configDir string) PipelineConfig {
	var cfg PipelineConfig
	path := filepath.Join(configDir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[LoadConfig] 读取 %s 失败: %v，使用默认值", path, err)
		}
		return cfg
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("[LoadConfig] 解析 %s 失败: %v，使用默认值", path, err)
		return cfg
	}
	log.Printf("[LoadConfig] 从 %s 加载配置: %+v", path, cfg)
	return cfg
}

// NewsPipeline holds dependencies and builds the Eino Graph.
type NewsPipeline struct {
	ChatModel    model.BaseChatModel // shared by Summary, MergeHistory, TranslateItems stages
	TagChatModel model.BaseChatModel // tagging stage only (JSON forced mode), falls back to ChatModel
	Embedding    EmbeddingClient     // OpenAI-compatible embedding for semantic dedup
	EmbedCache   *EmbeddingCache     // per-day persistent cache for embedding vectors
	ConfigDir    string              // path to config/ directory (config.yaml, feeds.yaml, tagging_guide.md, etc.)
	DataDir      string              // path to data/ directory (runtime output: push_history, tagged_cache, digest)
	ProxyAddr    string              // HTTP proxy for RSS feeds
	Config       PipelineConfig      // configurable limits (loaded from config.yaml)
}

// Getters with defaults
func (p *NewsPipeline) GetMaxItemsPerFeed() int {
	if p.Config.MaxItemsPerFeed <= 0 {
		return DefaultMaxItemsPerFeed
	}
	return p.Config.MaxItemsPerFeed
}

func (p *NewsPipeline) GetMaxTotalItems() int {
	if p.Config.MaxTotalItems <= 0 {
		return DefaultMaxTotalItems
	}
	return p.Config.MaxTotalItems
}

func (p *NewsPipeline) GetMaxDigestItems() int {
	if p.Config.MaxDigestItems <= 0 {
		return DefaultMaxDigestItems
	}
	return p.Config.MaxDigestItems
}

func (p *NewsPipeline) GetMaxPerCategory() int {
	if p.Config.MaxPerCategory <= 0 {
		return DefaultMaxPerCategory
	}
	return p.Config.MaxPerCategory
}

func (p *NewsPipeline) GetClusterThreshold() float64 {
	if p.Config.ClusterThreshold <= 0 {
		return DefaultClusterThreshold
	}
	return p.Config.ClusterThreshold
}

func (p *NewsPipeline) GetFileExpiryDays() int {
	if p.Config.FileExpiryDays <= 0 {
		return DefaultFileExpiryDays
	}
	return p.Config.FileExpiryDays
}

func (p *NewsPipeline) GetTagBatchSize() int {
	if p.Config.TagBatchSize <= 0 {
		return DefaultTagBatchSize
	}
	return p.Config.TagBatchSize
}

func (p *NewsPipeline) GetTagMaxConcurrentBatches() int {
	if p.Config.TagMaxConcurrentBatches <= 0 {
		return DefaultTagMaxConcurrentBatches
	}
	return p.Config.TagMaxConcurrentBatches
}

func (p *NewsPipeline) GetTagMaxRetries() int {
	if p.Config.TagMaxRetries <= 0 {
		return DefaultTagMaxRetries
	}
	return p.Config.TagMaxRetries
}

func (p *NewsPipeline) GetTagRetryBaseDelaySeconds() int {
	if p.Config.TagRetryBaseDelaySeconds <= 0 {
		return DefaultTagRetryBaseDelaySeconds
	}
	return p.Config.TagRetryBaseDelaySeconds
}

func (p *NewsPipeline) GetTagBatchTimeoutSeconds() int {
	if p.Config.TagBatchTimeoutSeconds <= 0 {
		return DefaultTagBatchTimeoutSeconds
	}
	return p.Config.TagBatchTimeoutSeconds
}

// EmbeddingClient is the interface for OpenAI-compatible embedding APIs.
type EmbeddingClient interface {
	// EmbedStrings returns embedding vectors for the given texts.
	EmbedStrings(ctx context.Context, texts []string) ([][]float64, error)
}

// BuildGraph constructs the 11-node Eino Graph as documented in README.
func (p *NewsPipeline) BuildGraph(ctx context.Context) (compose.Runnable[*NewsSummaryRequest, *NewsSummaryResult], error) {
	g := compose.NewGraph[*NewsSummaryRequest, *NewsSummaryResult](
		compose.WithGenLocalState(func(ctx context.Context) *PipelineState {
			return &PipelineState{}
		}),
	)

	// 1. FetchRSS — Lambda
	// PostHandler: save RawItems + Slot into shared state for downstream nodes
	if err := g.AddLambdaNode(NodeFetchRSS,
		compose.InvokableLambda(p.fetchRSS),
		compose.WithNodeName("抓取RSS新闻"),
		compose.WithStatePostHandler(func(ctx context.Context, out []RawNewsItem, state *PipelineState) ([]RawNewsItem, error) {
			state.RawItems = out
			return out, nil
		}),
	); err != nil {
		return nil, err
	}

	// 2. ParallelTag — Lambda (replaces FormatTagPrompt + TagTemplate + ChatModel + ParseTagResult)
	if err := g.AddLambdaNode(NodeParallelTag,
		compose.InvokableLambda(p.parallelTagItems),
		compose.WithNodeName("分批并行标注"),
	); err != nil {
		return nil, err
	}

	// 3. MergeHistory — Lambda (uses Embedding for semantic dedup)
	if err := g.AddLambdaNode(NodeMergeHistory,
		compose.InvokableLambda(p.mergeHistory),
		compose.WithNodeName("合并推送历史"),
	); err != nil {
		return nil, err
	}

	// 7. BuildDigest — Lambda
	// PostHandler: save DigestItems into shared state for RecordHistory
	if err := g.AddLambdaNode(NodeBuildDigest,
		compose.InvokableLambda(p.buildDigest),
		compose.WithNodeName("编排摘要"),
		compose.WithStatePostHandler(func(ctx context.Context, out *DigestData, state *PipelineState) (*DigestData, error) {
			state.DigestItems = out.Items
			state.DigestStats = &out.Stats
			return out, nil
		}),
	); err != nil {
		return nil, err
	}

	// 8. TranslateItems — Lambda
	// PostHandler: update DigestItems in shared state with translated data
	if err := g.AddLambdaNode(NodeTranslateItems,
		compose.InvokableLambda(p.TranslateItems),
		compose.WithNodeName("翻译外语新闻"),
		compose.WithStatePostHandler(func(ctx context.Context, out *DigestData, state *PipelineState) (*DigestData, error) {
			state.DigestItems = out.Items
			return out, nil
		}),
	); err != nil {
		return nil, err
	}

	// 9. FormatSummaryPrompt — Lambda
	if err := g.AddLambdaNode(NodeFormatSummaryPrompt,
		compose.InvokableLambda(p.formatSummaryPrompt),
		compose.WithNodeName("拼装摘要Prompt变量"),
	); err != nil {
		return nil, err
	}

	// 9. SummaryPromptTemplate — ChatTemplate
	summaryTpl, err := p.newSummaryChatTemplate()
	if err != nil {
		return nil, err
	}
	if err := g.AddChatTemplateNode(NodeSummaryTemplate, summaryTpl,
		compose.WithNodeName("摘要Prompt模板"),
	); err != nil {
		return nil, err
	}

	// 10. SummaryChatModel — same ChatModel instance for Summary stage
	if err := g.AddChatModelNode(NodeSummaryChatModel, p.ChatModel,
		compose.WithNodeName("摘要LLM"),
	); err != nil {
		return nil, err
	}

	// 11. RecordHistory — Lambda
	// PreHandler: inject Slot into state from request (already done via FetchRSS)
	// The lambda uses compose.ProcessState internally to access DigestItems
	if err := g.AddLambdaNode(NodeRecordHistory,
		compose.InvokableLambda(p.recordHistory),
		compose.WithNodeName("记录推送历史"),
	); err != nil {
		return nil, err
	}

	// 12. UpdateTaggingGuide — Lambda (analyzes tagged news, suggests category updates)
	if err := g.AddLambdaNode(NodeUpdateTaggingGuide,
		compose.InvokableLambda(p.updateTaggingGuide),
		compose.WithNodeName("更新分类体系"),
	); err != nil {
		return nil, err
	}

	// Edges: linear pipeline
	edges := [][2]string{
		{compose.START, NodeFetchRSS},
		{NodeFetchRSS, NodeParallelTag},
		{NodeParallelTag, NodeMergeHistory},
		{NodeMergeHistory, NodeBuildDigest},
		{NodeBuildDigest, NodeTranslateItems},
		{NodeTranslateItems, NodeFormatSummaryPrompt},
		{NodeFormatSummaryPrompt, NodeSummaryTemplate},
		{NodeSummaryTemplate, NodeSummaryChatModel},
		{NodeSummaryChatModel, NodeRecordHistory},
		{NodeRecordHistory, NodeUpdateTaggingGuide},
		{NodeUpdateTaggingGuide, compose.END},
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return nil, err
		}
	}

	// Compile
	r, err := g.Compile(ctx, compose.WithGraphName("NewsSummary"))
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Run is a convenience method to build and execute the pipeline.
func (p *NewsPipeline) Run(ctx context.Context, req *NewsSummaryRequest) (*NewsSummaryResult, error) {
	r, err := p.BuildGraph(ctx)
	if err != nil {
		return nil, err
	}
	return r.Invoke(ctx, req)
}
