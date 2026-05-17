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

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// parallelTagItems splits raw news items into batches, tags each batch
// concurrently via the tag sub-graph, and merges the results.
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

	// Save counts to pipeline state for final stats
	fetchedCount := len(items)
	taggedCount := len(allTagged)
	_ = compose.ProcessState[*PipelineState](ctx, func(_ context.Context, state *PipelineState) error {
		state.OriginalFetchedCount = fetchedCount
		state.ActualTaggedCount = taggedCount
		return nil
	})

	return allTagged, nil
}

// tagNewItems performs LLM tagging on items that are not in the cache.
// It splits items into batches and runs the tag sub-graph concurrently for each batch
// with a concurrency limiter, per-batch timeout, and retry with exponential backoff.
// Failed batches after all retries are logged and their items are dropped.
func (p *NewsPipeline) tagNewItems(ctx context.Context, items []RawNewsItem) ([]TaggedNewsItem, error) {
	// Build the tag sub-graph once (shared across all batches)
	tagGraph, err := p.buildTagSubGraph(ctx)
	if err != nil {
		log.Printf("[ParallelTag] 构建TagSubGraph失败: error=%v", err)
		return nil, fmt.Errorf("build tag sub-graph: %w", err)
	}

	// Load prompt content once (shared across batches)
	categories, _ := p.loadCategories()
	categoriesText := formatCategoriesForPrompt(categories)
	guide, err := p.loadTaggingGuide()
	if err != nil {
		log.Printf("[ParallelTag] 加载tagging_guide失败: error=%v", err)
		return nil, fmt.Errorf("load tagging guide: %w", err)
	}
	examples, err := p.loadTaggingExamples()
	if err != nil {
		log.Printf("[ParallelTag] 加载tagging_examples失败: error=%v", err)
		return nil, fmt.Errorf("load tagging examples: %w", err)
	}

	// Split into batches
	batches := splitBatches(items, p.GetTagBatchSize())

	type batchResult struct {
		items []TaggedNewsItem
		err   error
		index int
	}

	results := make([]batchResult, len(batches))
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.GetTagMaxConcurrentBatches()) // concurrency limiter

	// Read tag config once for use in goroutines
	maxRetries := p.GetTagMaxRetries()
	retryBaseDelay := time.Duration(p.GetTagRetryBaseDelaySeconds()) * time.Second
	batchTimeout := time.Duration(p.GetTagBatchTimeoutSeconds()) * time.Second

	for i, batch := range batches {
		wg.Add(1)
		go func(idx int, b []RawNewsItem) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			vars := p.formatBatchTagPromptVars(b, categoriesText, guide, examples)

			// Retry loop with exponential backoff
			var tagged []TaggedNewsItem
			var lastErr error
		retryLoop:
			for attempt := 0; attempt <= maxRetries; attempt++ {
				if attempt > 0 {
					backoff := time.Duration(1<<(attempt-1)) * retryBaseDelay
					log.Printf("[ParallelTag] batch %d/%d 第 %d 次重试（等待 %v）...",
						idx+1, len(batches), attempt, backoff)
					select {
					case <-ctx.Done():
						lastErr = ctx.Err()
						break retryLoop
					case <-time.After(backoff):
					}
				}

				// Create a per-attempt timeout context
				batchCtx, cancel := context.WithTimeout(ctx, batchTimeout)
				tagged, lastErr = tagGraph.Invoke(batchCtx, vars)
				cancel()

				if lastErr == nil {
					break // success
				}
				newsItemsStr, _ := vars["news_items"].(string)
				truncatedItems := newsItemsStr
				if len(truncatedItems) > 300 {
					truncatedItems = truncatedItems[:300] + "..."
				}
				log.Printf("[ParallelTag] batch %d/%d 第 %d 次尝试失败: error=%v | items_preview=%q",
					idx+1, len(batches), attempt+1, lastErr, truncatedItems)
			}

			if lastErr != nil {
				newsItemsStr, _ := vars["news_items"].(string)
				truncatedItems := newsItemsStr
				if len(truncatedItems) > 500 {
					truncatedItems = truncatedItems[:500] + "..."
				}
				log.Printf("[ParallelTag] batch %d/%d 失败（已丢弃，重试 %d 次后仍失败）: error=%v | items_preview=%q | total_count=%v",
					idx+1, len(batches), maxRetries, lastErr, truncatedItems, vars["total_count"])
			}
			results[idx] = batchResult{items: tagged, err: lastErr, index: idx}
		}(i, batch)
	}
	wg.Wait()

	// Merge results in order (failed batches are skipped)
	var allTagged []TaggedNewsItem
	for _, r := range results {
		if r.err != nil {
			continue
		}
		allTagged = append(allTagged, r.items...)
	}

	if len(allTagged) == 0 {
		log.Printf("[ParallelTag] 所有 %d 个批次均失败；继续处理，无新标注项", len(batches))
		return nil, nil
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

// formatBatchTagPromptVars builds template variables for one batch.
func (p *NewsPipeline) formatBatchTagPromptVars(batch []RawNewsItem, categoriesText, guide, examples string) map[string]any {
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
		"categories":       categoriesText,
		"tagging_guide":    guide,
		"tagging_examples": examples,
		"total_count":      fmt.Sprintf("%d", len(batch)),
	}
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
		// JSON mode may return a single object instead of an array
		var single tagResultItem
		if err2 := json.Unmarshal([]byte(content), &single); err2 == nil {
			results = []tagResultItem{single}
			err = nil
		}
	}
	if err != nil {
		extracted := extractJSONArray(content)
		if extracted != "" {
			var single tagResultItem
			if err2 := json.Unmarshal([]byte(extracted), &single); err2 == nil {
				results = []tagResultItem{single}
				err = nil
			}
		}
	}
	if err != nil {
		contentPreview := content
		if len(contentPreview) > 500 {
			contentPreview = contentPreview[:500] + "..."
		}
		log.Printf("[ParseTagResult] 解析LLM输出失败: error=%v | content_preview=%q", err, contentPreview)
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
