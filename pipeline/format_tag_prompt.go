package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	tagpkg "github.com/litousteven/news-summary-agent/pipeline/tag"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func (p *NewsPipeline) formatTagPrompt(ctx context.Context, items []types.RawNewsItem) (map[string]any, error) {
	var sb strings.Builder
	for i, item := range items {
		sb.WriteString(fmt.Sprintf("### [%d] %s\n", i+1, item.Title))
		sb.WriteString(fmt.Sprintf("- ID: %s\n", item.ID))
		sb.WriteString(fmt.Sprintf("- 来源: %s (%s)\n", item.Source, item.Lang))
		sb.WriteString(fmt.Sprintf("- 摘要: %s\n", item.Summary))
		sb.WriteString(fmt.Sprintf("- 发布时间: %s\n", item.PublishedAt))
		sb.WriteString(fmt.Sprintf("- 链接: %s\n\n", item.Link))
	}

	categories, _ := p.loadCategories()
	categoriesText := formatCategoriesForPrompt(categories)

	guide, err := p.loadTaggingGuide()
	if err != nil {
		return nil, fmt.Errorf("load tagging guide: %w", err)
	}

	examples, err := p.loadTaggingExamples()
	if err != nil {
		return nil, fmt.Errorf("load tagging examples: %w", err)
	}

	return map[string]any{
		"news_items":       sb.String(),
		"categories":       categoriesText,
		"tagging_guide":    guide,
		"tagging_examples": examples,
		"total_count":      fmt.Sprintf("%d", len(items)),
	}, nil
}

func formatCategoriesForPrompt(defs []types.CategoryDef) string {
	return tagpkg.FormatCategoriesForPrompt(convertToTagCategoryDefs(defs))
}

func convertToTagCategoryDefs(defs []types.CategoryDef) []tagpkg.CategoryDef {
	result := make([]tagpkg.CategoryDef, len(defs))
	for i, c := range defs {
		result[i] = tagpkg.CategoryDef{
			Name:        c.Name,
			Keywords:    c.Keywords,
			Boundary:    c.Boundary,
			NotBoundary: c.NotBoundary,
		}
	}
	return result
}

func (p *NewsPipeline) loadCategories() ([]types.CategoryDef, error) {
	path := p.ConfigDir + "/categories.json"
	data, err := os.ReadFile(path)
	if err != nil {
		if err := p.initCategoriesFile(path); err != nil {
			log.Printf("[loadCategories] 初始化 categories.json 失败: %v, 使用内置默认值", err)
		}
		return types.DefaultCategories, nil
	}
	var defs []types.CategoryDef
	if err := json.Unmarshal(data, &defs); err != nil {
		return types.DefaultCategories, nil
	}
	if len(defs) == 0 {
		return types.DefaultCategories, nil
	}
	types.ReloadCategoryDefs(defs)
	return defs, nil
}

func (p *NewsPipeline) initCategoriesFile(path string) error {
	tplPath := p.ConfigDir + "/categories.json.template"
	tplData, err := os.ReadFile(tplPath)
	if err == nil {
		if err := os.WriteFile(path, tplData, 0644); err != nil {
			return fmt.Errorf("copy template: %w", err)
		}
		log.Printf("[loadCategories] 已从 %s 创建 categories.json", tplPath)
		return nil
	}
	data, err := json.MarshalIndent(types.DefaultCategories, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal default categories: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write categories.json: %w", err)
	}
	log.Printf("[loadCategories] 已从模板创建 %s", path)
	return nil
}

func (p *NewsPipeline) loadTaggingGuide() (string, error) {
	path := p.ConfigDir + "/tagging_guide.md"
	data, err := os.ReadFile(path)
	if err != nil {
		return tagpkg.DefaultTaggingGuide, nil
	}
	return string(data), nil
}

func (p *NewsPipeline) loadTaggingExamples() (string, error) {
	path := p.ConfigDir + "/tagging_examples.csv"
	data, err := os.ReadFile(path)
	if err != nil {
		return tagpkg.DefaultTaggingExamples, nil
	}
	return string(data), nil
}
