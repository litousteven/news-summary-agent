package pipeline

import "time"

// NewsSummaryRequest is the input to the pipeline.
type NewsSummaryRequest struct {
	Slot string // "00:00" / "12:00" / "18:00"
}

// RawNewsItem represents a single news item fetched from RSS.
type RawNewsItem struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Link        string `json:"link"`
	PublishedAt string `json:"published_at"`
	Lang        string `json:"lang"`
	FetchedAt   string `json:"fetched_at"`
}

// TaggedNewsItem is a news item enriched with LLM-generated tags.
type TaggedNewsItem struct {
	RawNewsItem
	DisplayTitle  string   `json:"display_title"`
	Category      string   `json:"category"`
	TopicTags     []string `json:"topic_tags"`
	Region        string   `json:"region"`
	EventKey      string   `json:"event_key"`
	InterestScore int      `json:"interest_score"`
	IsDuplicate   bool     `json:"is_duplicate"`
	Selected      bool     `json:"selected"`
	WhySelected   string   `json:"why_selected"`
}

// NewsCluster is a group of semantically similar news items.
type NewsCluster struct {
	Items         []RawNewsItem
	Representative int // index of the representative item
}

// MergedNewsItem is a tagged item enriched with push history info.
type MergedNewsItem struct {
	TaggedNewsItem
	SeenBefore      bool   `json:"seen_before"`
	ShouldPush      bool   `json:"should_push"`
	LastPushTime    string `json:"last_push_time"`
	LastFactSummary string `json:"last_fact_summary"`
	HistoryNote     string `json:"history_note"`
}

// DigestData is the structured digest ready for summarization.
type DigestData struct {
	Items       []DigestItem
	SlotLabel   string
	CurrentTime string
	Stats       DigestStats
}

// DigestItem is a single item in the digest.
type DigestItem struct {
	MergedNewsItem
	FactParagraph string
}

// DigestStats contains pipeline statistics.
type DigestStats struct {
	TotalFetched  int            `json:"total_fetched"`
	TotalTagged   int            `json:"total_tagged"`
	TotalSelected int            `json:"total_selected"`
	ByCategory    map[string]int `json:"by_category"`
}

// PushHistoryRecord is a single record in push_history.jsonl.
type PushHistoryRecord struct {
	PushTime     string `json:"push_time"`
	Slot         string `json:"slot"`
	EventKey     string `json:"event_key"`
	DisplayTitle string `json:"display_title"`
	Category     string `json:"category"`
	Source       string `json:"source"`
	PublishedAt  string `json:"published_at"`
	Link         string `json:"link"`
	FactSummary  string `json:"fact_summary"`
	RawTitle     string `json:"raw_title"`
	Embedding    []float64 `json:"embedding,omitempty"`
}

// NewsSummaryResult is the final output of the pipeline.
type NewsSummaryResult struct {
	Message string      `json:"message"`
	Stats   DigestStats `json:"stats"`
}

// PipelineState is the shared state flowing through all graph nodes.
type PipelineState struct {
	// Input
	Request *NewsSummaryRequest

	// Stage 1: FetchRSS
	RawItems []RawNewsItem

	// Stage 2: EmbedAndCluster
	Embeddings [][]float64
	Clusters   []NewsCluster

	// Stage 3: TagNews
	TaggedItems []TaggedNewsItem

	// Stage 4: MergeHistory
	MergedItems []MergedNewsItem

	// Stage 5: BuildDigest
	Digest *DigestData

	// Stage 6: Summarize
	SummaryText string

	// Stage 7: RecordHistory
	Result *NewsSummaryResult
}

// RSS feed configuration
type FeedSource struct {
	Name     string
	URL      string
	Lang     string
	UseProxy bool
}

// Categories and their priority order
var CategoryOrder = []string{
	"战争与地缘",
	"航空航天",
	"军事装备",
	"AI与数码",
	"新能源与汽车",
	"全球经济",
	"其他重要动态",
}

var ValidCategories = map[string]bool{}

func init() {
	for _, c := range CategoryOrder {
		ValidCategories[c] = true
	}
}

// Source ranking for dedup (lower = higher priority)
var SourceRank = map[string]int{
	"中新网":        0,
	"BBC":         1,
	"NPR":         2,
	"NYT":         3,
	"Al Jazeera":  4,
}

// Default RSS feeds
var DefaultFeeds = []FeedSource{
	{Name: "中新网", URL: "https://www.chinanews.com.cn/rss/world.xml", Lang: "zh", UseProxy: false},
	{Name: "BBC", URL: "https://feeds.bbci.co.uk/news/world/rss.xml", Lang: "en", UseProxy: true},
	{Name: "NPR", URL: "https://feeds.npr.org/1004/rss.xml", Lang: "en", UseProxy: true},
	{Name: "NYT", URL: "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", Lang: "en", UseProxy: true},
	{Name: "Al Jazeera", URL: "https://www.aljazeera.com/xml/rss/all.xml", Lang: "en", UseProxy: true},
}

// Constants
const (
	MaxItemsPerFeed  = 10
	MaxTotalItems    = 50
	MaxDigestItems   = 10
	MaxPerCategory   = 3
	ClusterThreshold = 0.85
	HistoryLookback  = 24 * time.Hour
)
