package pipeline

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
)

// buildDigest selects, ranks, and formats news items into a digest.
func (p *NewsPipeline) buildDigest(ctx context.Context, items []MergedNewsItem) (*DigestData, error) {
	merged := p.dedupAndLinkBatch(ctx, items)

	candidates := make([]MergedNewsItem, 0)
	for _, item := range merged {
		if !item.SeenBefore {
			candidates = append(candidates, item)
		}
	}

	byCategory := make(map[string][]MergedNewsItem)
	for _, item := range candidates {
		cat := item.Category
		if cat == "" {
			cat = "其他重要动态"
		}
		byCategory[cat] = append(byCategory[cat], item)
	}

	for cat := range byCategory {
		sort.Slice(byCategory[cat], func(i, j int) bool {
			si, sj := byCategory[cat][i].InterestScore, byCategory[cat][j].InterestScore
			if si != sj {
				return si > sj
			}
			ri := SourceRank[byCategory[cat][i].Source]
			rj := SourceRank[byCategory[cat][j].Source]
			return ri < rj
		})
	}

	// First pass: select candidates per category
	selectedLinks := make(map[string]bool)
	var digestItems []DigestItem
	catCount := make(map[string]int)
	for _, cat := range CategoryOrder {
		items, ok := byCategory[cat]
		if !ok {
			continue
		}
		for _, item := range items {
			if catCount[cat] >= p.GetMaxPerCategory() {
				break
			}
			if len(digestItems) >= p.GetMaxDigestItems() {
				break
			}
			if selectedLinks[item.Link] {
				continue
			}
			fp := buildFactParagraph(item)
			digestItems = append(digestItems, DigestItem{
				MergedNewsItem: item,
				FactParagraph:  fp,
			})
			catCount[cat]++
			selectedLinks[item.Link] = true
		}
		if len(digestItems) >= p.GetMaxDigestItems() {
			break
		}
	}

	// Second pass: remove items that are now referenced by higher-priority selected items
	finalItems := make([]DigestItem, 0, len(digestItems))
	referencedLinks := make(map[string]bool)
	for _, item := range digestItems {
		for _, ref := range item.MergedNewsItem.Refs {
			referencedLinks[ref.Link] = true
		}
	}
	for _, item := range digestItems {
		if referencedLinks[item.Link] {
			// This item was already selected as a lower-priority candidate,
			// but is now referenced by a higher-priority item. Remove it.
			catCount[item.Category]--
		} else {
			finalItems = append(finalItems, item)
		}
	}
	digestItems = finalItems

	var taggingFailed int
	var originalFetchedCount, actualTaggedCount int
	_ = compose.ProcessState[*PipelineState](ctx, func(_ context.Context, state *PipelineState) error {
		originalFetchedCount = state.OriginalFetchedCount
		actualTaggedCount = state.ActualTaggedCount
		return nil
	})
	if originalFetchedCount > 0 {
		taggingFailed = originalFetchedCount - actualTaggedCount
	}

	stats := DigestStats{
		TotalFetched:   originalFetchedCount,
		TotalTagged:    actualTaggedCount,
		TaggingFailed:  taggingFailed,
		TotalSelected:  len(digestItems),
		ByCategory:     catCount,
	}

	slotLabel := getSlotLabel()
	_ = compose.ProcessState[*PipelineState](ctx, func(_ context.Context, state *PipelineState) error {
		if state.Slot != "" {
			slotLabel = slotToLabel(state.Slot)
		}
		return nil
	})

	return &DigestData{
		Items:       digestItems,
		SlotLabel:   slotLabel,
		CurrentTime: currentTimeStr(),
		Stats:       stats,
	}, nil
}

// buildFactParagraph constructs a fact paragraph from a news item.
func buildFactParagraph(item MergedNewsItem) string {
	title := cleanText(item.DisplayTitle)
	if title == "" {
		title = cleanText(item.Title)
	}
	summary := cleanText(item.Summary)
	region := cleanText(item.Region)
	pub := formatPublishTime(item.PublishedAt)

	var b strings.Builder

	// Location + time prefix
	if region != "" && pub != "" {
		b.WriteString(fmt.Sprintf("%s%s，", region, pub))
	} else if pub != "" {
		b.WriteString(pub + "，")
	}

	// Title as the core fact
	b.WriteString(title)

	// Append summary as supplementary detail (keep full for LLM summarization)
	if summary != "" {
		b.WriteString("。" + summary)
	}

	return b.String()
}

// formatPublishTime converts an RSS publish time to a readable Chinese date.
// Returns empty string if parsing fails.
func formatPublishTime(pub string) string {
	pub = strings.TrimSpace(pub)
	if pub == "" {
		return ""
	}
	for _, format := range []string{
		time.RFC1123,          // "Mon, 02 Jan 2006 15:04:05 MST"
		time.RFC1123Z,         // "Mon, 02 Jan 2006 15:04:05 -0700"
		time.RFC3339,          // "2006-01-02T15:04:05Z07:00"
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(format, pub); err == nil {
			return t.Format("1月2日")
		}
	}
	// If we can't parse, try to extract a date pattern
	m := regexp.MustCompile(`(\d{4})[/-](\d{1,2})[/-](\d{1,2})`).FindStringSubmatch(pub)
	if len(m) >= 4 {
		return fmt.Sprintf("%s月%s日", m[2], m[3])
	}
	return ""
}

// cleanText normalizes whitespace in text.
var whitespaceRe = regexp.MustCompile(`\s+`)

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = whitespaceRe.ReplaceAllString(s, " ")
	return s
}

// getSlotLabel returns the current slot label (午间版/晚间版/凌晨版).
func getSlotLabel() string {
	h := currentTime().Hour()
	return slotToLabelByHour(h)
}

// slotToLabel converts a slot string (e.g. "00:00", "12:00", "18:00") to a label.
func slotToLabel(slot string) string {
	switch slot {
	case "00:00":
		return "凌晨版"
	case "12:00":
		return "午间版"
	case "18:00":
		return "晚间版"
	default:
		// For "manual" or unknown slots, infer from current time
		return getSlotLabel()
	}
}

// slotToLabelByHour maps an hour value to a slot label.
func slotToLabelByHour(h int) string {
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
