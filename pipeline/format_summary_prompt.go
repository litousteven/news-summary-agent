package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

type itemSummaryResult struct {
	Summary string `json:"summary"`
}

func (p *NewsPipeline) summarizePerItem(ctx context.Context, data *DigestData) (*DigestData, error) {
	log.Printf("[SummarizePerItem] Start, total items: %d", len(data.Items))
	if p.ChatModel == nil {
		log.Printf("[SummarizePerItem] ChatModel is nil, skipping")
		return data, nil
	}
	if len(data.Items) == 0 {
		log.Printf("[SummarizePerItem] No items, skipping")
		return data, nil
	}

	sem := make(chan struct{}, 3)
	var mu sync.Mutex
	var wg sync.WaitGroup
	success := 0
	failed := 0

	for i := range data.Items {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item := &data.Items[idx]
			promptText := buildItemSummaryPrompt(item)
			log.Printf("[SummarizePerItem] Item [%d] sending prompt (length: %d)", idx, len(promptText))

			messages := []*schema.Message{
				schema.SystemMessage("你是一名国际新闻简报编辑。请将新闻条目用自己的话客观概括成一段简洁的新闻摘要，不要加入分析和评论。"),
				schema.UserMessage(promptText),
			}

			resp, err := p.ChatModel.Generate(ctx, messages)
			if err != nil {
				log.Printf("[SummarizePerItem] Item [%d] ChatModel.Generate error: %v", idx, err)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			if resp == nil {
				log.Printf("[SummarizePerItem] Item [%d] ChatModel.Generate returned nil response", idx)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			log.Printf("[SummarizePerItem] Item [%d] LLM response (length: %d): %s", idx, len(resp.Content), resp.Content)

			result := parseItemSummaryResponse(resp.Content)
			if result == nil || result.Summary == "" {
				log.Printf("[SummarizePerItem] Item [%d] failed to parse summary JSON, raw: %s", idx, resp.Content)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			mu.Lock()
			data.Items[idx].ItemSummary = result.Summary
			success++
			log.Printf("[SummarizePerItem] Item [%d] summary set (%d chars): %s", idx, len(result.Summary), result.Summary)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	log.Printf("[SummarizePerItem] Done: %d success, %d failed, %d total", success, failed, len(data.Items))
	return data, nil
}

func buildItemSummaryPrompt(item *DigestItem) string {
	var sb strings.Builder
	sb.WriteString("请将以下新闻用自己的话概括为一段简洁的新闻摘要（50-100字），以 JSON 格式返回。\n\n")

	sb.WriteString(fmt.Sprintf("标题: %s\n", item.DisplayTitle))
	if item.FactParagraph != "" {
		sb.WriteString(fmt.Sprintf("事实段落: %s\n", item.FactParagraph))
	}
	if item.Summary != "" && item.Summary != item.FactParagraph {
		sb.WriteString(fmt.Sprintf("原始摘要: %s\n", truncateForLLM(item.Summary, 200)))
	}

	if len(item.Refs) > 0 {
		sb.WriteString("\n相关参考：\n")
		for _, ref := range item.Refs {
			label := ref.DisplayTitle
			if ref.FactSummary != "" {
				label = ref.FactSummary
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", ref.RelationNote, label))
		}
	}

	sb.WriteString("\n请严格按以下 JSON 格式回复（不要包含其他内容）：\n")
	sb.WriteString(`{"summary": "<一句话或一段话的新闻摘要>"}`)
	return sb.String()
}

func parseItemSummaryResponse(content string) *itemSummaryResult {
	var result itemSummaryResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		extracted := extractJSONFromMarkdown(content)
		if extracted != "" {
			if err2 := json.Unmarshal([]byte(extracted), &result); err2 != nil {
				return nil
			}
			return &result
		}
		extracted = extractJSONObject(content)
		if extracted != "" {
			if err2 := json.Unmarshal([]byte(extracted), &result); err2 != nil {
				return nil
			}
			return &result
		}
		return nil
	}
	return &result
}
