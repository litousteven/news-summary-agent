package tag

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/litousteven/news-summary-agent/pipeline/types"
	"github.com/litousteven/news-summary-agent/pipeline/util"
)

func ParseTopicTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		parts := strings.Split(s, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	}

	return nil
}

func ParseInterestScore(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 6
	}

	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return NormalizeScore(n)
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		var sn int
		if _, err := fmt.Sscanf(s, "%d", &sn); err == nil {
			return NormalizeScore(sn)
		}
	}

	return 6
}

func ParseBool(raw json.RawMessage, defaultVal bool) bool {
	if len(raw) == 0 {
		return defaultVal
	}

	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}

	return defaultVal
}

func NormalizeScore(score int) int {
	if score < 0 {
		return 6
	}
	if score > 10 {
		return 10
	}
	return score
}

func NormalizeCategory(cat string, validCategories map[string]bool, categoryDefs []CategoryDef) string {
	cat = strings.TrimSpace(cat)
	if validCategories[cat] {
		return cat
	}

	for _, c := range categoryDefs {
		for _, kw := range c.Keywords {
			if strings.Contains(cat, kw) {
				return c.Name
			}
		}
	}

	switch {
	case strings.Contains(cat, "战争") || strings.Contains(cat, "地缘"):
		return "战争与地缘"
	case strings.Contains(cat, "航空") || strings.Contains(cat, "航天"):
		return "航空航天"
	case strings.Contains(cat, "军事") || strings.Contains(cat, "装备"):
		return "军事装备"
	case strings.Contains(cat, "AI") || strings.Contains(cat, "数码") || strings.Contains(cat, "芯片"):
		return "AI与数码"
	case strings.Contains(cat, "新能源") || strings.Contains(cat, "汽车") || strings.Contains(cat, "电动"):
		return "新能源与汽车"
	case strings.Contains(cat, "经济") || strings.Contains(cat, "金融") || strings.Contains(cat, "贸易"):
		return "全球经济"
	case strings.Contains(cat, "国内") || strings.Contains(cat, "内政") || strings.Contains(cat, "两岸"):
		return "国内事务"
	default:
		return "其他重要动态"
	}
}

func ExtractJSONArray(content string) string {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start >= 0 && end > start {
		return content[start : end+1]
	}

	var wrapper struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(content), &wrapper); err == nil && len(wrapper.Items) > 0 {
		return string(wrapper.Items)
	}

	return ""
}

func ParseTagResultItems(msgContent string) ([]TagResultItem, error) {
	content := msgContent

	var results []TagResultItem
	err := json.Unmarshal([]byte(content), &results)
	if err != nil {
		extracted := util.ExtractJSONFromMarkdown(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		extracted := ExtractJSONArray(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		var single TagResultItem
		if err2 := json.Unmarshal([]byte(content), &single); err2 == nil {
			results = []TagResultItem{single}
			err = nil
		}
	}
	if err != nil {
		extracted := ExtractJSONArray(content)
		if extracted != "" {
			var single TagResultItem
			if err2 := json.Unmarshal([]byte(extracted), &single); err2 == nil {
				results = []TagResultItem{single}
				err = nil
			}
		}
	}
	if err != nil {
		contentPreview := content
		if len(contentPreview) > 500 {
			contentPreview = contentPreview[:500] + "..."
		}
		log.Printf("[ParseTagResult] 解析LLM输出失败: error=%v | content_preview=%q", err, contentPreview)
		return nil, fmt.Errorf("failed to parse LLM tag output as JSON: %w", err)
	}

	return results, nil
}

func ParseTaggedItems(msgContent string, rawItems []types.RawNewsItem, validCategories map[string]bool, categoryDefs []CategoryDef) ([]types.TaggedNewsItem, error) {
	tagItems, err := ParseTagResultItems(msgContent)
	if err != nil {
		return nil, err
	}

	rawByID := make(map[string]types.RawNewsItem, len(rawItems))
	rawByTitle := make(map[string]types.RawNewsItem, len(rawItems))
	// 模型偶尔会把 ID 的「来源-」前缀丢掉（来源名是中文时尤其常见），
	// 留下裸哈希。用后缀建一张表，先把这类结果救回来。
	rawByIDHash := make(map[string]types.RawNewsItem, len(rawItems))
	for _, raw := range rawItems {
		rawByID[raw.ID] = raw
		rawByTitle[raw.Title] = raw
		if hash := idHash(raw.ID); hash != "" {
			rawByIDHash[hash] = raw
		}
	}

	tagged := make([]types.TaggedNewsItem, 0, len(tagItems))
	var orphaned int
	for _, r := range tagItems {
		item := types.TaggedNewsItem{
			RawNewsItem: types.RawNewsItem{
				ID: r.ID,
			},
			DisplayTitle:  r.DisplayTitle,
			Category:      NormalizeCategory(r.Category, validCategories, categoryDefs),
			TopicTags:     ParseTopicTags(r.TopicTags),
			Region:        r.Region,
			InterestScore: ParseInterestScore(r.InterestScore),
			IsDuplicate:   ParseBool(r.IsDuplicate, false),
			Selected:      ParseBool(r.Selected, false),
			WhySelected:   r.WhySelected,
		}

		// 逐级尝试把标注结果绑回一条真实抓取到的原始条目。
		matched := false
		if raw, ok := rawByID[r.ID]; ok {
			item.RawNewsItem, matched = raw, true
		} else if raw, ok := rawByTitle[r.DisplayTitle]; ok {
			item.RawNewsItem, matched = raw, true
		} else if raw, ok := rawByTitle[r.ID]; ok {
			item.RawNewsItem, matched = raw, true
		} else if raw, ok := rawByIDHash[idHash(r.ID)]; ok {
			item.RawNewsItem, matched = raw, true
			log.Printf("[ParseTaggedItems] ID 前缀缺失，按哈希后缀匹配成功: id=%q → 原始条目 source=%s", r.ID, raw.Source)
		}

		// 绑定不上就丢弃。原实现保留这类结果，会产出一条 source/title/link
		// 全空、只有模型生成内容的条目，并一路推进简报——等于凭空发布一条
		// 无法回溯到任何原文的「新闻」。2026-09-14 与 09-15 各发生过一次。
		if !matched {
			orphaned++
			log.Printf("[ParseTaggedItems] ⚠ 丢弃无法回溯到原始条目的标注结果: id=%q display_title=%q",
				r.ID, r.DisplayTitle)
			continue
		}

		if item.DisplayTitle == "" {
			item.DisplayTitle = item.Title
		}

		tagged = append(tagged, item)
	}

	if orphaned > 0 {
		log.Printf("[ParseTaggedItems] 本轮丢弃 %d 条无来源标注结果（原始 %d 条，返回 %d 条）",
			orphaned, len(rawItems), len(tagItems))
	}

	return tagged, nil
}

// idHash 取 "来源-哈希" 形式 ID 的哈希部分；没有分隔符时原样返回。
func idHash(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

func ParseTagResultFromMessage(msgContent string, rawItems []types.RawNewsItem) ([]types.TaggedNewsItem, error) {
	return ParseTaggedItems(msgContent, rawItems, types.ValidCategories, ConvertCategoryDefs(types.CategoryDefs))
}
