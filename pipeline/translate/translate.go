package translate

import (
	"encoding/json"
	"fmt"
	"time"

	"context"
	"log"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/litousteven/news-summary-agent/pipeline/config"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
	"github.com/litousteven/news-summary-agent/pipeline/util"
)

func TranslateItems(ctx context.Context, chatModel model.BaseChatModel, data *types.DigestData, cfg config.TagBatchConfig) (*types.DigestData, error) {
	log.Printf("[TranslateItems] Start, total items: %d", len(data.Items))
	if chatModel == nil {
		log.Printf("[TranslateItems] ChatModel is nil, skipping")
		return data, nil
	}
	if len(data.Items) == 0 {
		log.Printf("[TranslateItems] No items, skipping")
		return data, nil
	}

	var toTranslate []int
	var langStats = make(map[string]int)
	var emptySummaryItems []int
	for i, item := range data.Items {
		lang := item.Lang
		langStats[lang]++
		if lang != "zh" && lang != "zh-CN" && lang != "zh-TW" {
			toTranslate = append(toTranslate, i)
			if item.Summary == "" {
				emptySummaryItems = append(emptySummaryItems, i)
				log.Printf("[TranslateItems] Item [%d] lang=%q title=%q 摘要为空（上游数据缺失）", i, lang, item.DisplayTitle)
			} else {
				log.Printf("[TranslateItems] Item [%d] lang=%q title=%q", i, lang, item.DisplayTitle)
			}
		}
	}
	log.Printf("[TranslateItems] Language stats: %v, toTranslate: %d items, emptySummary: %d items",
		langStats, len(toTranslate), len(emptySummaryItems))
	if len(toTranslate) == 0 {
		log.Printf("[TranslateItems] No non-Chinese items found, skipping")
		return data, nil
	}

	sem := make(chan struct{}, cfg.MaxConcurrentBatches)
	var mu sync.Mutex
	var wg sync.WaitGroup
	translated := 0
	failed := 0

	for _, idx := range toTranslate {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item := data.Items[i]
			promptText := BuildSingleTranslatePrompt(item.DisplayTitle, item.Summary)
			log.Printf("[TranslateItems] Item [%d] sending prompt (length: %d)", i, len(promptText))

			messages := []*schema.Message{
				schema.SystemMessage("你是一个专业的新闻翻译助手，请将新闻标题和摘要翻译为简洁准确的简体中文。"),
				schema.UserMessage(promptText),
			}

			var resp *schema.Message
			var callErr error
		retryLoop:
			for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
				if attempt > 0 {
					backoff := time.Duration(1<<(attempt-1)) * cfg.RetryBaseDelay
					log.Printf("[TranslateItems] Item [%d] 第 %d 次重试（等待 %v）...", i, attempt, backoff)
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
				log.Printf("[TranslateItems] Item [%d] 第 %d 次尝试失败: %v", i, attempt+1, callErr)
			}

			if callErr != nil {
				log.Printf("[TranslateItems] Item [%d] 重试耗尽（共 %d 次）: %v", i, cfg.MaxRetries+1, callErr)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			if resp == nil {
				log.Printf("[TranslateItems] Item [%d] ChatModel.Generate returned nil response", i)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			log.Printf("[TranslateItems] Item [%d] LLM response (length: %d): %s", i, len(resp.Content), resp.Content)

			result := ParseTranslateResponse(resp.Content)
			if result == nil {
				log.Printf("[TranslateItems] Item [%d] failed to parse JSON response, raw: %s", i, resp.Content)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			mu.Lock()
			if result.Title != "" {
				oldTitle := data.Items[i].DisplayTitle
				data.Items[i].DisplayTitle = result.Title
				log.Printf("[TranslateItems] Item [%d] title updated: %q -> %q", i, oldTitle, result.Title)
			}
			if result.Summary != "" {
				data.Items[i].Summary = result.Summary
				log.Printf("[TranslateItems] Item [%d] summary updated", i)
			}
			translated++
			mu.Unlock()
		}(idx)
	}
	wg.Wait()

	log.Printf("[TranslateItems] Done: %d translated, %d failed, %d total",
		translated, failed, len(toTranslate))

	return data, nil
}

func BuildSingleTranslatePrompt(title, summary string) string {
	s := util.TruncateSummaryForLLM(summary, 300)
	return fmt.Sprintf(
		"请将以下新闻标题和摘要翻译为简体中文，保持简洁准确，并以 JSON 格式返回。\n\n"+
			"标题: %s\n摘要: %s\n\n"+
			"请严格按以下 JSON 格式回复（不要包含其他内容）：\n"+
			`{"title": "<中文标题>", "summary": "<中文摘要>"}`,
		title, s,
	)
}

func ParseTranslateResponse(content string) *translateResult {
	var result translateResult
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

type translateResult struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}
