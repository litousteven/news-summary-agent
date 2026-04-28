package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// mergeHistory merges tagged news items with push history for deduplication.
// It uses both event_key exact match and embedding-based semantic similarity.
func (p *NewsPipeline) mergeHistory(ctx context.Context, items []TaggedNewsItem) ([]MergedNewsItem, error) {
	// Load push history
	history, err := p.loadPushHistory()
	if err != nil {
		return nil, fmt.Errorf("load push history: %w", err)
	}

	// Filter to recent history (last 24 hours)
	cutoff := time.Now().UTC().Add(-HistoryLookback)
	recent := filterRecentHistory(history, cutoff)

	// Index by event_key for exact match
	byEvent := make(map[string]*PushHistoryRecord)
	for i := range recent {
		key := strings.TrimSpace(recent[i].EventKey)
		if key == "" {
			continue
		}
		byEvent[key] = &recent[i]
	}

	// Build history embedding index for semantic dedup
	var historyEmbeds [][]float64
	historyWithEmbed := make([]*PushHistoryRecord, 0)
	if p.Embedding != nil {
		// Collect history items that have embeddings already
		for i := range recent {
			if len(recent[i].Embedding) > 0 {
				historyWithEmbed = append(historyWithEmbed, &recent[i])
				historyEmbeds = append(historyEmbeds, recent[i].Embedding)
			}
		}

		// For history items without embeddings, compute them now
		var missing []string
		var missingIdx []int
		for i := range recent {
			if len(recent[i].Embedding) == 0 {
				missing = append(missing, recent[i].DisplayTitle+" "+recent[i].FactSummary)
				missingIdx = append(missingIdx, i)
			}
		}
		if len(missing) > 0 {
			vecs, err := p.Embedding.EmbedStrings(ctx, missing)
			if err == nil && len(vecs) == len(missing) {
				for j, idx := range missingIdx {
					recent[idx].Embedding = vecs[j]
					historyWithEmbed = append(historyWithEmbed, &recent[idx])
					historyEmbeds = append(historyEmbeds, vecs[j])
				}
			}
		}
	}

	// Compute embeddings for current items if embedding client is available
	var itemEmbeds [][]float64
	if p.Embedding != nil && len(items) > 0 {
		texts := make([]string, len(items))
		for i, item := range items {
			texts[i] = item.DisplayTitle + " " + item.Summary
		}
		vecs, err := p.Embedding.EmbedStrings(ctx, texts)
		if err == nil && len(vecs) == len(items) {
			itemEmbeds = vecs
		}
	}

	// Merge
	result := make([]MergedNewsItem, 0, len(items))
	for i, item := range items {
		merged := MergedNewsItem{
			TaggedNewsItem: item,
			ShouldPush:     true,
		}

		// 1. Exact match by event_key
		key := strings.TrimSpace(item.EventKey)
		if prev, ok := byEvent[key]; ok {
			merged.SeenBefore = true
			merged.ShouldPush = false
			merged.LastPushTime = prev.PushTime
			merged.LastFactSummary = prev.FactSummary
			merged.HistoryNote = fmt.Sprintf("此前已推送过该事件，时间：%s", prev.PushTime)
			result = append(result, merged)
			continue
		}

		// 2. Semantic similarity check (if embeddings available)
		if len(itemEmbeds) > 0 && i < len(itemEmbeds) && len(itemEmbeds[i]) > 0 {
			for j, hEmb := range historyEmbeds {
				if j >= len(historyWithEmbed) {
					break
				}
				sim := cosineSimilarity(itemEmbeds[i], hEmb)
				if sim >= ClusterThreshold {
					prev := historyWithEmbed[j]
					merged.SeenBefore = true
					merged.ShouldPush = false
					merged.LastPushTime = prev.PushTime
					merged.LastFactSummary = prev.FactSummary
					merged.HistoryNote = fmt.Sprintf("语义相似推送(%.2f)，时间：%s", sim, prev.PushTime)
					break
				}
			}
		}

		result = append(result, merged)
	}

	return result, nil
}

// loadPushHistory reads push_history.jsonl.
func (p *NewsPipeline) loadPushHistory() ([]PushHistoryRecord, error) {
	path := p.DataDir + "/push_history.jsonl"
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no history file yet
		}
		return nil, err
	}

	var records []PushHistoryRecord
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r PushHistoryRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // skip malformed lines
		}
		records = append(records, r)
	}
	return records, nil
}

// filterRecentHistory returns only records within the lookback window.
func filterRecentHistory(records []PushHistoryRecord, cutoff time.Time) []PushHistoryRecord {
	var recent []PushHistoryRecord
	for _, r := range records {
		if r.PushTime == "" {
			continue
		}
		t, err := parseTime(r.PushTime)
		if err != nil {
			continue
		}
		if t.UTC().After(cutoff) {
			recent = append(recent, r)
		}
	}
	return recent
}

// parseTime tries multiple time formats.
func parseTime(s string) (time.Time, error) {
	for _, format := range []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05Z07:00",
	} {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}
