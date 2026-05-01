package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// recordHistory appends pushed items to push_history.jsonl and returns the final result.
// It uses PipelineState to access DigestItems for per-item record writing with embeddings,
// which enables semantic dedup on subsequent runs.
func (p *NewsPipeline) recordHistory(ctx context.Context, msg *schema.Message) (*NewsSummaryResult, error) {
	summaryText := msg.Content

	result := &NewsSummaryResult{
		Message: summaryText,
		Stats: DigestStats{
			ByCategory: make(map[string]int),
		},
	}

	// Retrieve DigestItems, Slot, and Stats from shared state
	var digestItems []DigestItem
	var slot string
	_ = compose.ProcessState[*PipelineState](ctx, func(_ context.Context, state *PipelineState) error {
		digestItems = state.DigestItems
		slot = state.Slot
		// Copy stats from digest if available
		if state.DigestStats != nil {
			result.Stats = *state.DigestStats
		}
		result.DigestItems = digestItems
		return nil
	})

	// Write per-item records with embeddings for dedup on next run
	if len(digestItems) > 0 {
		if err := p.RecordHistoryFromDigest(ctx, digestItems, slot); err != nil {
			log.Printf("[RecordHistory] 写入逐条历史记录失败: %v", err)
			// Non-fatal: still return the result
		}
	} else {
		// Fallback: write a single session-level record if no digest items available
		slotLabel := slotToLabel(slot)
		now := time.Now().Format(time.RFC3339)
		record := PushHistoryRecord{
			PushTime:     now,
			Slot:         slot,
			DisplayTitle: fmt.Sprintf("国际新闻简报 %s", slotLabel),
			Category:     "简报",
			Source:       "多源",
			PublishedAt:  now,
			FactSummary:  truncateForHistory(summaryText),
		}
		if err := p.appendHistoryRecord(record); err != nil {
			return nil, fmt.Errorf("append history: %w", err)
		}
	}

	return result, nil
}

// historyFilePath returns the per-day history file path for the given date.
func (p *NewsPipeline) historyFilePath(t time.Time) string {
	return p.DataDir + "/push_history_" + t.Format("20060102") + ".jsonl"
}

// RecordHistoryFromDigest appends individual item records to today's history file.
// Computes embeddings for each item if Embedding client is available.
func (p *NewsPipeline) RecordHistoryFromDigest(ctx context.Context, items []DigestItem, slot string) error {
	now := time.Now()
	path := p.historyFilePath(now)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	defer f.Close()

	// Batch compute embeddings for all items
	var embeddings [][]float64
	if p.Embedding != nil && len(items) > 0 {
		texts := make([]string, len(items))
		for i, item := range items {
			texts[i] = item.DisplayTitle + " " + item.FactParagraph
		}
		vecs, err := CachedEmbedStrings(ctx, p, texts)
		if err == nil && len(vecs) == len(items) {
			embeddings = vecs
		}
	}

	nowStr := now.Format(time.RFC3339)
	for i, item := range items {
		record := PushHistoryRecord{
			PushTime:     nowStr,
			Slot:         slot,
			DisplayTitle: item.DisplayTitle,
			Category:     item.Category,
			Source:       item.Source,
			PublishedAt:  item.PublishedAt,
			Link:         item.Link,
			FactSummary:  item.FactParagraph,
			RawTitle:     item.Title,
		}
		if i < len(embeddings) && len(embeddings[i]) > 0 {
			record.Embedding = embeddings[i]
		}
		line, err := json.Marshal(record)
		if err != nil {
			continue
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}

	return nil
}

// appendHistoryRecord appends a single record to today's history file.
func (p *NewsPipeline) appendHistoryRecord(record PushHistoryRecord) error {
	path := p.historyFilePath(time.Now())

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	defer f.Close()

	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// truncateForHistory truncates text for history storage.
func truncateForHistory(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
