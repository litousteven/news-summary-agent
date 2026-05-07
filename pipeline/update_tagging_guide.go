package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// updateTaggingGuide analyzes the tagged news items from this run and uses the LLM
// to suggest updates to the category definitions in categories.json.
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

	// Load current categories and build a text representation for the prompt
	currentCategories, _ := p.loadCategories()
	categoriesText := formatCategoriesForPrompt(currentCategories)

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
2. **删除分类**：是否有分类长期没有新闻落入，可以考虑删除？如果有，给出分类名称。
3. **拆分分类**：是否有某个分类过于宽泛，内部包含明显不同的子类型，应该拆分？如果有，给出拆分方案。
4. **合并分类**：是否有分类之间边界模糊或重叠，应该合并？如果有，给出合并方案。

如果当前分类体系无需调整，直接回复"无需调整"。
如果需要调整，请输出 JSON 格式的调整方案（不要用 markdown 代码块包裹，直接输出 JSON）：

{"add": [{"name": "新分类名", "keywords": ["关键词1", "关键词2"], "boundary": "适合的分类边界说明", "not_boundary": "不适合归入的情况说明"}], "remove": ["要删除的分类名"], "split": [{"from": "原分类名", "into": [{"name": "新分类1", "keywords": ["..."], "boundary": "...", "not_boundary": "..."}]}], "merge": [{"from": ["分类A", "分类B"], "into": {"name": "合并后名称", "keywords": ["..."], "boundary": "...", "not_boundary": "..."}}]}

注意：只输出需要变更的部分，不需要变更的不要列出。不要输出分析过程。`,
		strings.Join(CategoryOrder, "、"),
		statsBuilder.String(),
		strings.Join(allTags, "、"),
		categoriesText)

	messages := []*schema.Message{
		schema.SystemMessage("你是一名新闻分类体系分析师，专门分析分类枚举的合理性并给出调整建议。只输出需要变更的 JSON 方案，不要输出分析过程。"),
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

	// Parse the suggestion and apply changes to categories.json
	changes, err := parseCategoryChanges(suggestion)
	if err != nil {
		log.Printf("[UpdateTaggingGuide] 解析分类调整建议失败: %v", err)
		return result, nil
	}

	if err := p.applyCategoryChanges(currentCategories, changes); err != nil {
		log.Printf("[UpdateTaggingGuide] 应用分类调整失败: %v", err)
		return result, nil
	}

	log.Printf("[UpdateTaggingGuide] categories.json 已更新")
	return result, nil
}

// categoryChanges represents parsed LLM suggestions for category modifications.
type categoryChanges struct {
	Add    []CategoryDef `json:"add"`
	Remove []string      `json:"remove"`
	Split  []splitChange `json:"split"`
	Merge  []mergeChange `json:"merge"`
}

type splitChange struct {
	From string        `json:"from"`
	Into []CategoryDef `json:"into"`
}

type mergeChange struct {
	From []string    `json:"from"`
	Into CategoryDef `json:"into"`
}

// parseCategoryChanges extracts the JSON from the LLM response and parses it.
func parseCategoryChanges(suggestion string) (*categoryChanges, error) {
	// Try direct JSON parse first
	var changes categoryChanges
	if err := json.Unmarshal([]byte(suggestion), &changes); err == nil {
		return &changes, nil
	}

	// Try extracting JSON from markdown code block
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	matches := re.FindStringSubmatch(suggestion)
	if len(matches) >= 2 {
		if err := json.Unmarshal([]byte(matches[1]), &changes); err == nil {
			return &changes, nil
		}
	}

	// Try finding JSON object in the text
	start := strings.Index(suggestion, "{")
	end := strings.LastIndex(suggestion, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(suggestion[start:end+1]), &changes); err == nil {
			return &changes, nil
		}
	}

	return nil, fmt.Errorf("failed to parse category changes from LLM response")
}

// applyCategoryChanges modifies the categories list and writes it to categories.json.
func (p *NewsPipeline) applyCategoryChanges(currentCategories []CategoryDef, changes *categoryChanges) error {
	if len(changes.Add) == 0 && len(changes.Remove) == 0 && len(changes.Split) == 0 && len(changes.Merge) == 0 {
		return nil
	}

	// Build a map of current categories by name for easy lookup
	catMap := make(map[string]CategoryDef, len(currentCategories))
	// Preserve order
	catOrder := make([]string, len(currentCategories))
	for i, c := range currentCategories {
		catMap[c.Name] = c
		catOrder[i] = c.Name
	}

	// Apply removals
	removeSet := make(map[string]bool)
	for _, name := range changes.Remove {
		removeSet[name] = true
		delete(catMap, name)
		log.Printf("[UpdateTaggingGuide] 删除分类: %s", name)
	}

	// Apply splits: remove the original, add the new ones
	for _, s := range changes.Split {
		delete(catMap, s.From)
		removeSet[s.From] = true
		for _, into := range s.Into {
			catMap[into.Name] = into
			log.Printf("[UpdateTaggingGuide] 拆分分类 %s → %s", s.From, into.Name)
		}
	}

	// Apply merges: remove the originals, add the merged one
	for _, m := range changes.Merge {
		mergedName := m.Into.Name
		for _, from := range m.From {
			delete(catMap, from)
			removeSet[from] = true
		}
		catMap[mergedName] = m.Into
		log.Printf("[UpdateTaggingGuide] 合并分类 %v → %s", m.From, mergedName)
	}

	// Apply additions (before "其他重要动态")
	for _, c := range changes.Add {
		catMap[c.Name] = c
		log.Printf("[UpdateTaggingGuide] 新增分类: %s", c.Name)
	}

	// Rebuild ordered list: keep original order for surviving categories,
	// append new categories before "其他重要动态"
	var result []CategoryDef
	var newCats []CategoryDef
	for _, name := range catOrder {
		if removeSet[name] {
			continue
		}
		if c, ok := catMap[name]; ok {
			result = append(result, c)
			delete(catMap, name)
		}
	}

	// Any remaining in catMap are new categories (from add/split/merge)
	for name, c := range catMap {
		// Check if this is a genuinely new category not yet in the result
		found := false
		for _, existing := range result {
			if existing.Name == name {
				found = true
				break
			}
		}
		if !found {
			newCats = append(newCats, c)
		}
	}

	// Insert new categories before "其他重要动态" if it exists
	if len(newCats) > 0 {
		finalResult := make([]CategoryDef, 0, len(result)+len(newCats))
		inserted := false
		for _, c := range result {
			if c.Name == "其他重要动态" && !inserted {
				finalResult = append(finalResult, newCats...)
				inserted = true
			}
			finalResult = append(finalResult, c)
		}
		if !inserted {
			finalResult = append(finalResult, newCats...)
		}
		result = finalResult
	}

	// Update runtime state
	ReloadCategoryDefs(result)

	// Write to categories.json
	path := p.ConfigDir + "/categories.json"
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal categories: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write categories.json: %w", err)
	}

	return nil
}
