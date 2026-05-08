package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// formatTagPrompt converts raw news items into template variables for the tagging ChatTemplate.
func (p *NewsPipeline) formatTagPrompt(ctx context.Context, items []RawNewsItem) (map[string]any, error) {
	// Format news items as numbered list
	var sb strings.Builder
	for i, item := range items {
		sb.WriteString(fmt.Sprintf("### [%d] %s\n", i+1, item.Title))
		sb.WriteString(fmt.Sprintf("- ID: %s\n", item.ID))
		sb.WriteString(fmt.Sprintf("- 来源: %s (%s)\n", item.Source, item.Lang))
		sb.WriteString(fmt.Sprintf("- 摘要: %s\n", item.Summary))
		sb.WriteString(fmt.Sprintf("- 发布时间: %s\n", item.PublishedAt))
		sb.WriteString(fmt.Sprintf("- 链接: %s\n\n", item.Link))
	}

	// Load categories and build the categories section of the prompt
	categories, _ := p.loadCategories()
	categoriesText := formatCategoriesForPrompt(categories)

	// Load tagging guide
	guide, err := p.loadTaggingGuide()
	if err != nil {
		return nil, fmt.Errorf("load tagging guide: %w", err)
	}

	// Load tagging examples
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

// formatCategoriesForPrompt renders the category list and boundaries as text for the LLM prompt.
func formatCategoriesForPrompt(defs []CategoryDef) string {
	var sb strings.Builder
	// Category enum
	sb.WriteString("category 枚举值限定 " + fmt.Sprintf("%d", len(defs)) + " 个：")
	names := make([]string, len(defs))
	for i, c := range defs {
		names[i] = c.Name
	}
	sb.WriteString(strings.Join(names, " / "))
	sb.WriteString("\n\n")

	// Category boundaries
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

// loadCategories reads the categories JSON file and updates the global category state.
// If the file does not exist, it is created by copying categories.json.template
// (falling back to DefaultCategories if the template is also missing).
func (p *NewsPipeline) loadCategories() ([]CategoryDef, error) {
	path := p.ConfigDir + "/categories.json"
	data, err := os.ReadFile(path)
	if err != nil {
		// First run: copy from template
		if err := p.initCategoriesFile(path); err != nil {
			log.Printf("[loadCategories] 初始化 categories.json 失败: %v, 使用内置默认值", err)
		}
		return DefaultCategories, nil
	}
	var defs []CategoryDef
	if err := json.Unmarshal(data, &defs); err != nil {
		return DefaultCategories, nil
	}
	if len(defs) == 0 {
		return DefaultCategories, nil
	}
	ReloadCategoryDefs(defs)
	return defs, nil
}

// initCategoriesFile copies categories.json.template to categories.json.
// Falls back to writing DefaultCategories if the template file is missing.
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
	// Template not found, write embedded defaults
	data, err := json.MarshalIndent(DefaultCategories, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal default categories: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write categories.json: %w", err)
	}
	log.Printf("[loadCategories] 已从模板创建 %s", path)
	return nil
}

// loadTaggingGuide reads the tagging guide markdown file.
func (p *NewsPipeline) loadTaggingGuide() (string, error) {
	path := p.ConfigDir + "/tagging_guide.md"
	data, err := os.ReadFile(path)
	if err != nil {
		// Return embedded default if file not found
		return defaultTaggingGuide, nil
	}
	return string(data), nil
}

// loadTaggingExamples reads the tagging examples CSV file.
func (p *NewsPipeline) loadTaggingExamples() (string, error) {
	path := p.ConfigDir + "/tagging_examples.csv"
	data, err := os.ReadFile(path)
	if err != nil {
		// Return embedded default if file not found
		return defaultTaggingExamples, nil
	}
	return string(data), nil
}

// Embedded defaults (used when data files are not available)
// Note: category definitions come from categories.json / DefaultCategories,
// not from the tagging guide. The guide only contains non-category rules.
const defaultTaggingGuide = `## 标注规范 v3

### 重复项判定
- 主项：is_duplicate=false, selected=true
- 重复项：is_duplicate=true, selected=false
- 同一事件的不同报道，display_title 应尽量保持一致
- 主项优先：中文标题更清晰、信息更完整、来源更稳

### display_title 规则
- 最终给用户看的标题
- 英文标题翻成自然中文
- 可以润色但不改事实
- 同一事件的不同报道，display_title 应尽量保持一致

### selected 规则
- selected=true：interest_score 较高、事件独特、对用户兴趣相关
- selected=false：重复报道、弱相关、信息价值低`

const defaultTaggingExamples = `title,display_title,category,topic_tags,region,interest_score,is_duplicate,selected,why_selected
伊朗袭击迪拜附近油轮，地区战事持续升级,伊朗袭击迪拜附近油轮，地区战事持续升级,战争与地缘,"伊朗,迪拜,油轮,中东",中东,9,false,true,中东冲突升级且与能源运输相关
Kuwaiti Tanker Full of Oil Struck Off Dubai,特朗普发出威胁次日满载原油的科威特油轮在迪拜附近遭袭,战争与地缘,"迪拜,油轮,中东,能源",中东,8,true,false,与主事件重复报道
SpaceX launches next-generation Starlink satellites,SpaceX 发射新一代 Starlink 卫星,航空航天,"SpaceX,卫星,火箭,航天",北美,10,false,true,高度符合用户航天兴趣
Japan deploys long-range missiles,日本在熊本和静冈部署远程导弹,军事装备,"日本,导弹,部署,防务",东亚,9,false,true,典型军事部署新闻
Nvidia unveils new AI chip architecture,英伟达发布新一代 AI 芯片架构,AI与数码,"AI,芯片,Nvidia,半导体",北美,10,false,true,高度符合 AI/数码兴趣`
