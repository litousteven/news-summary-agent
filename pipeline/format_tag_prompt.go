package pipeline

import (
	"context"
	"fmt"
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
		"tagging_guide":    guide,
		"tagging_examples": examples,
		"total_count":      fmt.Sprintf("%d", len(items)),
	}, nil
}

// loadTaggingGuide reads the tagging guide markdown file.
func (p *NewsPipeline) loadTaggingGuide() (string, error) {
	path := p.DataDir + "/tagging_guide.md"
	data, err := os.ReadFile(path)
	if err != nil {
		// Return embedded default if file not found
		return defaultTaggingGuide, nil
	}
	return string(data), nil
}

// loadTaggingExamples reads the tagging examples CSV file.
func (p *NewsPipeline) loadTaggingExamples() (string, error) {
	path := p.DataDir + "/tagging_examples.csv"
	data, err := os.ReadFile(path)
	if err != nil {
		// Return embedded default if file not found
		return defaultTaggingExamples, nil
	}
	return string(data), nil
}

// Embedded defaults (used when data files are not available)
const defaultTaggingGuide = `## 标注规范 v2

### 分类枚举（只能选一个）
- 战争与地缘
- 航空航天
- 军事装备
- AI与数码
- 新能源与汽车
- 全球经济
- 其他重要动态

### event_key 规则
- 格式：YYYY-MM-DD-slug-description
- 同事件多源报道时，必须共用同一个 event_key
- 简洁、稳定，英文/拼音均可

### 重复项判定
- 主项：is_duplicate=false, selected=true
- 重复项：is_duplicate=true, selected=false
- 主项优先：中文标题更清晰、信息更完整、来源更稳

### display_title 规则
- 最终给用户看的标题
- 英文标题翻成自然中文
- 可以润色但不改事实

### selected 规则
- selected=true：interest_score 较高、事件独特、对用户兴趣相关
- selected=false：重复报道、弱相关、信息价值低`

const defaultTaggingExamples = `title,display_title,category,topic_tags,region,interest_score,event_key,is_duplicate,selected,why_selected
伊朗袭击迪拜附近油轮，地区战事持续升级,伊朗袭击迪拜附近油轮，地区战事持续升级,战争与地缘,"伊朗,迪拜,油轮,中东",中东,9,2026-03-31-dubai-oil-tanker-strike,false,true,中东冲突升级且与能源运输相关
Kuwaiti Tanker Full of Oil Struck Off Dubai,特朗普发出威胁次日满载原油的科威特油轮在迪拜附近遭袭,战争与地缘,"迪拜,油轮,中东,能源",中东,8,2026-03-31-dubai-oil-tanker-strike,true,false,与主事件重复报道
SpaceX launches next-generation Starlink satellites,SpaceX 发射新一代 Starlink 卫星,航空航天,"SpaceX,卫星,火箭,航天",北美,10,2026-03-31-spacex-starlink-launch,false,true,高度符合用户航天兴趣
Japan deploys long-range missiles,日本在熊本和静冈部署远程导弹,军事装备,"日本,导弹,部署,防务",东亚,9,2026-03-31-japan-long-range-missile-deploy,false,true,典型军事部署新闻
Nvidia unveils new AI chip architecture,英伟达发布新一代 AI 芯片架构,AI与数码,"AI,芯片,Nvidia,半导体",北美,10,2026-03-31-nvidia-ai-chip-architecture,false,true,高度符合 AI/数码兴趣`
