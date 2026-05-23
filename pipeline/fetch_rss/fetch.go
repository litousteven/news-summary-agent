package fetch_rss

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"gopkg.in/yaml.v3"
)

type FeedSource struct {
	Name     string `yaml:"name" json:"name"`
	URL      string `yaml:"url" json:"url"`
	Lang     string `yaml:"lang" json:"lang"`
	UseProxy bool   `yaml:"use_proxy" json:"use_proxy"`
	Enabled  bool   `yaml:"enabled" json:"enabled"`
}

var SourceRank = map[string]int{
	"中新网":        0,
	"BBC":        1,
	"NPR":        2,
	"NYT":        3,
	"Al Jazeera": 4,
}

var DefaultFeeds = []FeedSource{
	{Name: "中新网", URL: "https://www.chinanews.com.cn/rss/world.xml", Lang: "zh", Enabled: true},
	{Name: "BBC", URL: "https://feeds.bbci.co.uk/news/world/rss.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "NPR", URL: "https://feeds.npr.org/1004/rss.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "NYT", URL: "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "联合早报", URL: "https://plink.anyfeeder.com/zaobao/realtime/world", Lang: "zh", Enabled: true},
	{Name: "联合早报-中国", URL: "https://plink.anyfeeder.com/zaobao/realtime/china", Lang: "zh", Enabled: true},
	{Name: "香港电台", URL: "https://rthk.hk/rthk/news/rss/c_expressnews_cinternational.xml", Lang: "zh"},
	{Name: "CNN", URL: "http://rss.cnn.com/rss/edition.rss", Lang: "en"},
	{Name: "Washington Post", URL: "https://feeds.washingtonpost.com/rss/world", Lang: "en", UseProxy: true},
	{Name: "NBC News", URL: "https://feeds.nbcnews.com/nbcnews/public/news", Lang: "en", UseProxy: true},
	{Name: "ABC News", URL: "https://abcnews.go.com/abcnews/topstories", Lang: "en", UseProxy: true},
	{Name: "FOX News", URL: "https://moxie.foxnews.com/google-publisher/world.xml", Lang: "en", UseProxy: true},
	{Name: "The Guardian", URL: "https://www.theguardian.com/world/rss", Lang: "en", UseProxy: true},
	{Name: "Financial Times", URL: "https://www.ft.com/rss/home", Lang: "en", UseProxy: true},
	{Name: "The Independent", URL: "https://www.independent.co.uk/rss", Lang: "en", UseProxy: true},
	{Name: "Sky News", URL: "https://feeds.skynews.com/feeds/rss/world.xml", Lang: "en", UseProxy: true},
	{Name: "France24", URL: "https://www.france24.com/en/rss", Lang: "en", UseProxy: true},
	{Name: "DW", URL: "https://rss.dw.com/rdf/rss-en-all", Lang: "en", UseProxy: true},
	{Name: "Japan Times", URL: "https://www.japantimes.co.jp/feed/", Lang: "en", UseProxy: true},
}

func LoadFeeds(configDir string) []FeedSource {
	allFeeds := DefaultFeeds

	if configDir != "" {
		path := filepath.Join(configDir, "feeds.yaml")
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

func FetchFeed(ctx context.Context, src FeedSource, proxyAddr string) ([]*gofeed.Item, error) {
	client := &http.Client{Timeout: 20 * time.Second}

	if src.UseProxy && proxyAddr != "" {
		proxyURL, err := url.Parse(proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %s: %w", proxyAddr, err)
		}
		client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", src.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", src.Name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", src.Name, err)
	}

	fp := gofeed.NewParser()
	feed, err := fp.ParseString(string(body))
	if err != nil {
		contentType := resp.Header.Get("Content-Type")
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		log.Printf("[FetchRSS] %s 解析失败: Content-Type=%s, 响应前200字节: %s", src.Name, contentType, snippet)
		return nil, fmt.Errorf("parse %s: %w", src.Name, err)
	}

	return feed.Items, nil
}

func GenerateItemID(source, title, link string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", source, title, link)))
	return fmt.Sprintf("%s-%x", source, h[:4])
}

var (
	htmlTagRe     = regexp.MustCompile(`<[^>]+>`)
	whitespaceRe2 = regexp.MustCompile(`\s+`)
)

func CleanHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(s)
	s = whitespaceRe2.ReplaceAllString(s, " ")
	return s
}
