package pipeline
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

// NewsReference is a related news item, either from the same batch or from push history.
// RelationNote indicates the relationship: "相关报道", "后续进展", "反转", etc.
type NewsReference struct {
	DisplayTitle string  `json:"display_title"`
	Source       string  `json:"source"`
	Link         string  `json:"link"`
	PushTime     string  `json:"push_time,omitempty"` // only for history references
	FactSummary  string  `json:"fact_summary,omitempty"` // only for history references
	Similarity   float64 `json:"similarity,omitempty"`   // only for batch references
	RelationNote string  `json:"relation_note"`           // "相关报道" / "后续进展" / "反转"
}

// MergedNewsItem is a tagged item enriched with push history info.
type MergedNewsItem struct {
	TaggedNewsItem
	SeenBefore  bool             `json:"seen_before"`
	HistoryNote string           `json:"history_note"`
	Refs        []NewsReference  `json:"refs"`
	Links       []string         `json:"links,omitempty"`
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
	Message     string        `json:"message"`
	Stats       DigestStats   `json:"stats"`
	DigestItems []DigestItem  `json:"digest_items,omitempty"`
}

// PipelineState is the shared state flowing through all graph nodes.
// Used via compose.WithGenLocalState to allow downstream nodes to access
// data produced by earlier nodes that is not passed through the linear edge.
type PipelineState struct {
	// Set by FetchRSS, consumed by ParseTagResult to merge raw fields
	RawItems []RawNewsItem

	// Set by BuildDigest, consumed by RecordHistory to write per-item records
	DigestItems []DigestItem

	// Set by BuildDigest, consumed by RecordHistory to populate result stats
	DigestStats *DigestStats

	// The slot from the original request
	Slot string
}

// RSS feed configuration
type FeedSource struct {
	Name     string `yaml:"name" json:"name"`
	URL      string `yaml:"url" json:"url"`
	Lang     string `yaml:"lang" json:"lang"`
	UseProxy bool   `yaml:"use_proxy" json:"use_proxy"`
	Enabled  bool   `yaml:"enabled" json:"enabled"`
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

// Default RSS feeds (used when feeds.yaml is not found)
// Only Enabled=true feeds are active.
var DefaultFeeds = []FeedSource{
	{Name: "中新网", URL: "https://www.chinanews.com.cn/rss/world.xml", Lang: "zh", Enabled: true},
	{Name: "BBC", URL: "https://feeds.bbci.co.uk/news/world/rss.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "NPR", URL: "https://feeds.npr.org/1004/rss.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "NYT", URL: "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", Lang: "en", UseProxy: true, Enabled: true},
	{Name: "联合早报", URL: "https://www.zaobao.com.sg/rss/news.xml", Lang: "zh", Enabled: true},
	// Candidate sources (disabled by default)
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

// Default constants (used when PipelineConfig fields are zero)
const (
	DefaultMaxItemsPerFeed  = 10
	DefaultMaxTotalItems    = 50
	DefaultMaxDigestItems   = 10
	DefaultMaxPerCategory   = 3
	DefaultClusterThreshold = 0.75
	DefaultFileExpiryDays  = 2
)
