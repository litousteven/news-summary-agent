package pipeline

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	tagpkg "github.com/litousteven/news-summary-agent/pipeline/tag"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func formatCategoriesForPrompt(defs []types.CategoryDef) string {
	return tagpkg.FormatCategoriesForPrompt(tagpkg.ConvertCategoryDefs(defs))
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
