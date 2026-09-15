package pipeline

import (
	"context"
	"fmt"
	"html"
	"log"
	"time"

	"github.com/cloudwego/eino/compose"

	fetchrss "github.com/litousteven/news-summary-agent/pipeline/fetch_rss"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func (p *NewsPipeline) fetchRSS(ctx context.Context, req *types.NewsSummaryRequest) ([]types.RawNewsItem, error) {
	_ = compose.ProcessState[*types.PipelineState](ctx, func(_ context.Context, state *types.PipelineState) error {
		state.Slot = req.Slot
		return nil
	})

	var items []types.RawNewsItem
	now := time.Now()
	nowStr := now.UTC().Format(time.RFC3339)

	maxAgeDays := p.GetMaxNewsAgeDays()
	// 以「今天 00:00 往前推 maxAgeDays 天」为界，避免同一天内因运行时刻不同
	// 而时松时紧；发布时刻晚于该边界的条目一律保留。
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -maxAgeDays)

	feeds := fetchrss.LoadFeeds(p.ConfigDir)

	for _, feed := range feeds {
		fetched, err := fetchrss.FetchFeedWithRetry(ctx, feed, p.ProxyAddr,
			p.GetFeedMaxRetries(), time.Duration(p.GetFeedRetryBaseDelaySeconds())*time.Second)
		if err != nil {
			log.Printf("[FetchRSS] feed=%s 最终失败: %v", feed.Name, err)
			continue
		}
		var staleSkipped, undated int
		var newest time.Time
		for i, item := range fetched {
			if i >= p.GetMaxItemsPerFeed() {
				break
			}
			// 先记录该源最新条目（含过期条目），再决定取舍：一个完全死掉的
			// 源里全是过期条目，若只在「新鲜」分支里记录最新时间，恰恰会漏报。
			fresh, pub := freshnessOf(item.Published, cutoff)
			if !pub.IsZero() && pub.After(newest) {
				newest = pub
			}
			switch fresh {
			case freshnessStale:
				staleSkipped++
				continue
			case freshnessUndated:
				undated++
			}
			summary := fetchrss.CleanHTML(item.Description)
			if summary == "" {
				summary = fetchrss.CleanHTML(item.Content)
			}
			if summary == "" {
				log.Printf("[FetchRSS] source=%s title=%q 跳过（description和content均缺失）", feed.Name, item.Title)
				continue
			}
			if item.Description == "" && item.Content != "" {
				log.Printf("[FetchRSS] source=%s title=%q 摘要来自content:encoded（description为空）", feed.Name, item.Title)
			}
			id := fetchrss.GenerateItemID(feed.Name, item.Title, item.Link)
			items = append(items, types.RawNewsItem{
				ID:          id,
				Source:      feed.Name,
				Title:       html.UnescapeString(item.Title),
				Summary:     summary,
				Link:        item.Link,
				PublishedAt: item.Published,
				Lang:        feed.Lang,
				FetchedAt:   nowStr,
			})
			if len(items) >= p.GetMaxTotalItems() {
				break
			}
		}

		// 停更告警：源本身还在返回 200，但内容已经不再更新。没有这条日志时，
		// 一个死源可以安静地污染简报数周（见 2026-09-14 的 zaobao 事件）。
		if !newest.IsZero() {
			ageDays := int(now.Sub(newest).Hours() / 24)
			if ageDays > maxAgeDays {
				log.Printf("[FetchRSS] ⚠ 源可能已停更: source=%s 最新条目为 %d 天前 (%s)，已超过 %d 天阈值；该源本次贡献 0 条",
					feed.Name, ageDays, newest.Format("2006-01-02"), maxAgeDays)
			}
		}
		log.Printf("[FetchRSS] source=%s count=%d 跳过过期=%d 无日期=%d", feed.Name, len(fetched), staleSkipped, undated)

		if len(items) >= p.GetMaxTotalItems() {
			break
		}
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no news items fetched from any source")
	}

	return items, nil
}
