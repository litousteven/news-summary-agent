package pipeline

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	NodeFetchRSS            = "FetchRSS"
	NodeFormatTagPrompt     = "FormatTagPrompt"
	NodeTagPromptTemplate   = "TagPromptTemplate"
	NodeChatModel           = "ChatModel"
	NodeParseTagResult      = "ParseTagResult"
	NodeMergeHistory        = "MergeHistory"
	NodeBuildDigest         = "BuildDigest"
	NodeFormatSummaryPrompt = "FormatSummaryPrompt"
	NodeSummaryTemplate     = "SummaryPromptTemplate"
	NodeSummaryChatModel    = "SummaryChatModel"
	NodeRecordHistory       = "RecordHistory"
)

// NewsPipeline holds dependencies and builds the Eino Graph.
type NewsPipeline struct {
	ChatModel model.BaseChatModel // shared by Tag and Summary stages
	Embedding EmbeddingClient     // OpenAI-compatible embedding for semantic dedup
	DataDir   string              // path to data/ directory
	ProxyAddr string              // HTTP proxy for RSS feeds
}

// EmbeddingClient is the interface for OpenAI-compatible embedding APIs.
type EmbeddingClient interface {
	// EmbedStrings returns embedding vectors for the given texts.
	EmbedStrings(ctx context.Context, texts []string) ([][]float64, error)
}

// BuildGraph constructs the 11-node Eino Graph as documented in README.
func (p *NewsPipeline) BuildGraph(ctx context.Context) (compose.Runnable[*NewsSummaryRequest, *NewsSummaryResult], error) {
	g := compose.NewGraph[*NewsSummaryRequest, *NewsSummaryResult]()

	// 1. FetchRSS — Lambda
	if err := g.AddLambdaNode(NodeFetchRSS,
		compose.InvokableLambda(p.fetchRSS),
		compose.WithNodeName("抓取RSS新闻"),
	); err != nil {
		return nil, err
	}

	// 2. FormatTagPrompt — Lambda
	if err := g.AddLambdaNode(NodeFormatTagPrompt,
		compose.InvokableLambda(p.formatTagPrompt),
		compose.WithNodeName("拼装标注Prompt变量"),
	); err != nil {
		return nil, err
	}

	// 3. TagPromptTemplate — ChatTemplate
	tagTpl, err := p.newTagChatTemplate()
	if err != nil {
		return nil, err
	}
	if err := g.AddChatTemplateNode(NodeTagPromptTemplate, tagTpl,
		compose.WithNodeName("标注Prompt模板"),
	); err != nil {
		return nil, err
	}

	// 4. ChatModel — shared ChatModel for Tag stage
	if err := g.AddChatModelNode(NodeChatModel, p.ChatModel,
		compose.WithNodeName("标注LLM"),
	); err != nil {
		return nil, err
	}

	// 5. ParseTagResult — Lambda
	if err := g.AddLambdaNode(NodeParseTagResult,
		compose.InvokableLambda(p.parseTagResult),
		compose.WithNodeName("解析标注JSON"),
	); err != nil {
		return nil, err
	}

	// 6. MergeHistory — Lambda (uses Embedding for semantic dedup)
	if err := g.AddLambdaNode(NodeMergeHistory,
		compose.InvokableLambda(p.mergeHistory),
		compose.WithNodeName("合并推送历史"),
	); err != nil {
		return nil, err
	}

	// 7. BuildDigest — Lambda
	if err := g.AddLambdaNode(NodeBuildDigest,
		compose.InvokableLambda(p.buildDigest),
		compose.WithNodeName("编排摘要"),
	); err != nil {
		return nil, err
	}

	// 8. FormatSummaryPrompt — Lambda
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
	if err := g.AddLambdaNode(NodeRecordHistory,
		compose.InvokableLambda(p.recordHistory),
		compose.WithNodeName("记录推送历史"),
	); err != nil {
		return nil, err
	}

	// Edges: linear pipeline
	edges := [][2]string{
		{compose.START, NodeFetchRSS},
		{NodeFetchRSS, NodeFormatTagPrompt},
		{NodeFormatTagPrompt, NodeTagPromptTemplate},
		{NodeTagPromptTemplate, NodeChatModel},
		{NodeChatModel, NodeParseTagResult},
		{NodeParseTagResult, NodeMergeHistory},
		{NodeMergeHistory, NodeBuildDigest},
		{NodeBuildDigest, NodeFormatSummaryPrompt},
		{NodeFormatSummaryPrompt, NodeSummaryTemplate},
		{NodeSummaryTemplate, NodeSummaryChatModel},
		{NodeSummaryChatModel, NodeRecordHistory},
		{NodeRecordHistory, compose.END},
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

// compile-time interface check
var _ model.BaseChatModel = (*newsPipelineChatModel)(nil)

type newsPipelineChatModel struct{}

func (n *newsPipelineChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return nil, nil
}
func (n *newsPipelineChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}
