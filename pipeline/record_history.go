package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func (p *NewsPipeline) recordHistory(ctx context.Context, data *types.DigestData) (*types.NewsSummaryResult, error) {
	message := buildFinalMessage(data)

	result := &types.NewsSummaryResult{
		Message: message,
		Stats:   data.Stats,
	}

	var slot string
	_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
		slot = state.Slot
		return nil
	})

	result.DigestItems = data.Items

	if len(data.Items) > 0 {
		if err := p.RecordHistoryFromDigest(ctx, data.Items, slot); err != nil {
			log.Printf("[RecordHistory] 写入逐条历史记录失败: %v", err)
		}
	} else {
		slotLabel := slotToLabel(slot)
		now := time.Now().Format(time.RFC3339)
		record := types.PushHistoryRecord{
			PushTime:     now,
			Slot:         slot,
			DisplayTitle: fmt.Sprintf("国际新闻简报 %s", slotLabel),
			Category:     "简报",
			Source:       "多源",
			PublishedAt:  now,
			FactSummary:  truncateForHistory("暂无内容"),
		}
		if err := p.appendHistoryRecord(record); err != nil {
			return nil, fmt.Errorf("append history: %w", err)
		}
	}

	return result, nil
}

func buildFinalMessage(data *types.DigestData) string {
	if len(data.Items) == 0 {
		return ""
	}

	byCategory := make(map[string][]types.DigestItem)
	var catOrder []string
	seen := make(map[string]bool)
	for _, item := range data.Items {
		cat := item.Category
		if cat == "" {
			cat = "其他重要动态"
		}
		if !seen[cat] {
			catOrder = append(catOrder, cat)
			seen[cat] = true
		}
		byCategory[cat] = append(byCategory[cat], item)
	}

	sort.SliceStable(catOrder, func(i, j int) bool {
		ci, cj := catOrder[i], catOrder[j]
		for _, c := range types.CategoryOrder {
			if c == ci {
				return true
			}
			if c == cj {
				return false
			}
		}
		return false
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("当前时间：%s\n档位：%s\n\n", data.CurrentTime, data.SlotLabel))

	for _, cat := range catOrder {
		items := byCategory[cat]
		sb.WriteString(fmt.Sprintf("## %s\n\n", cat))
		for _, item := range items {
			summary := item.ItemSummary
			if summary == "" {
				summary = item.FactParagraph
			}
			sb.WriteString(fmt.Sprintf("- %s（%s）\n", summary, item.Source))
			for _, ref := range item.Refs {
				label := ref.DisplayTitle
				if ref.FactSummary != "" {
					label = ref.FactSummary
				}
				sb.WriteString(fmt.Sprintf("  [%s] %s（%s）\n", ref.RelationNote, label, ref.Source))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// historyFilePath returns the per-day history file path for the given date.
func (p *NewsPipeline) historyFilePath(t time.Time) string {
	return p.DataDir + "/push_history_" + t.Format("20060102") + ".jsonl"
}

// RecordHistoryFromDigest appends individual item records to today's history file.
// Computes embeddings for each item if Embedding client is available.
func (p *NewsPipeline) RecordHistoryFromDigest(ctx context.Context, items []types.DigestItem, slot string) error {
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
			summary := item.ItemSummary
			if summary == "" {
				summary = item.FactParagraph
			}
			texts[i] = item.DisplayTitle + " " + summary
		}
		vecs, err := CachedEmbedStrings(ctx, p, texts)
		if err == nil && len(vecs) == len(items) {
			embeddings = vecs
		}
	}

	nowStr := now.Format(time.RFC3339)
	for i, item := range items {
		record := types.PushHistoryRecord{
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
func (p *NewsPipeline) appendHistoryRecord(record types.PushHistoryRecord) error {
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
