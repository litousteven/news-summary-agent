package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// updateTaggingGuide analyzes the tagged news items from this run and uses the LLM
// to suggest updates to the tagging guide (add/split/merge categories).
func (p *NewsPipeline) updateTaggingGuide(ctx context.Context, result *NewsSummaryResult) (*NewsSummaryResult, error) {
	if len(result.DigestItems) == 0 {
		return result, nil
	}

	// Build a summary of categories and topic_tags from this run
	categoryCounts := make(map[string]int)
	var allTags []string
	tagSet := make(map[string]bool)
	for _, item := range result.DigestItems {
		cat := item.Category
		if cat == "" {
			cat = "其他重要动态"
		}
		categoryCounts[cat]++
		for _, tag := range item.TopicTags {
			if !tagSet[tag] {
				tagSet[tag] = true
				allTags = append(allTags, tag)
			}
		}
	}

	// Build current category distribution summary
	var statsBuilder strings.Builder
	for _, cat := range CategoryOrder {
		if count, ok := categoryCounts[cat]; ok {
			statsBuilder.WriteString(fmt.Sprintf("- %s: %d条\n", cat, count))
		}
	}
	if categoryCounts["其他重要动态"] > 0 || len(categoryCounts) == 0 {
		// Already included via CategoryOrder
	}

	// Load current tagging guide
	currentGuide, err := p.loadTaggingGuide()
	if err != nil {
		currentGuide = defaultTaggingGuide
	}

	// Build prompt for LLM to analyze if categories need updating
	prompt := fmt.Sprintf(`你是一名新闻分类体系分析师。根据本次收集的新闻数据，分析当前分类体系是否需要调整。

## 当前分类枚举
%s

## 当前分类下的新闻分布
%s

## 本轮新闻出现的关键标签
%s

## 当前分类边界规范
%s

## 分析要求

请分析以下问题，并给出建议：

1. **新增分类**：是否出现了大量新闻无法归入现有分类，需要新增分类？如果有，给出分类名称和边界说明。
2. **拆分分类**：是否有某个分类过于宽泛，内部包含明显不同的子类型，应该拆分？如果有，给出拆分方案。
3. **合并分类**：是否有分类之间边界模糊或重叠，应该合并？如果有，给出合并方案。

如果当前分类体系无需调整，直接回复"无需调整"。
如果需要调整，请输出调整方案，格式如下：

### 新增分类
- 分类名：xxx
  边界说明：适合xxx，不要误分到xxx

### 拆分分类
- 原分类：xxx → 新分类1：xxx, 新分类2：xxx
  边界说明：...

### 合并分类
- 合并：xxx + yyy → 新分类：zzz
  边界说明：...`,
		strings.Join(CategoryOrder, "、"),
		statsBuilder.String(),
		strings.Join(allTags, "、"),
		currentGuide)

	messages := []*schema.Message{
		schema.SystemMessage("你是一名新闻分类体系分析师，专门分析分类枚举的合理性并给出调整建议。"),
		schema.UserMessage(prompt),
	}

	resp, err := p.ChatModel.Generate(ctx, messages)
	if err != nil {
		log.Printf("[UpdateTaggingGuide] LLM调用失败: %v", err)
		return result, nil // non-fatal
	}

	suggestion := strings.TrimSpace(resp.Content)
	if suggestion == "无需调整" || suggestion == "" {
		log.Printf("[UpdateTaggingGuide] 分类体系无需调整")
		return result, nil
	}

	log.Printf("[UpdateTaggingGuide] 收到分类调整建议:\n%s", suggestion)

	// Apply suggestions by updating the tagging guide
	// Append the suggestions as a "pending review" section
	updated, err := p.applyTaggingGuideUpdate(currentGuide, suggestion)
	if err != nil {
		log.Printf("[UpdateTaggingGuide] 更新tagging_guide失败: %v", err)
		return result, nil // non-fatal
	}

	// Write updated guide
	path := p.ConfigDir + "/tagging_guide.md"
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		log.Printf("[UpdateTaggingGuide] 写入tagging_guide失败: %v", err)
		return result, nil
	}

	log.Printf("[UpdateTaggingGuide] tagging_guide.md 已更新")
	return result, nil
}

// applyTaggingGuideUpdate merges LLM suggestions into the existing tagging guide.
func (p *NewsPipeline) applyTaggingGuideUpdate(currentGuide, suggestion string) (string, error) {
	// If the suggestion contains new categories, update the guide's category section
	var additions []string

	lines := strings.Split(suggestion, "\n")
	var inSection string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### 新增分类") {
			inSection = "new"
			continue
		} else if strings.HasPrefix(trimmed, "### 拆分分类") {
			inSection = "split"
			continue
		} else if strings.HasPrefix(trimmed, "### 合并分类") {
			inSection = "merge"
			continue
		}

		if trimmed == "" {
			continue
		}

		switch inSection {
		case "new", "split", "merge":
			additions = append(additions, trimmed)
		}
	}

	if len(additions) == 0 {
		return currentGuide, nil
	}

	// Append a "动态调整记录" section to the guide
	var sb strings.Builder
	sb.WriteString(currentGuide)
	if !strings.HasSuffix(currentGuide, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\n---\n\n")
	sb.WriteString("## 动态调整记录\n\n")
	sb.WriteString("以下为基于新闻数据自动分析的分类调整建议（待人工审核确认后加入正式分类枚举）：\n\n")
	for _, a := range additions {
		sb.WriteString("- " + a + "\n")
	}

	return sb.String(), nil
}
