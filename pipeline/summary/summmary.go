package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/litousteven/news-summary-agent/pipeline/config"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
	"github.com/litousteven/news-summary-agent/pipeline/util"
)

type itemSummaryResult struct {
	Summary string `json:"summary"`
}

func SummarizePerItem(ctx context.Context, chatModel model.BaseChatModel, data *types.DigestData, cfg config.TagBatchConfig) (*types.DigestData, error) {
	log.Printf("[SummarizePerItem] Start, total items: %d", len(data.Items))
	if chatModel == nil {
		log.Printf("[SummarizePerItem] ChatModel is nil, skipping")
		return data, nil
	}
	if len(data.Items) == 0 {
		log.Printf("[SummarizePerItem] No items, skipping")
		return data, nil
	}

	sem := make(chan struct{}, cfg.MaxConcurrentBatches)
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

			var resp *schema.Message
			var callErr error
		retryLoop:
			for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
				if attempt > 0 {
					backoff := time.Duration(1<<(attempt-1)) * cfg.RetryBaseDelay
					log.Printf("[SummarizePerItem] Item [%d] 第 %d 次重试（等待 %v）...", idx, attempt, backoff)
					select {
					case <-ctx.Done():
						callErr = ctx.Err()
						break retryLoop
					case <-time.After(backoff):
					}
				}

				callCtx, cancel := context.WithTimeout(ctx, cfg.BatchTimeout)
				resp, callErr = chatModel.Generate(callCtx, messages)
				cancel()

				if callErr == nil {
					break
				}
				log.Printf("[SummarizePerItem] Item [%d] 第 %d 次尝试失败: %v", idx, attempt+1, callErr)
			}

			if callErr != nil {
				log.Printf("[SummarizePerItem] Item [%d] 重试耗尽（共 %d 次）: %v", idx, cfg.MaxRetries+1, callErr)
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

func buildItemSummaryPrompt(item *types.DigestItem) string {
	var sb strings.Builder
	sb.WriteString("请将以下新闻用自己的话概括为一段简洁的新闻摘要（50-100字），以 JSON 格式返回。\n\n")

	sb.WriteString(fmt.Sprintf("标题: %s\n", item.DisplayTitle))
	if item.FactParagraph != "" {
		sb.WriteString(fmt.Sprintf("事实段落: %s\n", item.FactParagraph))
	}
	if item.Summary != "" && item.Summary != item.FactParagraph {
		sb.WriteString(fmt.Sprintf("原始摘要: %s\n", util.TruncateSummaryForLLM(item.Summary, 200)))
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
		extracted := util.ExtractJSONFromMarkdown(content)
		if extracted != "" {
			if err2 := json.Unmarshal([]byte(extracted), &result); err2 != nil {
				return nil
			}
			return &result
		}
		extracted = util.ExtractJSONObject(content)
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
