package pipeline

import (
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/schema"

	tagpkg "github.com/litousteven/news-summary-agent/pipeline/tag"
)

func (p *NewsPipeline) newTagChatTemplate() (prompt.ChatTemplate, error) {
	return prompt.FromMessages(schema.FString,
		schema.SystemMessage(tagSystemPrompt),
		schema.UserMessage(tagUserPrompt),
	), nil
}

const tagSystemPrompt = tagpkg.SystemPrompt

const tagUserPrompt = tagpkg.UserPrompt
