package pipeline

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// buildDigest selects, ranks, and formats news items into a digest.
func (p *NewsPipeline) buildDigest(ctx context.Context, items []MergedNewsItem) (*DigestData, error) {
	// Filter: only items that should be pushed
	selected := make([]MergedNewsItem, 0)
	for _, item := range items {
		if item.ShouldPush {
			selected = append(selected, item)
		}
	}

	// Deduplicate by event_key, keeping best source
	merged := dedupByEventKey(selected)

	// Group by category
	byCategory := make(map[string][]MergedNewsItem)
	for _, item := range merged {
		cat := item.Category
		if cat == "" {
			cat = "其他重要动态"
		}
		byCategory[cat] = append(byCategory[cat], item)
	}

	// Sort within each category by source rank
	for cat := range byCategory {
		sort.Slice(byCategory[cat], func(i, j int) bool {
			ri := SourceRank[byCategory[cat][i].Source]
			rj := SourceRank[byCategory[cat][j].Source]
			if ri != rj {
				return ri < rj
			}
			return byCategory[cat][i].InterestScore > byCategory[cat][j].InterestScore
		})
	}

	// Build final list with limits: max MaxPerCategory per category, MaxDigestItems total
	var digestItems []DigestItem
	catCount := make(map[string]int)
	for _, cat := range CategoryOrder {
		items, ok := byCategory[cat]
		if !ok {
			continue
		}
		for _, item := range items {
			if catCount[cat] >= MaxPerCategory {
				break
			}
			if len(digestItems) >= MaxDigestItems {
				break
			}
			fp := buildFactParagraph(item)
			digestItems = append(digestItems, DigestItem{
				MergedNewsItem: item,
				FactParagraph:  fp,
			})
			catCount[cat]++
		}
		if len(digestItems) >= MaxDigestItems {
			break
		}
	}

	// Stats
	stats := DigestStats{
		TotalFetched:  len(items),
		TotalTagged:   len(items),
		TotalSelected: len(digestItems),
		ByCategory:    catCount,
	}

	// Slot label
	slotLabel := getSlotLabel()

	return &DigestData{
		Items:       digestItems,
		SlotLabel:   slotLabel,
		CurrentTime: currentTimeStr(),
		Stats:       stats,
	}, nil
}

// dedupByEventKey keeps the best source for each event_key.
func dedupByEventKey(items []MergedNewsItem) []MergedNewsItem {
	best := make(map[string]MergedNewsItem)
	for _, item := range items {
		key := item.EventKey
		if key == "" {
			key = item.ID
		}
		existing, ok := best[key]
		if !ok {
			best[key] = item
			continue
		}
		// Keep the one with better source rank
		if SourceRank[item.Source] < SourceRank[existing.Source] {
			best[key] = item
		}
	}
	result := make([]MergedNewsItem, 0, len(best))
	for _, item := range best {
		result = append(result, item)
	}
	return result
}

// buildFactParagraph constructs a fact paragraph from a news item.
// Ported from the original build_digest_from_tags.py build_fact_paragraph.
func buildFactParagraph(item MergedNewsItem) string {
	source := cleanText(item.Source)
	title := cleanText(item.DisplayTitle)
	if title == "" {
		title = cleanText(item.Title)
	}
	summary := cleanText(item.Summary)
	region := cleanText(item.Region)
	if region == "" {
		region = "相关地区"
	}
	pub := cleanText(item.PublishedAt)
	if pub == "" {
		pub = "最新拉取"
	}

	subject := "相关方面"
	location := region
	event := title

	if source == "中新网" {
		if strings.Contains(summary, "日电") {
			m := regexp.MustCompile(`[^0-9]{0,20}(中新网|中新社)?([^0-9]{1,12})\d{1,2}月\d{1,2}日电`).FindStringSubmatch(summary)
			if len(m) >= 3 && m[2] != "" {
				location = m[2]
			}
		}
		if strings.Contains(summary, "总台记者获悉") {
			subject = "总台记者引述的相关方面"
		} else if strings.Contains(summary, "表示") || strings.Contains(summary, "称") {
			subject = "报道涉及的相关方面"
		}
	} else if source == "BBC" || source == "NPR" || source == "NYT" || source == "Al Jazeera" {
		subject = fmt.Sprintf("%s报道涉及的相关方面", source)
	}

	detail := extractQuoteOrNumber(summary)
	return fmt.Sprintf("%s在%s于%s发生的事件是：%s。%s", subject, location, pub, event, detail)
}

// extractQuoteOrNumber extracts a key quote or number from summary text.
func extractQuoteOrNumber(summary string) string {
	s := cleanText(summary)
	if s == "" {
		return ""
	}
	// Try Chinese quotes
	m := regexp.MustCompile(`["""]([^"""]{6,80})["""]`).FindStringSubmatch(s)
	if len(m) >= 2 {
		return fmt.Sprintf("原话：\"%s\"", m[1])
	}
	// Try regular quotes
	m = regexp.MustCompile(`"([^"]{6,80})"`).FindStringSubmatch(s)
	if len(m) >= 2 {
		return fmt.Sprintf("原话：\"%s\"", m[1])
	}
	// Try numbers with Chinese units
	m = regexp.MustCompile(`(\d+(?:\.\d+)?\s*(?:人|架|枚|亿美元|万|小时|%|级|名))`).FindStringSubmatch(s)
	if len(m) >= 2 {
		return fmt.Sprintf("关键数字：%s", m[1])
	}
	// Try "至少" prefix
	m = regexp.MustCompile(`(至少\d+(?:\.\d+)?\s*(?:人|架|枚|亿美元|万|小时|%|级|名))`).FindStringSubmatch(s)
	if len(m) >= 2 {
		return fmt.Sprintf("关键数字：%s", m[1])
	}
	// Fallback: first 70 chars
	if len(s) > 70 {
		return fmt.Sprintf("细节：%s…", s[:70])
	}
	return fmt.Sprintf("细节：%s", s)
}

// cleanText normalizes whitespace in text.
func cleanText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return s
}

// getSlotLabel returns the current slot label (午间版/晚间版/凌晨版).
func getSlotLabel() string {
	h := currentTime().Hour()
	switch {
	case h >= 6 && h < 12:
		return "午间版"
	case h >= 12 && h < 22:
		return "晚间版"
	default:
		return "凌晨版"
	}
}

// currentTime returns the current local time (overrideable in tests).
var currentTime = func() time.Time {
	return time.Now()
}

func currentTimeStr() string {
	return currentTime().Format("2006-01-02 15:04:05")
}
