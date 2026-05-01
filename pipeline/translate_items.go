package pipeline

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// TranslateItems translates non-Chinese news titles and summaries to Chinese.
// It operates on DigestData (the output of buildDigest) and returns modified DigestData.
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

	// Collect non-Chinese items
	var toTranslate []int
	var langStats = make(map[string]int)
	for i, item := range data.Items {
		lang := item.Lang
		langStats[lang]++
		if lang != "zh" && lang != "zh-CN" && lang != "zh-TW" {
			toTranslate = append(toTranslate, i)
			log.Printf("[TranslateItems] Item [%d] lang=%q title=%q", i, lang, item.DisplayTitle)
		}
	}
	log.Printf("[TranslateItems] Language stats: %v, toTranslate: %d items", langStats, len(toTranslate))
	if len(toTranslate) == 0 {
		log.Printf("[TranslateItems] No non-Chinese items found, skipping")
		return data, nil
	}

	// Build batch translation prompt
	var sb strings.Builder
	sb.WriteString("请将以下新闻标题和摘要翻译为简体中文，保持简洁准确。\n\n")
	for idx, i := range toTranslate {
		item := data.Items[i]
		sb.WriteString(fmt.Sprintf("[%d]\n标题: %s\n摘要: %s\n\n", idx, item.DisplayTitle, truncateForLLM(item.Summary, 300)))
	}
	sb.WriteString("请按编号顺序回复，格式为：\n[编号]\n标题: <中文标题>\n摘要: <中文摘要>\n\n只回复翻译结果，不要其他内容。")

	promptText := sb.String()
	log.Printf("[TranslateItems] Sending translation prompt (length: %d)\n%s", len(promptText), promptText)

	messages := []*schema.Message{
		schema.SystemMessage("你是一个专业的新闻翻译助手，请将新闻标题和摘要翻译为简洁准确的简体中文。"),
		schema.UserMessage(promptText),
	}

	resp, err := p.ChatModel.Generate(ctx, messages)
	if err != nil {
		log.Printf("[TranslateItems] ChatModel.Generate error: %v, falling through without translation", err)
		return data, nil
	}
	if resp == nil {
		log.Printf("[TranslateItems] ChatModel.Generate returned nil response")
		return data, nil
	}

	log.Printf("[TranslateItems] LLM response (length: %d):\n%s", len(resp.Content), resp.Content)

	// Parse translation results
	lines := strings.Split(resp.Content, "\n")
	currentIdx := -1
	parsedCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			n := strings.Trim(line, "[]")
			if idx := parseInt(n); idx >= 0 && idx < len(toTranslate) {
				currentIdx = toTranslate[idx]
				log.Printf("[TranslateItems] Parsed index marker: [%s] -> data.Items[%d]", n, currentIdx)
			} else {
				currentIdx = -1
				log.Printf("[TranslateItems] Invalid index marker: [%s]", n)
			}
		} else if currentIdx >= 0 {
			if strings.HasPrefix(line, "标题:") || strings.HasPrefix(line, "标题：") {
				title := strings.TrimPrefix(strings.TrimPrefix(line, "标题:"), "标题：")
				title = strings.TrimSpace(title)
				if title != "" {
					oldTitle := data.Items[currentIdx].DisplayTitle
					data.Items[currentIdx].DisplayTitle = title
					parsedCount++
					log.Printf("[TranslateItems] Updated title for item[%d]: %q -> %q", currentIdx, oldTitle, title)
				}
			} else if strings.HasPrefix(line, "摘要:") || strings.HasPrefix(line, "摘要：") {
				summary := strings.TrimPrefix(strings.TrimPrefix(line, "摘要:"), "摘要：")
				summary = strings.TrimSpace(summary)
				if summary != "" {
					data.Items[currentIdx].Summary = summary
					parsedCount++
					log.Printf("[TranslateItems] Updated summary for item[%d]", currentIdx)
				}
			}
		}
	}

	log.Printf("[TranslateItems] Parsing complete: %d fields updated", parsedCount)

	// Rebuild FactParagraph for translated items so downstream nodes see Chinese content
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

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
