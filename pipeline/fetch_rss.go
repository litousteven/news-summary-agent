package pipeline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

// fetchRSS fetches RSS feeds from configured sources and returns structured news items.
func (p *NewsPipeline) fetchRSS(ctx context.Context, req *NewsSummaryRequest) ([]RawNewsItem, error) {
	var items []RawNewsItem
	now := time.Now().UTC().Format(time.RFC3339)

	for _, feed := range DefaultFeeds {
		fetched, err := p.fetchFeed(ctx, feed)
		if err != nil {
			log.Printf("[FetchRSS] feed=%s err=%v", feed.Name, err)
			continue
		}
		for i, item := range fetched {
			if i >= MaxItemsPerFeed {
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
			if len(items) >= MaxTotalItems {
				break
			}
		}
		if len(items) >= MaxTotalItems {
			break
		}
		log.Printf("[FetchRSS] source=%s count=%d", feed.Name, len(fetched))
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no news items fetched from any source")
	}

	return items, nil
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
	// collapse whitespace
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return s
}
