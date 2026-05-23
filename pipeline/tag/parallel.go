package tag

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

	"github.com/litousteven/news-summary-agent/pipeline/config"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func LoadTagCache(dataDir string) ([]types.TaggedNewsItem, error) {
	today := time.Now().Format("20060102")
	path := dataDir + "/tagged_cache_" + today + ".jsonl"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
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
	return items, nil
}

func AppendTagCache(dataDir string, items []types.TaggedNewsItem) error {
	today := time.Now().Format("20060102")
	path := dataDir + "/tagged_cache_" + today + ".jsonl"

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

func ToBatchItems(items []types.RawNewsItem) []BatchItem {
	result := make([]BatchItem, len(items))
	for i, item := range items {
		result[i] = BatchItem{
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

func TagNewItems(
	ctx context.Context,
	tagGraph compose.Runnable[map[string]any, []types.TaggedNewsItem],
	items []types.RawNewsItem,
	categoriesText string,
	guide string,
	examples string,
	cfg config.TagBatchConfig,
) ([]types.TaggedNewsItem, error) {
	batchItems := ToBatchItems(items)
	batches := SplitBatches(batchItems, cfg.BatchSize)

	type batchResult struct {
		items []types.TaggedNewsItem
		err   error
		index int
	}

	results := make([]batchResult, len(batches))
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.MaxConcurrentBatches)

	for i, batch := range batches {
		wg.Add(1)
		go func(idx int, b []BatchItem) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			log.Printf("[ParallelTag] batch %d/%d 开始（%d 条）", idx+1, len(batches), len(b))

			vars := FormatBatchTagPromptVars(b, categoriesText, guide, examples)

			var tagged []types.TaggedNewsItem
			var lastErr error
		retryLoop:
			for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
				if attempt > 0 {
					backoff := time.Duration(1<<(attempt-1)) * cfg.RetryBaseDelay
					log.Printf("[ParallelTag] batch %d/%d 第 %d 次重试（等待 %v）...",
						idx+1, len(batches), attempt, backoff)
					select {
					case <-ctx.Done():
						lastErr = ctx.Err()
						break retryLoop
					case <-time.After(backoff):
					}
				}

				batchCtx, cancel := context.WithTimeout(ctx, cfg.BatchTimeout)
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
					idx+1, len(batches), cfg.MaxRetries, lastErr, truncatedItems, vars["total_count"])
			} else {
				log.Printf("[ParallelTag] batch %d/%d 成功（%d 条标注）", idx+1, len(batches), len(tagged))
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

func ParallelTagItems(
	ctx context.Context,
	dataDir string,
	items []types.RawNewsItem,
	tagGraph compose.Runnable[map[string]any, []types.TaggedNewsItem],
	categoriesText string,
	guide string,
	examples string,
	cfg config.TagBatchConfig,
) (tagged []types.TaggedNewsItem, fromCache int, fromNew int, err error) {
	if len(items) == 0 {
		return nil, 0, 0, nil
	}

	cached, loadErr := LoadTagCache(dataDir)
	if loadErr != nil {
		log.Printf("[ParallelTag] 加载标签缓存失败: %v", loadErr)
	}

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
		result, tagErr := TagNewItems(ctx, tagGraph, newItems, categoriesText, guide, examples, cfg)
		if tagErr != nil {
			return nil, 0, 0, tagErr
		}
		taggedNew = result

		if appendErr := AppendTagCache(dataDir, taggedNew); appendErr != nil {
			log.Printf("[ParallelTag] 写入标签缓存失败: %v", appendErr)
		}
	}

	allTagged := make([]types.TaggedNewsItem, 0, len(taggedFromCache)+len(taggedNew))
	allTagged = append(allTagged, taggedFromCache...)
	allTagged = append(allTagged, taggedNew...)

	log.Printf("[ParallelTag] 完成: 缓存 %d + 新标注 %d = %d/%d 条",
		len(taggedFromCache), len(taggedNew), len(allTagged), len(items))

	return allTagged, len(taggedFromCache), len(taggedNew), nil
}
