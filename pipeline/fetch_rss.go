package pipeline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/mmcdole/gofeed"
	"gopkg.in/yaml.v3"
)

// fetchRSS fetches RSS feeds from configured sources and returns structured news items.
func (p *NewsPipeline) fetchRSS(ctx context.Context, req *NewsSummaryRequest) ([]RawNewsItem, error) {
	// Save slot into shared state for downstream nodes (especially RecordHistory)
	_ = compose.ProcessState[*PipelineState](ctx, func(_ context.Context, state *PipelineState) error {
		state.Slot = req.Slot
		return nil
	})

	var items []RawNewsItem
	now := time.Now().UTC().Format(time.RFC3339)

	// Load feeds: try feeds.yaml first, fall back to DefaultFeeds
	feeds := p.loadFeeds()

	for _, feed := range feeds {
		fetched, err := p.fetchFeed(ctx, feed)
		if err != nil {
			log.Printf("[FetchRSS] feed=%s err=%v", feed.Name, err)
			continue
		}
		for i, item := range fetched {
			if i >= p.GetMaxItemsPerFeed() {
				break
			}
			summary := cleanHTML(item.Description)
			id := generateItemID(feed.Name, item.Title, item.Link)
			items = append(items, RawNewsItem{
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

// loadFeeds loads RSS feed sources from config/feeds.yaml if it exists,
// otherwise falls back to DefaultFeeds. Only returns enabled feeds.
func (p *NewsPipeline) loadFeeds() []FeedSource {
	allFeeds := DefaultFeeds

	if p.ConfigDir != "" {
		path := filepath.Join(p.ConfigDir, "feeds.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("[loadFeeds] 读取 %s 失败: %v，使用默认源", path, err)
			}
		} else {
			var feeds []FeedSource
			if err := yaml.Unmarshal(data, &feeds); err != nil {
				log.Printf("[loadFeeds] 解析 %s 失败: %v，使用默认源", path, err)
			} else if len(feeds) > 0 {
				allFeeds = feeds
				log.Printf("[loadFeeds] 从 %s 加载了 %d 个RSS源", path, len(feeds))
			} else {
				log.Printf("[loadFeeds] %s 为空，使用默认源", path)
			}
		}
	}

	// Filter to only enabled feeds
	var enabled []FeedSource
	for _, f := range allFeeds {
		if f.Enabled {
			enabled = append(enabled, f)
		}
	}

	if len(enabled) == 0 {
		log.Printf("[loadFeeds] 无启用的RSS源，使用默认源")
		return DefaultFeeds
	}

	log.Printf("[loadFeeds] 启用的RSS源: %d/%d", len(enabled), len(allFeeds))
	return enabled
}

// fetchFeed fetches and parses a single RSS feed.
func (p *NewsPipeline) fetchFeed(ctx context.Context, src FeedSource) ([]*gofeed.Item, error) {
	client := &http.Client{Timeout: 20 * time.Second}

	if src.UseProxy && p.ProxyAddr != "" {
		proxyURL, err := url.Parse(p.ProxyAddr)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %s: %w", p.ProxyAddr, err)
		}
		client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", src.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", src.Name, resp.StatusCode)
	}

	fp := gofeed.NewParser()
	feed, err := fp.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", src.Name, err)
	}

	return feed.Items, nil
}

// generateItemID creates a deterministic ID for a news item.
func generateItemID(source, title, link string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", source, title, link)))
	return fmt.Sprintf("%s-%x", source, h[:4])
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// cleanHTML strips HTML tags and unescapes entities.
func cleanHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(s)
	s = whitespaceRe.ReplaceAllString(s, " ")
	return s
}
