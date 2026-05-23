package pipeline

import (
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/schema"
)

// newTagChatTemplate creates the ChatTemplate for the tagging stage.
func (p *NewsPipeline) newTagChatTemplate() (prompt.ChatTemplate, error) {
	return prompt.FromMessages(schema.FString,
		schema.SystemMessage(tagSystemPrompt),
		schema.UserMessage(tagUserPrompt),
	), nil
}

const tagSystemPrompt = `你是一名国际新闻标注专家。你的任务是阅读新闻条目，为每条新闻生成结构化标注。

## 标注规则

1. 输出 JSON 数组，每个元素对应一条新闻
2. 必填字段：id, display_title, category, topic_tags, region, interest_score, is_duplicate, selected, why_selected
3. {categories}
4. 同一事件的不同报道，display_title 应尽量保持一致，便于后续去重
5. is_duplicate=true 的条目通常 selected=false
6. display_title 是给用户看的标题，英文标题翻成自然中文
7. interest_score 范围 0-10

## 标注规范（来自 tagging_guide.md）

{tagging_guide}

## 标注示例（来自 tagging_examples）

{tagging_examples}`

const tagUserPrompt = `请标注以下 {total_count} 条新闻，输出 JSON 数组：

{news_items}

要求：
1. 严格按照 JSON 数组格式输出，不要输出其他内容
2. 每个元素必须包含所有必填字段
3. 同一事件的不同报道，display_title 应尽量保持一致`


