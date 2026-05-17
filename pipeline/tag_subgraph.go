package pipeline

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// tagSubGraphInput is the input to the tag sub-graph.
// It carries both the template variables and the raw item index for result merging.
type tagSubGraphInput = map[string]any

// buildTagSubGraph creates a compiled eino sub-graph for the tagging stage:
//
//	FormatTagPrompt → TagTemplate → TagChatModel → ParseTagResult
//
// Input:  map[string]any  (template variables: news_items, categories, tagging_guide, tagging_examples, total_count)
// Output: []TaggedNewsItem
func (p *NewsPipeline) buildTagSubGraph(ctx context.Context) (compose.Runnable[map[string]any, []TaggedNewsItem], error) {
	g := compose.NewGraph[map[string]any, []TaggedNewsItem](
		compose.WithGenLocalState(func(ctx context.Context) *tagSubGraphState {
			return &tagSubGraphState{}
		}),
	)

	// Node: TagPromptTemplate
	tagTpl, err := p.newTagChatTemplate()
	if err != nil {
		return nil, fmt.Errorf("create tag template: %w", err)
	}
	if err := g.AddChatTemplateNode("TagTemplate", tagTpl,
		compose.WithNodeName("标注Prompt模板"),
	); err != nil {
		return nil, err
	}

	// Node: TagChatModel (use TagChatModel if set, otherwise fallback to ChatModel)
	tagCM := p.TagChatModel
	if tagCM == nil {
		tagCM = p.ChatModel
	}
	if err := g.AddChatModelNode("TagChatModel", tagCM,
		compose.WithNodeName("标注LLM"),
	); err != nil {
		return nil, err
	}

	// Node: ParseTagResult — Lambda
	if err := g.AddLambdaNode("ParseTagResult",
		compose.InvokableLambda(p.parseTagResultFromMessage),
		compose.WithNodeName("解析标注结果"),
	); err != nil {
		return nil, err
	}

	// Edges
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

// tagSubGraphState holds state for the tag sub-graph (reserved for future use).
type tagSubGraphState struct{}

// parseTagResultFromMessage wraps parseTagResultFromMessage for use as a Lambda node.
func (p *NewsPipeline) parseTagResultFromMessage(ctx context.Context, msg *schema.Message) ([]TaggedNewsItem, error) {
	// Read rawByID from the parent graph's PipelineState
	rawByID := make(map[string]RawNewsItem)
	_ = compose.ProcessState[*PipelineState](ctx, func(_ context.Context, state *PipelineState) error {
		for _, raw := range state.RawItems {
			rawByID[raw.ID] = raw
		}
		return nil
	})

	return parseTagResultFromMessage(msg, rawByID)
}
