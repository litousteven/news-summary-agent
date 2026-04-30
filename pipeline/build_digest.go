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
	// Filter: only items that should be pushed
	selected := make([]MergedNewsItem, 0)
	for _, item := range items {
		if item.ShouldPush {
			selected = append(selected, item)
		}
	}

	// Deduplicate by link/title, keeping best source
	merged := dedupByTitle(selected)

	// Semantic dedup: remove within-batch duplicates missed by exact match
	merged = p.dedupByEmbedding(ctx, merged)

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

	// Build final list with limits: max GetMaxPerCategory() per category, Getp.GetMaxDigestItems()() total
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
			fp := buildFactParagraph(item)
			digestItems = append(digestItems, DigestItem{
				MergedNewsItem: item,
				FactParagraph:  fp,
			})
			catCount[cat]++
		}
		if len(digestItems) >= p.GetMaxDigestItems() {
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

	// Slot label: use the slot from the request if available, otherwise infer from current time
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

// dedupByTitle keeps the best source for each unique display_title or link.
// Titles are normalized (trimmed, lowercased) for matching.
func dedupByTitle(items []MergedNewsItem) []MergedNewsItem {
	best := make(map[string]MergedNewsItem)
	for _, item := range items {
		// Primary key: link (most reliable dedup signal)
		key := strings.TrimSpace(item.Link)
		if key == "" {
			// Fallback: display_title
			key = strings.TrimSpace(item.DisplayTitle)
			if key == "" {
				key = strings.TrimSpace(item.Title)
			}
			key = strings.ToLower(key)
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

// dedupByEmbedding removes within-batch semantic duplicates using embedding similarity.
// Items with similarity >= GetClusterThreshold() are grouped, keeping the one with the best source rank.
func (p *NewsPipeline) dedupByEmbedding(ctx context.Context, items []MergedNewsItem) []MergedNewsItem {
	if p.Embedding == nil || len(items) <= 1 {
		return items
	}

	// Compute embeddings for all items
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.DisplayTitle + " " + item.Summary
	}
	vecs, err := p.Embedding.EmbedStrings(ctx, texts)
	if err != nil || len(vecs) != len(items) {
		return items
	}

	// Find clusters of similar items using union-find
	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[i].Category != items[j].Category {
				continue // only dedup within same category
			}
			sim := cosineSimilarity(vecs[i], vecs[j])
			if sim >= p.GetClusterThreshold() {
				union(i, j)
			}
		}
	}

	// For each cluster, keep the best item (lowest source rank)
	clusters := make(map[int][]int) // root -> indices
	for i := range items {
		root := find(i)
		clusters[root] = append(clusters[root], i)
	}

	result := make([]MergedNewsItem, 0, len(clusters))
	for _, indices := range clusters {
		best := indices[0]
		for _, idx := range indices[1:] {
			if SourceRank[items[idx].Source] < SourceRank[items[best].Source] {
				best = idx
			} else if SourceRank[items[idx].Source] == SourceRank[items[best].Source] && items[idx].InterestScore > items[best].InterestScore {
				best = idx
			}
		}
		result = append(result, items[best])
	}
	return result
}
