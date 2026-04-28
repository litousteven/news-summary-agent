package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// tagResultItem is the expected JSON structure from LLM tagging output.
// Uses flexible types for fields that LLM may output inconsistently.
type tagResultItem struct {
	ID            string          `json:"id"`
	DisplayTitle  string          `json:"display_title"`
	Category      string          `json:"category"`
	TopicTags     json.RawMessage `json:"topic_tags"`
	Region        string          `json:"region"`
	EventKey      string          `json:"event_key"`
	InterestScore json.RawMessage `json:"interest_score"`
	IsDuplicate   json.RawMessage `json:"is_duplicate"`
	Selected      json.RawMessage `json:"selected"`
	WhySelected   string          `json:"why_selected"`
}

// parseTagResult parses the LLM output JSON into structured TaggedNewsItems.
func (p *NewsPipeline) parseTagResult(ctx context.Context, msg *schema.Message) ([]TaggedNewsItem, error) {
	content := msg.Content

	// Try direct JSON parse first
	var results []tagResultItem
	err := json.Unmarshal([]byte(content), &results)
	if err != nil {
		// Try extracting JSON from markdown code block
		extracted := extractJSONFromMarkdown(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		// Try finding JSON array in the text
		extracted := extractJSONArray(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM tag output as JSON: %w", err)
	}

	// Build tagged items from parsed results
	tagged := make([]TaggedNewsItem, 0, len(results))
	for _, r := range results {
		item := TaggedNewsItem{
			RawNewsItem: RawNewsItem{
				ID: r.ID,
			},
			DisplayTitle:  r.DisplayTitle,
			Category:      normalizeCategory(r.Category),
			TopicTags:     parseTopicTags(r.TopicTags),
			Region:        r.Region,
			EventKey:      r.EventKey,
			InterestScore: parseInterestScore(r.InterestScore),
			IsDuplicate:   parseBool(r.IsDuplicate, false),
			Selected:      parseBool(r.Selected, false),
			WhySelected:   r.WhySelected,
		}

		// Auto-generate event_key if missing
		if item.EventKey == "" {
			item.EventKey = generateEventKey(item.ID, item.Category)
		}

		// Default display_title to title if empty
		if item.DisplayTitle == "" {
			item.DisplayTitle = item.Title
		}

		tagged = append(tagged, item)
	}

	return tagged, nil
}

// parseTopicTags handles both array and string formats for topic_tags.
// LLM may output: ["a","b"] or "a,b" or "a, b"
func parseTopicTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	// Try as array first
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}

	// Try as comma-separated string
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

// parseInterestScore handles both number and string formats.
func parseInterestScore(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 6
	}

	// Try as number
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return normalizeScore(n)
	}

	// Try as string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		// Try parsing string as number
		var sn int
		if _, err := fmt.Sscanf(s, "%d", &sn); err == nil {
			return normalizeScore(sn)
		}
	}

	return 6
}

// parseBool handles both bool and string formats.
func parseBool(raw json.RawMessage, defaultVal bool) bool {
	if len(raw) == 0 {
		return defaultVal
	}

	// Try as bool
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}

	// Try as string
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

// normalizeCategory maps the category to one of the valid enum values.
func normalizeCategory(cat string) string {
	cat = strings.TrimSpace(cat)
	if ValidCategories[cat] {
		return cat
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
	default:
		return "其他重要动态"
	}
}

// normalizeScore clamps interest_score to 0-10 range.
func normalizeScore(score int) int {
	if score < 0 {
		return 6
	}
	if score > 10 {
		return 10
	}
	return score
}

// generateEventKey creates a fallback event_key from ID and category.
func generateEventKey(id, category string) string {
	now := time.Now().Format("2006-01-02")
	slug := slugify(id)
	return fmt.Sprintf("%s-%s", now, slug)
}

var nonAlphaNumRe = regexp.MustCompile(`[^a-z0-9\p{Han}]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphaNumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// extractJSONFromMarkdown tries to extract JSON from ```json ... ``` blocks.
func extractJSONFromMarkdown(content string) string {
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	matches := re.FindStringSubmatch(content)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// extractJSONArray finds the first JSON array in text.
func extractJSONArray(content string) string {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start >= 0 && end > start {
		return content[start : end+1]
	}
	return ""
}
