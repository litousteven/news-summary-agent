package pipeline

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	tagpkg "github.com/litousteven/news-summary-agent/pipeline/tag"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func (p *NewsPipeline) buildTagSubGraph(ctx context.Context) (compose.Runnable[map[string]any, []types.TaggedNewsItem], error) {
	g := compose.NewGraph[map[string]any, []types.TaggedNewsItem]()

	tagTpl, err := p.newTagChatTemplate()
	if err != nil {
		return nil, fmt.Errorf("create tag template: %w", err)
	}
	if err := g.AddChatTemplateNode("TagTemplate", tagTpl,
		compose.WithNodeName("标注Prompt模板"),
	); err != nil {
		return nil, err
	}

	tagCM := p.TagChatModel
	if tagCM == nil {
		tagCM = p.ChatModel
	}
	if err := g.AddChatModelNode("TagChatModel", tagCM,
		compose.WithNodeName("标注LLM"),
	); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode("ParseTagResult",
		compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) ([]types.TaggedNewsItem, error) {
			var rawItems []types.RawNewsItem
			_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
				rawItems = state.RawItems
				return nil
			})
			return tagpkg.ParseTagResultFromMessage(msg.Content, rawItems)
		}),
		compose.WithNodeName("解析标注结果"),
	); err != nil {
		return nil, err
	}

	edges := [][2]string{
		{compose.START, "TagTemplate"},
		{"TagTemplate", "TagChatModel"},
		{"TagChatModel", "ParseTagResult"},
		{"ParseTagResult", compose.END},
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return nil, err
		}
	}

	r, err := g.Compile(ctx, compose.WithGraphName("TagSubGraph"))
	if err != nil {
		return nil, fmt.Errorf("compile tag sub-graph: %w", err)
	}
	return r, nil
}
