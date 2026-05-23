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
	now := time.Now().UTC().Format(time.RFC3339)

	feeds := fetchrss.LoadFeeds(p.ConfigDir)

	for _, feed := range feeds {
		fetched, err := fetchrss.FetchFeed(ctx, feed, p.ProxyAddr)
		if err != nil {
			log.Printf("[FetchRSS] feed=%s err=%v", feed.Name, err)
			continue
		}
		for i, item := range fetched {
			if i >= p.GetMaxItemsPerFeed() {
				break
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
				FetchedAt:   now,
			})
			if len(items) >= p.GetMaxTotalItems() {
				break
			}
		}
		if len(items) >= p.GetMaxTotalItems() {
			break
		}
		log.Printf("[FetchRSS] source=%s count=%d", feed.Name, len(fetched))
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no news items fetched from any source")
	}

	return items, nil
}
