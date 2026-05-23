package pipeline

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

type tagSubGraphInput = map[string]any

func (p *NewsPipeline) buildTagSubGraph(ctx context.Context) (compose.Runnable[map[string]any, []types.TaggedNewsItem], error) {
	g := compose.NewGraph[map[string]any, []types.TaggedNewsItem](
		compose.WithGenLocalState(func(ctx context.Context) *tagSubGraphState {
			return &tagSubGraphState{}
		}),
	)

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
		compose.InvokableLambda(p.parseTagResultFromMessage),
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

type tagSubGraphState struct{}

func (p *NewsPipeline) parseTagResultFromMessage(ctx context.Context, msg *schema.Message) ([]types.TaggedNewsItem, error) {
	rawByID := make(map[string]types.RawNewsItem)
	_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
		for _, raw := range state.RawItems {
			rawByID[raw.ID] = raw
		}
		return nil
	})

	return parseTagResultFromMessage(msg, rawByID)
}
