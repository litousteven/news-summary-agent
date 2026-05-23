package tag

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
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

func ExtractJSONFromMarkdown(content string) string {
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	matches := re.FindStringSubmatch(content)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func ExtractJSONArray(content string) string {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start >= 0 && end > start {
		return content[start : end+1]
	}
	return ""
}

func ParseTagResultItems(msgContent string) ([]TagResultItem, error) {
	content := msgContent

	var results []TagResultItem
	err := json.Unmarshal([]byte(content), &results)
	if err != nil {
		extracted := ExtractJSONFromMarkdown(content)
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
