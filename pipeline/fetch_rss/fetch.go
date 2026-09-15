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
	"strconv"
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

// SourceRank 是新闻源的质量档位，**数值越小越优先**。
//
// 这张表必须显式覆盖每一个在用的源。原因：未收录的源会取 Go map 的零值 0，
// 而 0 恰好是**最优档**——于是任何漏配的源都会在去重与编排里被当成比 NYT
// （rank 3）更好的来源。2026-09 之前只有 5 个源被收录，剩下的 17 个实际上
// 都在"顶配"档，导致「同一事件保留更高质来源」这条规则大部分时候是反的。
//
// 档位来源：0–11 沿用 feeds.yaml 里已经表达的偏好顺序；其余（从未表达过偏好、
// 或已确认停更的）统一用 UnknownSourceRank，等有明确判断时再调。
var SourceRank = map[string]int{
	// —— 在用的源，按 feeds.yaml 顺序 ——
	"中新网":             0,
	"BBC":             1,
	"NPR":             2,
	"NYT":             3,
	"Al Jazeera":      4, // 已配档位但当前未启用
	"中新网-中国":          5,
	"中新网-财经":          6,
	"ABC News":        7,
	"FOX News":        8,
	"Financial Times": 9,
	"France24":        10,
	"Japan Times":     11,
}

// UnknownSourceRank 是未在 SourceRank 中登记时的档位。
//
// 刻意不等于零值：零值是最优档，会让新加的源静默插到所有已评级源前面。
// 取一个偏后但非末尾的值，含义是"尚未评估"——会被采用，但排在已确认质量的
// 源之后。新来源稳定产出后再给它一个明确的档位。
const UnknownSourceRank = 20

// RankOf 返回某个源的质量档位，越小越优先。未登记的源得到 UnknownSourceRank。
func RankOf(source string) int {
	if rank, ok := SourceRank[source]; ok {
		return rank
	}
	return UnknownSourceRank
}

var DefaultFeeds = []FeedSource{
	{Name: "中新网", URL: "https://www.chinanews.com.cn/rss/world.xml", Lang: "zh", Enabled: true},
	{Name: "BBC", URL: "https://feeds.bbci.co.uk/news/world/rss.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "NPR", URL: "https://feeds.npr.org/1004/rss.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "NYT", URL: "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", Lang: "en", UseProxy: true, Enabled: true},
	// 中新网-中国 / 中新网-财经 用于替代已停更的 zaobao 代理源（见 feeds.yaml 注释）
	{Name: "中新网-中国", URL: "https://www.chinanews.com.cn/rss/china.xml", Lang: "zh", Enabled: true},
	{Name: "中新网-财经", URL: "https://www.chinanews.com.cn/rss/finance.xml", Lang: "zh", Enabled: true},
	// 联合早报两个源已停用：第三方代理 plink.anyfeeder.com 自 2026-07-29 起不再更新
	{Name: "联合早报", URL: "https://plink.anyfeeder.com/zaobao/realtime/world", Lang: "zh"},
	{Name: "联合早报-中国", URL: "https://plink.anyfeeder.com/zaobao/realtime/china", Lang: "zh"},
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

// HTTP 状态码提取，用于区分「永久失败」与「可重试的瞬时失败」。
var httpStatusRe = regexp.MustCompile(`status (\d{3})`)

// isPermanentFeedError 判断是否为不该重试的错误。
// 4xx 是源本身的问题（地址失效、被拒），重试没有意义；其余（连接重置、
// 超时、5xx、代理抖动）都值得重试。
func isPermanentFeedError(err error) bool {
	if err == nil {
		return false
	}
	m := httpStatusRe.FindStringSubmatch(err.Error())
	if len(m) < 2 {
		return false
	}
	code, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		return false
	}
	return code >= 400 && code < 500
}

// FetchFeedWithRetry 抓取一个源，瞬时失败时按线性退避重试。
//
// 抓取原本是一次性的：代理抖一下（connection reset）就整轮丢掉一个源，
// 而这一天剩下的几轮里该源的内容可能已经过期。标注阶段一直有重试，抓取阶段
// 却漏了。
func FetchFeedWithRetry(
	ctx context.Context,
	src FeedSource,
	proxyAddr string,
	maxRetries int,
	baseDelay time.Duration,
) ([]*gofeed.Item, error) {
	attempts := maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		items, err := FetchFeed(ctx, src, proxyAddr)
		if err == nil {
			if attempt > 1 {
				log.Printf("[FetchRSS] source=%s 第 %d 次尝试成功（前 %d 次失败）", src.Name, attempt, attempt-1)
			}
			return items, nil
		}
		lastErr = err

		if isPermanentFeedError(err) {
			log.Printf("[FetchRSS] source=%s 永久失败，不重试: %v", src.Name, err)
			return nil, err
		}
		if attempt == attempts {
			break
		}

		delay := time.Duration(attempt) * baseDelay
		log.Printf("[FetchRSS] source=%s 第 %d/%d 次失败: %v，%v 后重试",
			src.Name, attempt, attempts, err, delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, fmt.Errorf("%w（已尝试 %d 次）", lastErr, attempts)
}
