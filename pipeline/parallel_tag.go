package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// parallelTagItems splits raw news items into batches, tags each batch
// concurrently via the LLM, and merges the results.
// Uses a per-day tag cache to avoid re-tagging already processed items.
func (p *NewsPipeline) parallelTagItems(ctx context.Context, items []RawNewsItem) ([]TaggedNewsItem, error) {
	if len(items) == 0 {
		return nil, nil
	}

	// Load cached tag results for today
	cached := p.loadTagCache()
	cachedByLink := make(map[string]TaggedNewsItem, len(cached))
	for _, item := range cached {
		link := strings.TrimSpace(item.Link)
		if link != "" {
			cachedByLink[link] = item
		}
	}

	// Split into cached and new items
	var newItems []RawNewsItem
	var taggedFromCache []TaggedNewsItem
	for _, item := range items {
		link := strings.TrimSpace(item.Link)
		if link != "" {
			if cached, ok := cachedByLink[link]; ok {
				taggedFromCache = append(taggedFromCache, cached)
				continue
			}
		}
		newItems = append(newItems, item)
	}

	if len(taggedFromCache) > 0 {
		log.Printf("[ParallelTag] 命中缓存: %d 条，待标注: %d 条", len(taggedFromCache), len(newItems))
	}

	// Tag only the new items
	var taggedNew []TaggedNewsItem
	if len(newItems) > 0 {
		result, err := p.tagNewItems(ctx, newItems)
		if err != nil {
			return nil, err
		}
		taggedNew = result

		// Append new results to cache
		if err := p.appendTagCache(taggedNew); err != nil {
			log.Printf("[ParallelTag] 写入标签缓存失败: %v", err)
		}
	}

	// Merge cached + newly tagged
	allTagged := make([]TaggedNewsItem, 0, len(taggedFromCache)+len(taggedNew))
	allTagged = append(allTagged, taggedFromCache...)
	allTagged = append(allTagged, taggedNew...)

	log.Printf("[ParallelTag] 完成: 缓存 %d + 新标注 %d = %d/%d 条",
		len(taggedFromCache), len(taggedNew), len(allTagged), len(items))
	return allTagged, nil
}

// tagNewItems performs LLM tagging on items that are not in the cache.
func (p *NewsPipeline) tagNewItems(ctx context.Context, items []RawNewsItem) ([]TaggedNewsItem, error) {
	// Load guide and examples once (shared across batches)
	guide, err := p.loadTaggingGuide()
	if err != nil {
		return nil, fmt.Errorf("load tagging guide: %w", err)
	}
	examples, err := p.loadTaggingExamples()
	if err != nil {
		return nil, fmt.Errorf("load tagging examples: %w", err)
	}

	// Build index of raw items by ID for merging
	rawByID := make(map[string]RawNewsItem, len(items))
	for _, item := range items {
		rawByID[item.ID] = item
	}

	// Split into batches
	batches := splitBatches(items, TagBatchSize)

	type batchResult struct {
		items []TaggedNewsItem
		err   error
		index int
	}

	results := make([]batchResult, len(batches))
	var wg sync.WaitGroup

	for i, batch := range batches {
		wg.Add(1)
		go func(idx int, b []RawNewsItem) {
			defer wg.Done()
			tagged, err := p.tagOneBatch(ctx, b, guide, examples, rawByID)
			results[idx] = batchResult{items: tagged, err: err, index: idx}
		}(i, batch)
	}
	wg.Wait()

	// Merge results in order
	var allTagged []TaggedNewsItem
	for i, r := range results {
		if r.err != nil {
			log.Printf("[ParallelTag] batch %d/%d failed: %v", i+1, len(batches), r.err)
			continue
		}
		allTagged = append(allTagged, r.items...)
	}

	if len(allTagged) == 0 {
		return nil, fmt.Errorf("all %d tag batches failed", len(batches))
	}

	return allTagged, nil
}

// loadTagCache reads the per-day tag cache file.
func (p *NewsPipeline) loadTagCache() []TaggedNewsItem {
	today := time.Now().Format("20060102")
	path := p.DataDir + "/tagged_cache_" + today + ".jsonl"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var items []TaggedNewsItem
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item TaggedNewsItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items
}

// appendTagCache appends newly tagged items to the per-day cache file.
func (p *NewsPipeline) appendTagCache(items []TaggedNewsItem) error {
	today := time.Now().Format("20060102")
	path := p.DataDir + "/tagged_cache_" + today + ".jsonl"

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open tag cache: %w", err)
	}
	defer f.Close()

	for _, item := range items {
		line, err := json.Marshal(item)
		if err != nil {
			continue
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// tagOneBatch formats a prompt, calls the LLM, and parses the result for one batch.
func (p *NewsPipeline) tagOneBatch(ctx context.Context, batch []RawNewsItem, guide, examples string, rawByID map[string]RawNewsItem) ([]TaggedNewsItem, error) {
	// 1. Format prompt variables
	vars, err := p.formatBatchTagPrompt(batch, guide, examples)
	if err != nil {
		return nil, fmt.Errorf("format prompt: %w", err)
	}

	// 2. Render template
	tagTpl, err := p.newTagChatTemplate()
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}
	messages, err := tagTpl.Format(ctx, vars)
	if err != nil {
		return nil, fmt.Errorf("render template: %w", err)
	}

	// 3. Call LLM
	resp, err := p.ChatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLM call: %w", err)
	}

	// 4. Parse result
	tagged, err := parseTagResultFromMessage(resp, rawByID)
	if err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}

	return tagged, nil
}

// TagBatchSize controls how many news items are sent to the LLM per batch.
const TagBatchSize = 15

// formatBatchTagPrompt builds template variables for one batch.
func (p *NewsPipeline) formatBatchTagPrompt(batch []RawNewsItem, guide, examples string) (map[string]any, error) {
	var sb strings.Builder
	for i, item := range batch {
		sb.WriteString(fmt.Sprintf("### [%d] %s\n", i+1, item.Title))
		sb.WriteString(fmt.Sprintf("- ID: %s\n", item.ID))
		sb.WriteString(fmt.Sprintf("- 来源: %s (%s)\n", item.Source, item.Lang))
		sb.WriteString(fmt.Sprintf("- 摘要: %s\n", item.Summary))
		sb.WriteString(fmt.Sprintf("- 发布时间: %s\n", item.PublishedAt))
		sb.WriteString(fmt.Sprintf("- 链接: %s\n\n", item.Link))
	}

	return map[string]any{
		"news_items":       sb.String(),
		"tagging_guide":    guide,
		"tagging_examples": examples,
		"total_count":      fmt.Sprintf("%d", len(batch)),
	}, nil
}

// parseTagResultFromMessage parses LLM output into TaggedNewsItems, merging raw fields.
func parseTagResultFromMessage(msg *schema.Message, rawByID map[string]RawNewsItem) ([]TaggedNewsItem, error) {
	content := msg.Content

	var results []tagResultItem
	err := json.Unmarshal([]byte(content), &results)
	if err != nil {
		extracted := extractJSONFromMarkdown(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		extracted := extractJSONArray(content)
		if extracted != "" {
			err = json.Unmarshal([]byte(extracted), &results)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM tag output as JSON: %w", err)
	}

	tagged := make([]TaggedNewsItem, 0, len(results))
	// Build title-to-raw index for fallback matching when ID doesn't match
	rawByTitle := make(map[string]RawNewsItem, len(rawByID))
	for _, raw := range rawByID {
		rawByTitle[raw.Title] = raw
	}

	for _, r := range results {
		item := TaggedNewsItem{
			RawNewsItem: RawNewsItem{
				ID: r.ID,
			},
			DisplayTitle:  r.DisplayTitle,
			Category:      normalizeCategory(r.Category),
			TopicTags:     parseTopicTags(r.TopicTags),
			Region:        r.Region,
			InterestScore: parseInterestScore(r.InterestScore),
			IsDuplicate:   parseBool(r.IsDuplicate, false),
			Selected:      parseBool(r.Selected, false),
			WhySelected:   r.WhySelected,
		}

		// Merge raw fields: try ID match first, then title match as fallback
		if raw, ok := rawByID[r.ID]; ok {
			item.RawNewsItem = raw
		} else if raw, ok := rawByTitle[r.DisplayTitle]; ok {
			item.RawNewsItem = raw
		} else if raw, ok := rawByTitle[r.ID]; ok {
			// LLM may put the title into the id field
			item.RawNewsItem = raw
		}
		if item.DisplayTitle == "" {
			item.DisplayTitle = item.Title
		}

		tagged = append(tagged, item)
	}
	return tagged, nil
}

// splitBatches splits items into batches of at most batchSize.
func splitBatches(items []RawNewsItem, batchSize int) [][]RawNewsItem {
	var batches [][]RawNewsItem
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batches = append(batches, items[i:end])
	}
	return batches
}
