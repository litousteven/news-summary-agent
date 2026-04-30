package pipeline

import (
	"context"
	"fmt"
	"strings"
)

// formatSummaryPrompt converts DigestData into template variables for the summary ChatTemplate.
func (p *NewsPipeline) formatSummaryPrompt(ctx context.Context, digest *DigestData) (map[string]any, error) {
	// Build digest content organized by category
	var contentBuilder strings.Builder
	currentCat := ""
	for _, item := range digest.Items {
		if item.Category != currentCat {
			if currentCat != "" {
				contentBuilder.WriteString("\n")
			}
			currentCat = item.Category
			contentBuilder.WriteString(fmt.Sprintf("## %s\n", currentCat))
		}
		if item.SeenBefore {
			contentBuilder.WriteString(fmt.Sprintf("- [追踪更新] %s\n", item.FactParagraph))
		} else {
			contentBuilder.WriteString(fmt.Sprintf("- %s\n", item.FactParagraph))
		}
	}

	// Build history section for seen-before items that have prior context
	var historyBuilder strings.Builder
	hasHistory := false
	for _, item := range digest.Items {
		if item.SeenBefore && item.LastFactSummary != "" {
			if !hasHistory {
				historyBuilder.WriteString("## 需要前情提要的条目\n\n")
				hasHistory = true
			}
			historyBuilder.WriteString(fmt.Sprintf("- 【%s】%s（上次推送：%s）\n",
				item.DisplayTitle, item.LastFactSummary, item.LastPushTime))
		}
	}

	historySection := ""
	if hasHistory {
		historySection = historyBuilder.String()
	} else {
		historySection = "（本轮无需要前情提要的条目）"
	}

	return map[string]any{
		"digest_content":  contentBuilder.String(),
		"slot_label":      digest.SlotLabel,
		"current_time":    digest.CurrentTime,
		"history_section": historySection,
	}, nil
}
