package pipeline

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	types "github.com/litousteven/news-summary-agent/pipeline/types"

	fetchrss "github.com/litousteven/news-summary-agent/pipeline/fetch_rss"
)

// buildDigest selects, ranks, and formats news items into a digest.
func (p *NewsPipeline) buildDigest(ctx context.Context, items []types.MergedNewsItem) (*types.DigestData, error) {
	log.Printf("[BuildDigest] === 开始: %d 条 merged items ===", len(items))
	merged := p.dedupAndLinkBatch(ctx, items)

	candidates := make([]types.MergedNewsItem, 0)
	var noSource int
	for _, item := range merged {
		if item.SeenBefore {
			continue
		}
		// 兜底：简报里每一条都必须能回溯到一条真实抓取的新闻。上游若漏出
		// 无来源条目（见 ParseTaggedItems），这里再拦一次——宁可少推一条，
		// 也不能推一条读者无法自查的内容。
		if strings.TrimSpace(item.Source) == "" {
			noSource++
			log.Printf("[BuildDigest] ⚠ 丢弃无来源条目: display_title=%q link=%q", item.DisplayTitle, item.Link)
			continue
		}
		candidates = append(candidates, item)
	}
	if noSource > 0 {
		log.Printf("[BuildDigest] 本轮丢弃 %d 条无来源条目", noSource)
	}

	byCategory := make(map[string][]types.MergedNewsItem)
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
			ri := fetchrss.SourceRank[byCategory[cat][i].Source]
			rj := fetchrss.SourceRank[byCategory[cat][j].Source]
			return ri < rj
		})
	}

	// First pass: select candidates per category
	selectedLinks := make(map[string]bool)
	var digestItems []types.DigestItem
	catCount := make(map[string]int)
	for _, cat := range types.CategoryOrder {
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
			digestItems = append(digestItems, types.DigestItem{
				MergedNewsItem: item,
				FactParagraph:  fp,
				DatePrefix:     itemDatePrefix(item),
			})
			catCount[cat]++
			selectedLinks[item.Link] = true
		}
		if len(digestItems) >= p.GetMaxDigestItems() {
			break
		}
	}

	// Second pass: remove items that are now referenced by higher-priority selected items
	finalItems := make([]types.DigestItem, 0, len(digestItems))
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
	_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
		originalFetchedCount = state.OriginalFetchedCount
		actualTaggedCount = state.ActualTaggedCount
		return nil
	})
	if originalFetchedCount > 0 {
		taggingFailed = originalFetchedCount - actualTaggedCount
	}

	stats := types.DigestStats{
		TotalFetched:  originalFetchedCount,
		TotalTagged:   actualTaggedCount,
		TaggingFailed: taggingFailed,
		TotalSelected: len(digestItems),
		ByCategory:    catCount,
	}

	slotLabel := getSlotLabel()
	_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
		if state.Slot != "" {
			slotLabel = slotToLabel(state.Slot)
		}
		return nil
	})

	return &types.DigestData{
		Items:       digestItems,
		SlotLabel:   slotLabel,
		CurrentTime: currentTimeStr(),
		Stats:       stats,
	}, nil
}

// buildFactParagraph constructs a fact paragraph from a news item.
func buildFactParagraph(item types.MergedNewsItem) string {
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
// The year appears only when it differs from the current year: same-year items
// stay terse, while a stale item (e.g. one resurfaced by a frozen feed) is
// unambiguous instead of looking like it happened this year.
// Returns empty string if parsing fails.
func formatPublishTime(pub string) string {
	t, ok := parsePublishTime(pub)
	if !ok {
		return ""
	}
	if t.Year() != currentTime().Year() {
		return t.Format("2006年1月2日")
	}
	return t.Format("1月2日")
}

// parsePublishTime parses an RSS publish time. It tolerates the formats seen
// across the configured feeds, plus a bare YYYY-MM-DD date as a last resort.
// ok is false when the value is empty or unrecognized, which callers treat as
// "unknown age" rather than "stale".
func parsePublishTime(pub string) (time.Time, bool) {
	pub = strings.TrimSpace(pub)
	if pub == "" {
		return time.Time{}, false
	}
	for _, format := range []string{
		time.RFC1123,  // "Mon, 02 Jan 2006 15:04:05 MST"
		time.RFC1123Z, // "Mon, 02 Jan 2006 15:04:05 -0700"
		time.RFC3339,  // "2006-01-02T15:04:05Z07:00"
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(format, pub); err == nil {
			return t, true
		}
	}
	if m := datePatternRe.FindStringSubmatch(pub); len(m) >= 4 {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

// freshnessOf classifies an item's publish time against the freshness cutoff
// and returns the parsed time (zero when unparseable, so callers can still
// track a feed's newest item even when every item in it is stale).
//
// The purity matters: this is the single place the freshness rule is expressed,
// so tests cover it directly instead of going through the network.
func freshnessOf(pub string, cutoff time.Time) (freshness, time.Time) {
	t, ok := parsePublishTime(pub)
	if !ok {
		return freshnessUndated, time.Time{}
	}
	if t.Before(cutoff) {
		return freshnessStale, t
	}
	return freshnessFresh, t
}

// freshness classifies an item's publish time against the freshness cutoff.
type freshness int

const (
	// freshnessFresh: published inside the window.
	freshnessFresh freshness = iota
	// freshnessStale: published before the window; the item is dropped.
	freshnessStale
	// freshnessUndated: no parseable publish time; the item is kept because
	// dropping items of unknown age would silently lose feed coverage.
	freshnessUndated
)

// itemDatePrefix returns the date-only prefix used in front of ItemSummary.
// It deliberately excludes Region: the LLM summary usually already opens with
// the region, so including it again would duplicate it.
func itemDatePrefix(item types.MergedNewsItem) string {
	pub := formatPublishTime(item.PublishedAt)
	if pub == "" {
		return ""
	}
	return pub + "，"
}

// datePatternRe matches a bare YYYY-MM-DD / YYYY/MM/DD date inside a
// non-standard publish string.
var datePatternRe = regexp.MustCompile(`(\d{4})[/-](\d{1,2})[/-](\d{1,2})`)

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
