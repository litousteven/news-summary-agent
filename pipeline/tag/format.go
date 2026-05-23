package tag

import (
	"fmt"
	"strings"
)

func FormatCategoriesForPrompt(defs []CategoryDef) string {
	var sb strings.Builder

	sb.WriteString("category 枚举值限定 " + fmt.Sprintf("%d", len(defs)) + " 个：")
	names := make([]string, len(defs))
	for i, c := range defs {
		names[i] = c.Name
	}
	sb.WriteString(strings.Join(names, " / "))
	sb.WriteString("\n\n")

	sb.WriteString("## 分类边界\n\n")
	for _, c := range defs {
		sb.WriteString("### " + c.Name + "\n")
		sb.WriteString("适合：\n- " + c.Boundary + "\n")
		if c.NotBoundary != "" {
			sb.WriteString("\n不要误分到这里的情况：\n- " + c.NotBoundary + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

type BatchItem struct {
	ID          string
	Source      string
	Lang        string
	Title       string
	Summary     string
	PublishedAt string
	Link        string
}

func FormatBatchTagPromptVars(batch []BatchItem, categoriesText, guide, examples string) map[string]any {
	var sb strings.Builder
	for i, item := range batch {
		sb.WriteString(fmt.Sprintf("### [%d] %s\n", i+1, item.Title))
		sb.WriteString(fmt.Sprintf("- ID: %s\n", item.ID))
		sb.WriteString(fmt.Sprintf("- 来源: %s (%s)\n", item.Source, item.Lang))
		sb.WriteString(fmt.Sprintf("- 摘要: %s\n", item.Summary))
		sb.WriteString(fmt.Sprintf("- 发布时间: %s\n", item.PublishedAt))
		sb.WriteString(fmt.Sprintf("- 链接: %s\n\n", item.Link))
	}

	return map[string]any{
		"news_items":       sb.String(),
		"categories":       categoriesText,
		"tagging_guide":    guide,
		"tagging_examples": examples,
		"total_count":      fmt.Sprintf("%d", len(batch)),
	}
}
