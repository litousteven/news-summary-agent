package pipeline

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	tagpkg "github.com/litousteven/news-summary-agent/pipeline/tag"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

type tagResultItem struct {
	ID            string          `json:"id"`
	DisplayTitle  string          `json:"display_title"`
	Category      string          `json:"category"`
	TopicTags     json.RawMessage `json:"topic_tags"`
	Region        string          `json:"region"`
	InterestScore json.RawMessage `json:"interest_score"`
	IsDuplicate   json.RawMessage `json:"is_duplicate"`
	Selected      json.RawMessage `json:"selected"`
	WhySelected   string          `json:"why_selected"`
}

func (p *NewsPipeline) parseTagResult(ctx context.Context, msg *schema.Message) ([]types.TaggedNewsItem, error) {
	content := msg.Content

	var results []tagResultItem
	err := json.Unmarshal([]byte(content), &results)
	if err != nil {
		extracted := tagpkg.ExtractJSONFromMarkdown(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		extracted := tagpkg.ExtractJSONArray(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		return nil, &jsonParseError{err: err}
	}

	rawByID := make(map[string]types.RawNewsItem)
	rawByTitle := make(map[string]types.RawNewsItem)
	_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
		for _, raw := range state.RawItems {
			rawByID[raw.ID] = raw
			rawByTitle[raw.Title] = raw
		}
		return nil
	})

	tagged := make([]types.TaggedNewsItem, 0, len(results))
	for _, r := range results {
		item := types.TaggedNewsItem{
			RawNewsItem: types.RawNewsItem{
				ID: r.ID,
			},
			DisplayTitle:  r.DisplayTitle,
			Category:      normalizeCategoryInternal(r.Category),
			TopicTags:     tagpkg.ParseTopicTags(r.TopicTags),
			Region:        r.Region,
			InterestScore: tagpkg.ParseInterestScore(r.InterestScore),
			IsDuplicate:   tagpkg.ParseBool(r.IsDuplicate, false),
			Selected:      tagpkg.ParseBool(r.Selected, false),
			WhySelected:   r.WhySelected,
		}

		if raw, ok := rawByID[r.ID]; ok {
			item.RawNewsItem = raw
		} else if raw, ok := rawByTitle[r.DisplayTitle]; ok {
			item.RawNewsItem = raw
		} else if raw, ok := rawByTitle[r.ID]; ok {
			item.RawNewsItem = raw
		}

		if item.DisplayTitle == "" {
			item.DisplayTitle = item.Title
		}

		tagged = append(tagged, item)
	}

	return tagged, nil
}

type jsonParseError struct{ err error }

func (e *jsonParseError) Error() string {
	return "failed to parse LLM tag output as JSON: " + e.err.Error()
}

func (e *jsonParseError) Unwrap() error {
	return e.err
}

func normalizeCategoryInternal(cat string) string {
	return tagpkg.NormalizeCategory(cat, types.ValidCategories, convertCategoryDefsForTag())
}

func convertCategoryDefsForTag() []tagpkg.CategoryDef {
	result := make([]tagpkg.CategoryDef, len(types.CategoryDefs))
	for i, c := range types.CategoryDefs {
		result[i] = tagpkg.CategoryDef{
			Name:        c.Name,
			Keywords:    c.Keywords,
			Boundary:    c.Boundary,
			NotBoundary: c.NotBoundary,
		}
	}
	return result
}
