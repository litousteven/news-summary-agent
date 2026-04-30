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
		contentBuilder.WriteString(fmt.Sprintf("- %s\n", item.FactParagraph))
		// Append references as context for LLM
		for _, ref := range item.References {
			contentBuilder.WriteString(fmt.Sprintf("  [%s] %s\n", ref.RelationNote, ref.FactSummary))
		}
	}

	return map[string]any{
		"digest_content": contentBuilder.String(),
		"slot_label":     digest.SlotLabel,
		"current_time":   digest.CurrentTime,
	}, nil
}
