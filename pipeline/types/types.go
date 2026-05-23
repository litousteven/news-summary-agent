package types

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
	Items          []RawNewsItem
	Representative int // index of the representative item
}

// NewsReference is a related news item, either from the same batch or from push history.
// RelationNote indicates the relationship: "相关", "前情回顾", "反转", etc.
type NewsReference struct {
	DisplayTitle string  `json:"display_title"`
	Source       string  `json:"source"`
	Link         string  `json:"link"`
	PushTime     string  `json:"push_time,omitempty"`    // only for history references
	FactSummary  string  `json:"fact_summary,omitempty"` // only for history references
	Similarity   float64 `json:"similarity,omitempty"`   // only for batch references
	RelationNote string  `json:"relation_note"`          // "相关" / "前情回顾" / "反转"
}

// MergedNewsItem is a tagged item enriched with push history info.
type MergedNewsItem struct {
	TaggedNewsItem
	SeenBefore  bool            `json:"seen_before"`
	HistoryNote string          `json:"history_note"`
	Refs        []NewsReference `json:"refs"`
	Links       []string        `json:"links,omitempty"`
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
	FactParagraph string `json:"fact_paragraph"`
	ItemSummary   string `json:"item_summary"`
}

// DigestStats contains pipeline statistics.
type DigestStats struct {
	TotalFetched  int            `json:"total_fetched"`
	TotalTagged   int            `json:"total_tagged"`
	TaggingFailed int            `json:"tagging_failed"`
	TotalSelected int            `json:"total_selected"`
	ByCategory    map[string]int `json:"by_category"`
}

// PushHistoryRecord is a single record in push_history.jsonl.
type PushHistoryRecord struct {
	PushTime     string    `json:"push_time"`
	Slot         string    `json:"slot"`
	DisplayTitle string    `json:"display_title"`
	Category     string    `json:"category"`
	Source       string    `json:"source"`
	PublishedAt  string    `json:"published_at"`
	Link         string    `json:"link"`
	FactSummary  string    `json:"fact_summary"`
	RawTitle     string    `json:"raw_title"`
	Embedding    []float64 `json:"embedding,omitempty"`
}

// NewsSummaryResult is the final output of the pipeline.
type NewsSummaryResult struct {
	Message     string       `json:"message"`
	Stats       DigestStats  `json:"stats"`
	DigestItems []DigestItem `json:"digest_items,omitempty"`
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

	// Set by FetchRSS (post-dedup), used by BuildDigest to track original fetch count
	OriginalFetchedCount int

	// Set by ParallelTag, used by BuildDigest to track actual tagged count
	ActualTaggedCount int
}

// CategoryDef represents a single category definition loaded from categories.json.
type CategoryDef struct {
	Name        string   `json:"name"`
	Keywords    []string `json:"keywords"`
	Boundary    string   `json:"boundary"`
	NotBoundary string   `json:"not_boundary"`
}

// DefaultCategories is the embedded fallback when categories.json is not found.
var DefaultCategories = []CategoryDef{
	{Name: "战争与地缘", Keywords: []string{"战争", "冲突", "停火", "袭击", "外交摩擦", "制裁", "使馆事件", "地区局势升级"}, Boundary: "战争、冲突、停火、袭击、外交摩擦、制裁、使馆事件、地区局势升级", NotBoundary: "单纯介绍某种武器性能 → 更可能是\"军事装备\"；单纯航天任务/火箭发射 → \"航空航天\""},
	{Name: "航空航天", Keywords: []string{"火箭", "卫星", "探测器", "航天计划", "航空工业", "民航"}, Boundary: "火箭、卫星、探测器、航天计划、重大航空工业动态、重大民航事件", NotBoundary: "军机参与作战、直升机伴飞、战斗飞行 → 通常不是\"航空航天\"主类"},
	{Name: "军事装备", Keywords: []string{"导弹", "舰艇", "无人机", "坦克", "武器系统", "军费", "军工部署", "防务体系"}, Boundary: "导弹、舰艇、无人机、坦克、武器系统、军费、军工部署、防务体系", NotBoundary: "事件重点是\"冲突爆发、局势升级、外交回应\" → 更应归\"战争与地缘\""},
	{Name: "AI与数码", Keywords: []string{"AI", "芯片", "半导体", "互联网平台", "消费电子", "机器人", "数码终端"}, Boundary: "AI 模型、芯片、半导体、互联网平台、消费电子、机器人、数码终端"},
	{Name: "新能源与汽车", Keywords: []string{"电动车", "电池", "车企", "充电", "自动驾驶", "能源转型"}, Boundary: "电动车、电池、车企、充电、自动驾驶、能源转型"},
	{Name: "全球经济", Keywords: []string{"通胀", "油价", "贸易", "粮食", "金融市场", "供应链", "产业链冲击"}, Boundary: "通胀、油价、贸易、粮食、金融市场、供应链、产业链冲击"},
	{Name: "国内事务", Keywords: []string{"国内政治", "两岸关系", "反腐", "官员被查", "国内政策", "社会发展"}, Boundary: "中国内政、社会发展、政策动向、两岸关系等重要新闻", NotBoundary: "不要误分到\"战争与地缘\"或\"其他重要动态\""},
	{Name: "其他重要动态", Keywords: []string{}, Boundary: "不属于以上分类，但仍值得进入简报的重要国际新闻"},
}

// CategoryOrder and ValidCategories are populated from the loaded categories.
// They are initialized from DefaultCategories and may be overridden by
// loadCategories() reading config/categories.json at runtime.
var (
	CategoryOrder   []string
	ValidCategories map[string]bool
	CategoryDefs    []CategoryDef
)

func init() {
	ReloadCategoryDefs(DefaultCategories)
}

// ReloadCategoryDefs rebuilds CategoryOrder and ValidCategories from a slice of CategoryDef.
func ReloadCategoryDefs(defs []CategoryDef) {
	CategoryDefs = defs
	CategoryOrder = make([]string, len(defs))
	ValidCategories = make(map[string]bool, len(defs))
	for i, c := range defs {
		CategoryOrder[i] = c.Name
		ValidCategories[c.Name] = true
	}
}

// Default constants (used when PipelineConfig fields are zero)
const (
	DefaultMaxItemsPerFeed  = 10
	DefaultMaxTotalItems    = 50
	DefaultMaxDigestItems   = 10
	DefaultMaxPerCategory   = 3
	DefaultClusterThreshold = 0.75
	DefaultFileExpiryDays   = 2

	// 标注批次相关默认值（一般无需调整）
	DefaultTagBatchSize             = 15
	DefaultTagMaxConcurrentBatches  = 2
	DefaultTagMaxRetries            = 2
	DefaultTagRetryBaseDelaySeconds = 3
	DefaultTagBatchTimeoutSeconds   = 120
)
