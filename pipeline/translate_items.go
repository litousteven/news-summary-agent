package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/cloudwego/eino/schema"
)

type translateResult struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func (p *NewsPipeline) TranslateItems(ctx context.Context, data *DigestData) (*DigestData, error) {
	log.Printf("[TranslateItems] Start, total items: %d", len(data.Items))
	if p.ChatModel == nil {
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

	sem := make(chan struct{}, 3)
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
			promptText := buildSingleTranslatePrompt(item.DisplayTitle, item.Summary)
			log.Printf("[TranslateItems] Item [%d] sending prompt (length: %d)", i, len(promptText))

			messages := []*schema.Message{
				schema.SystemMessage("你是一个专业的新闻翻译助手，请将新闻标题和摘要翻译为简洁准确的简体中文。"),
				schema.UserMessage(promptText),
			}

			resp, err := p.ChatModel.Generate(ctx, messages)
			if err != nil {
				log.Printf("[TranslateItems] Item [%d] ChatModel.Generate error: %v", i, err)
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

			result := parseTranslateResponse(resp.Content)
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

	for _, idx := range toTranslate {
		if idx < len(data.Items) {
			old := data.Items[idx].FactParagraph
			data.Items[idx].FactParagraph = buildFactParagraph(data.Items[idx].MergedNewsItem)
			if old != data.Items[idx].FactParagraph {
				log.Printf("[TranslateItems] Rebuilt FactParagraph for item[%d]", idx)
			}
		}
	}

	return data, nil
}

func buildSingleTranslatePrompt(title, summary string) string {
	s := truncateForLLM(summary, 300)
	return fmt.Sprintf(
		"请将以下新闻标题和摘要翻译为简体中文，保持简洁准确，并以 JSON 格式返回。\n\n"+
			"标题: %s\n摘要: %s\n\n"+
			"请严格按以下 JSON 格式回复（不要包含其他内容）：\n"+
			`{"title": "<中文标题>", "summary": "<中文摘要>"}`,
		title, s,
	)
}

func parseTranslateResponse(content string) *translateResult {
	var result translateResult
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

func extractJSONObject(content string) string {
	start := -1
	end := -1
	braceCount := 0
	for i, c := range content {
		if c == '{' {
			if start == -1 {
				start = i
			}
			braceCount++
		} else if c == '}' {
			braceCount--
			if braceCount == 0 && start != -1 {
				end = i
				break
			}
		}
	}
	if start >= 0 && end > start {
		return content[start : end+1]
	}
	return ""
}
