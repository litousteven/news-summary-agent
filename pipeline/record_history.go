package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// recordHistory appends pushed items to push_history.jsonl and returns the final result.
func (p *NewsPipeline) recordHistory(ctx context.Context, msg *schema.Message) (*NewsSummaryResult, error) {
	summaryText := msg.Content

	result := &NewsSummaryResult{
		Message: summaryText,
		Stats: DigestStats{
			ByCategory: make(map[string]int),
		},
	}

	// Record a single session entry to push_history.jsonl
	now := time.Now().UTC().Format(time.RFC3339)
	record := PushHistoryRecord{
		PushTime:     now,
		Slot:         getSlotLabel(),
		EventKey:     fmt.Sprintf("digest-%s", time.Now().Format("2006-01-02-150405")),
		DisplayTitle: fmt.Sprintf("国际新闻简报 %s", getSlotLabel()),
		Category:     "简报",
		Source:       "多源",
		PublishedAt:  now,
		FactSummary:  truncateForHistory(summaryText),
	}

	if err := p.appendHistoryRecord(record); err != nil {
		return nil, fmt.Errorf("append history: %w", err)
	}

	return result, nil
}

// RecordHistoryFromDigest appends individual item records to push_history.jsonl.
// Computes embeddings for each item if Embedding client is available.
func (p *NewsPipeline) RecordHistoryFromDigest(ctx context.Context, items []DigestItem, slot string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	path := p.DataDir + "/push_history.jsonl"

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
		vecs, err := p.Embedding.EmbedStrings(ctx, texts)
		if err == nil && len(vecs) == len(items) {
			embeddings = vecs
		}
	}

	for i, item := range items {
		record := PushHistoryRecord{
			PushTime:     now,
			Slot:         slot,
			EventKey:     item.EventKey,
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

// appendHistoryRecord appends a single record to push_history.jsonl.
func (p *NewsPipeline) appendHistoryRecord(record PushHistoryRecord) error {
	path := p.DataDir + "/push_history.jsonl"

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
