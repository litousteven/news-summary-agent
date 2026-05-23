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

	tagpkg "github.com/litousteven/news-summary-agent/pipeline/tag"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func (p *NewsPipeline) parallelTagItems(ctx context.Context, items []types.RawNewsItem) ([]types.TaggedNewsItem, error) {
	if len(items) == 0 {
		return nil, nil
	}

	cached := p.loadTagCache()
	cachedByLink := make(map[string]types.TaggedNewsItem, len(cached))
	for _, item := range cached {
		link := strings.TrimSpace(item.Link)
		if link != "" {
			cachedByLink[link] = item
		}
	}

	var newItems []types.RawNewsItem
	var taggedFromCache []types.TaggedNewsItem
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

	var taggedNew []types.TaggedNewsItem
	if len(newItems) > 0 {
		result, err := p.tagNewItems(ctx, newItems)
		if err != nil {
			return nil, err
		}
		taggedNew = result

		if err := p.appendTagCache(taggedNew); err != nil {
			log.Printf("[ParallelTag] 写入标签缓存失败: %v", err)
		}
	}

	allTagged := make([]types.TaggedNewsItem, 0, len(taggedFromCache)+len(taggedNew))
	allTagged = append(allTagged, taggedFromCache...)
	allTagged = append(allTagged, taggedNew...)

	log.Printf("[ParallelTag] 完成: 缓存 %d + 新标注 %d = %d/%d 条",
		len(taggedFromCache), len(taggedNew), len(allTagged), len(items))

	fetchedCount := len(items)
	taggedCount := len(allTagged)
	_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
		state.OriginalFetchedCount = fetchedCount
		state.ActualTaggedCount = taggedCount
		return nil
	})

	return allTagged, nil
}

func (p *NewsPipeline) tagNewItems(ctx context.Context, items []types.RawNewsItem) ([]types.TaggedNewsItem, error) {
	tagGraph, err := p.buildTagSubGraph(ctx)
	if err != nil {
		log.Printf("[ParallelTag] 构建TagSubGraph失败: error=%v", err)
		return nil, fmt.Errorf("build tag sub-graph: %w", err)
	}

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

	batchItems := toBatchItems(items)
	batches := tagpkg.SplitBatches(batchItems, p.GetTagBatchSize())

	type batchResult struct {
		items []types.TaggedNewsItem
		err   error
		index int
	}

	results := make([]batchResult, len(batches))
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.GetTagMaxConcurrentBatches())

	maxRetries := p.GetTagMaxRetries()
	retryBaseDelay := time.Duration(p.GetTagRetryBaseDelaySeconds()) * time.Second
	batchTimeout := time.Duration(p.GetTagBatchTimeoutSeconds()) * time.Second

	for i, batch := range batches {
		wg.Add(1)
		go func(idx int, b []tagpkg.BatchItem) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			vars := tagpkg.FormatBatchTagPromptVars(b, categoriesText, guide, examples)

			var tagged []types.TaggedNewsItem
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

				batchCtx, cancel := context.WithTimeout(ctx, batchTimeout)
				tagged, lastErr = tagGraph.Invoke(batchCtx, vars)
				cancel()

				if lastErr == nil {
					break
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

	var allTagged []types.TaggedNewsItem
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

func (p *NewsPipeline) loadTagCache() []types.TaggedNewsItem {
	today := time.Now().Format("20060102")
	path := p.DataDir + "/tagged_cache_" + today + ".jsonl"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var items []types.TaggedNewsItem
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item types.TaggedNewsItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (p *NewsPipeline) appendTagCache(items []types.TaggedNewsItem) error {
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

func toBatchItems(items []types.RawNewsItem) []tagpkg.BatchItem {
	result := make([]tagpkg.BatchItem, len(items))
	for i, item := range items {
		result[i] = tagpkg.BatchItem{
			ID:          item.ID,
			Source:      item.Source,
			Lang:        item.Lang,
			Title:       item.Title,
			Summary:     item.Summary,
			PublishedAt: item.PublishedAt,
			Link:        item.Link,
		}
	}
	return result
}

func parseTagResultFromMessage(msg *schema.Message, rawByID map[string]types.RawNewsItem) ([]types.TaggedNewsItem, error) {
	tagItems, err := tagpkg.ParseTagResultItems(msg.Content)
	if err != nil {
		contentPreview := msg.Content
		if len(contentPreview) > 500 {
			contentPreview = contentPreview[:500] + "..."
		}
		log.Printf("[ParseTagResult] 解析LLM输出失败: error=%v | content_preview=%q", err, contentPreview)
		return nil, fmt.Errorf("failed to parse LLM tag output as JSON: %w", err)
	}

	tagged := make([]types.TaggedNewsItem, 0, len(tagItems))
	rawByTitle := make(map[string]types.RawNewsItem, len(rawByID))
	for _, raw := range rawByID {
		rawByTitle[raw.Title] = raw
	}

	for _, r := range tagItems {
		item := types.TaggedNewsItem{
			RawNewsItem: types.RawNewsItem{
				ID: r.ID,
			},
			DisplayTitle:  r.DisplayTitle,
			Category:      normalizeCategoryInternal(r.Category),
			TopicTags:     tagpkg.ParseTopicTags(r.TopicTags),
			Region:        r.Region,
			InterestScore: tagpkg.ParseInterestScore(r.InterestScore),
			IsDuplicate:   tagpkg.ParseBool(r.IsDuplicate, false),
			Selected:      tagpkg.ParseBool(r.Selected, false),
			WhySelected:   r.WhySelected,
		}

		if raw, ok := rawByID[r.ID]; ok {
			item.RawNewsItem = raw
		} else if raw, ok := rawByTitle[r.DisplayTitle]; ok {
			item.RawNewsItem = raw
		} else if raw, ok := rawByTitle[r.ID]; ok {
			item.RawNewsItem = raw
		}
		if item.DisplayTitle == "" {
			item.DisplayTitle = item.Title
		}

		tagged = append(tagged, item)
	}
	return tagged, nil
}
