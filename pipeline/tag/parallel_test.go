package tag

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/litousteven/news-summary-agent/pipeline/config"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func TestLoadTagCache_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	items, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestLoadTagCache_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	today := time.Now().Format("20060102")
	path := tmpDir + "/tagged_cache_" + today + ".jsonl"

	data := `{"id":"test-1","source":"BBC","title":"Test Title","summary":"Test Summary","link":"https://example.com/1","published_at":"2025-01-01T00:00:00Z","lang":"en","display_title":"测试标题","category":"AI与数码","topic_tags":["AI"],"region":"北美","interest_score":8,"is_duplicate":false,"selected":true,"why_selected":"测试"}
{"id":"test-2","source":"CNN","title":"Test 2","summary":"Test Summary 2","link":"https://example.com/2","published_at":"2025-01-01T00:00:00Z","lang":"en","display_title":"测试标题2","category":"战争与地缘","topic_tags":["战争"],"region":"中东","interest_score":9,"is_duplicate":false,"selected":true,"why_selected":"测试"}
`

	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write test cache: %v", err)
	}

	items, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].ID != "test-1" {
		t.Errorf("items[0].ID: got %q, want %q", items[0].ID, "test-1")
	}
	if items[0].DisplayTitle != "测试标题" {
		t.Errorf("items[0].DisplayTitle: got %q, want %q", items[0].DisplayTitle, "测试标题")
	}
	if items[0].Category != "AI与数码" {
		t.Errorf("items[0].Category: got %q, want %q", items[0].Category, "AI与数码")
	}
	if items[0].Source != "BBC" {
		t.Errorf("items[0].Source: got %q, want %q", items[0].Source, "BBC")
	}
	if items[0].Link != "https://example.com/1" {
		t.Errorf("items[0].Link: got %q, want %q", items[0].Link, "https://example.com/1")
	}
	if !items[0].Selected {
		t.Error("items[0].Selected should be true")
	}

	if items[1].ID != "test-2" {
		t.Errorf("items[1].ID: got %q, want %q", items[1].ID, "test-2")
	}
	if items[1].Category != "战争与地缘" {
		t.Errorf("items[1].Category: got %q, want %q", items[1].Category, "战争与地缘")
	}
	if items[1].InterestScore != 9 {
		t.Errorf("items[1].InterestScore: got %d, want 9", items[1].InterestScore)
	}
}

func TestLoadTagCache_MalformedLines(t *testing.T) {
	tmpDir := t.TempDir()
	today := time.Now().Format("20060102")
	path := tmpDir + "/tagged_cache_" + today + ".jsonl"

	data := `{"id":"valid-1","source":"BBC","title":"Valid","summary":"ok","link":"https://example.com","published_at":"2025-01-01T00:00:00Z","lang":"en","display_title":"有效","category":"AI与数码","topic_tags":[],"region":"北美","interest_score":5,"is_duplicate":false,"selected":true,"why_selected":""}
not-valid-json
{"id":"valid-2","source":"CNN","title":"Valid 2","summary":"ok","link":"https://example.com/2","published_at":"2025-01-01T00:00:00Z","lang":"en","display_title":"有效2","category":"战争与地缘","topic_tags":[],"region":"中东","interest_score":6,"is_duplicate":false,"selected":true,"why_selected":""}
`

	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write test cache: %v", err)
	}

	items, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items (malformed line skipped), got %d", len(items))
	}
	if items[0].ID != "valid-1" {
		t.Errorf("items[0].ID: got %q, want %q", items[0].ID, "valid-1")
	}
	if items[1].ID != "valid-2" {
		t.Errorf("items[1].ID: got %q, want %q", items[1].ID, "valid-2")
	}
}

func TestLoadTagCache_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	today := time.Now().Format("20060102")
	path := tmpDir + "/tagged_cache_" + today + ".jsonl"

	if err := os.WriteFile(path, []byte("\n\n"), 0644); err != nil {
		t.Fatalf("write test cache: %v", err)
	}

	items, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestAppendTagCache_NewFile(t *testing.T) {
	tmpDir := t.TempDir()

	items := []types.TaggedNewsItem{
		{
			RawNewsItem: types.RawNewsItem{
				ID:          "append-1",
				Source:      "BBC",
				Title:       "Append Test",
				Summary:     "Summary",
				Link:        "https://example.com",
				PublishedAt: "2025-01-01T00:00:00Z",
				Lang:        "en",
			},
			DisplayTitle:  "追加测试",
			Category:      "AI与数码",
			TopicTags:     []string{"AI"},
			Region:        "北美",
			InterestScore: 7,
			Selected:      true,
			WhySelected:   "test",
		},
	}

	if err := AppendTagCache(tmpDir, items); err != nil {
		t.Fatalf("AppendTagCache: %v", err)
	}

	loaded, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("LoadTagCache: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 item, got %d", len(loaded))
	}
	if loaded[0].ID != "append-1" {
		t.Errorf("ID: got %q, want %q", loaded[0].ID, "append-1")
	}
	if loaded[0].DisplayTitle != "追加测试" {
		t.Errorf("DisplayTitle: got %q, want %q", loaded[0].DisplayTitle, "追加测试")
	}
}

func TestAppendTagCache_AppendToExisting(t *testing.T) {
	tmpDir := t.TempDir()

	first := []types.TaggedNewsItem{
		{
			RawNewsItem:  types.RawNewsItem{ID: "first", Source: "BBC", Title: "First", Summary: "s", Link: "https://x.com/1", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
			DisplayTitle: "第一", Category: "AI与数码", InterestScore: 5, Selected: true,
		},
	}
	if err := AppendTagCache(tmpDir, first); err != nil {
		t.Fatalf("first AppendTagCache: %v", err)
	}

	second := []types.TaggedNewsItem{
		{
			RawNewsItem:  types.RawNewsItem{ID: "second", Source: "CNN", Title: "Second", Summary: "s", Link: "https://x.com/2", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
			DisplayTitle: "第二", Category: "战争与地缘", InterestScore: 8, Selected: true,
		},
	}
	if err := AppendTagCache(tmpDir, second); err != nil {
		t.Fatalf("second AppendTagCache: %v", err)
	}

	loaded, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("LoadTagCache: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 items, got %d", len(loaded))
	}
	if loaded[0].ID != "first" {
		t.Errorf("loaded[0].ID: got %q, want %q", loaded[0].ID, "first")
	}
	if loaded[1].ID != "second" {
		t.Errorf("loaded[1].ID: got %q, want %q", loaded[1].ID, "second")
	}
}

func TestAppendTagCache_EmptyItems(t *testing.T) {
	tmpDir := t.TempDir()

	if err := AppendTagCache(tmpDir, nil); err != nil {
		t.Fatalf("AppendTagCache with nil: %v", err)
	}
	if err := AppendTagCache(tmpDir, []types.TaggedNewsItem{}); err != nil {
		t.Fatalf("AppendTagCache with empty: %v", err)
	}
}

func TestToBatchItems_ConvertsAllFields(t *testing.T) {
	raw := []types.RawNewsItem{
		{
			ID:          "id-1",
			Source:      "BBC",
			Lang:        "en",
			Title:       "News Title",
			Summary:     "News Summary",
			PublishedAt: "2025-01-01T00:00:00Z",
			Link:        "https://example.com",
			FetchedAt:   "2025-01-01T01:00:00Z",
		},
		{
			ID:          "id-2",
			Source:      "CNN中文",
			Lang:        "zh",
			Title:       "新闻标题",
			Summary:     "新闻摘要",
			PublishedAt: "2025-01-01T02:00:00Z",
			Link:        "https://example.com/2",
			FetchedAt:   "2025-01-01T01:00:00Z",
		},
	}

	batchItems := ToBatchItems(raw)
	if len(batchItems) != 2 {
		t.Fatalf("expected 2 batch items, got %d", len(batchItems))
	}

	if batchItems[0].ID != "id-1" {
		t.Errorf("batchItems[0].ID: got %q, want %q", batchItems[0].ID, "id-1")
	}
	if batchItems[0].Source != "BBC" {
		t.Errorf("batchItems[0].Source: got %q, want %q", batchItems[0].Source, "BBC")
	}
	if batchItems[0].Lang != "en" {
		t.Errorf("batchItems[0].Lang: got %q, want %q", batchItems[0].Lang, "en")
	}
	if batchItems[0].Title != "News Title" {
		t.Errorf("batchItems[0].Title: got %q, want %q", batchItems[0].Title, "News Title")
	}
	if batchItems[0].Summary != "News Summary" {
		t.Errorf("batchItems[0].Summary: got %q, want %q", batchItems[0].Summary, "News Summary")
	}
	if batchItems[0].PublishedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("batchItems[0].PublishedAt: got %q, want %q", batchItems[0].PublishedAt, "2025-01-01T00:00:00Z")
	}
	if batchItems[0].Link != "https://example.com" {
		t.Errorf("batchItems[0].Link: got %q, want %q", batchItems[0].Link, "https://example.com")
	}

	if batchItems[1].ID != "id-2" {
		t.Errorf("batchItems[1].ID: got %q, want %q", batchItems[1].ID, "id-2")
	}
	if batchItems[1].Title != "新闻标题" {
		t.Errorf("batchItems[1].Title: got %q, want %q", batchItems[1].Title, "新闻标题")
	}
}

func TestToBatchItems_EmptySlice(t *testing.T) {
	items := ToBatchItems(nil)
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	items = ToBatchItems([]types.RawNewsItem{})
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

type mockTagGraph struct {
	results []types.TaggedNewsItem
	err     error
	callCnt *int32
}

func (m *mockTagGraph) Invoke(ctx context.Context, input map[string]any, opts ...compose.Option) ([]types.TaggedNewsItem, error) {
	if m.callCnt != nil {
		atomic.AddInt32(m.callCnt, 1)
	}
	return m.results, m.err
}

func (m *mockTagGraph) Stream(ctx context.Context, input map[string]any, opts ...compose.Option) (*schema.StreamReader[[]types.TaggedNewsItem], error) {
	return nil, nil
}

func (m *mockTagGraph) Collect(ctx context.Context, input *schema.StreamReader[map[string]any], opts ...compose.Option) ([]types.TaggedNewsItem, error) {
	return nil, nil
}

func (m *mockTagGraph) Transform(ctx context.Context, input *schema.StreamReader[map[string]any], opts ...compose.Option) (*schema.StreamReader[[]types.TaggedNewsItem], error) {
	return nil, nil
}

func TestTagNewItems_SingleBatchSuccess(t *testing.T) {
	mockResult := []types.TaggedNewsItem{
		{
			RawNewsItem:   types.RawNewsItem{ID: "item-1"},
			DisplayTitle:  "测试标题1",
			Category:      "AI与数码",
			InterestScore: 8,
			Selected:      true,
		},
		{
			RawNewsItem:   types.RawNewsItem{ID: "item-2"},
			DisplayTitle:  "测试标题2",
			Category:      "战争与地缘",
			InterestScore: 9,
			Selected:      true,
		},
	}

	graph := &mockTagGraph{results: mockResult}
	items := []types.RawNewsItem{
		{ID: "item-1", Source: "BBC", Title: "News 1", Summary: "S1", Link: "https://x.com/1", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
		{ID: "item-2", Source: "CNN", Title: "News 2", Summary: "S2", Link: "https://x.com/2", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}

	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           2,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	result, err := TagNewItems(context.Background(), graph, items,
		"categories", "guide", "examples", cfg)
	if err != nil {
		t.Fatalf("TagNewItems: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
	if result[0].ID != "item-1" {
		t.Errorf("result[0].ID: got %q, want %q", result[0].ID, "item-1")
	}
	if result[0].DisplayTitle != "测试标题1" {
		t.Errorf("result[0].DisplayTitle: got %q, want %q", result[0].DisplayTitle, "测试标题1")
	}
	if result[1].ID != "item-2" {
		t.Errorf("result[1].ID: got %q, want %q", result[1].ID, "item-2")
	}
}

func TestTagNewItems_MultipleBatches(t *testing.T) {
	var callCnt int32
	mockResult := []types.TaggedNewsItem{
		{RawNewsItem: types.RawNewsItem{ID: "a"}, DisplayTitle: "A", Category: "AI与数码", InterestScore: 5, Selected: true},
	}
	graph := &mockTagGraph{results: mockResult, callCnt: &callCnt}

	items := make([]types.RawNewsItem, 5)
	for i := range items {
		items[i] = types.RawNewsItem{
			ID:          "item-" + string(rune('a'+i)),
			Source:      "Test",
			Title:       "News",
			Summary:     "S",
			Link:        "https://x.com/" + string(rune('a'+i)),
			PublishedAt: "2025-01-01T00:00:00Z",
			Lang:        "en",
		}
	}

	cfg := config.TagBatchConfig{
		BatchSize:            2,
		MaxConcurrentBatches: 2,
		MaxRetries:           1,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	result, err := TagNewItems(context.Background(), graph, items,
		"categories", "guide", "examples", cfg)
	if err != nil {
		t.Fatalf("TagNewItems: %v", err)
	}

	expectedBatches := 3
	if int(callCnt) != expectedBatches {
		t.Errorf("expected %d batches, got %d", expectedBatches, callCnt)
	}

	expectedItems := 3
	if len(result) != expectedItems {
		t.Errorf("expected %d items (3 batches × 1 mock result), got %d", expectedItems, len(result))
	}
}

func TestTagNewItems_RetryOnFailure(t *testing.T) {
	var callCnt int32
	failingGraph := &failingMockTagGraph{
		failCount: 2,
		successResult: []types.TaggedNewsItem{
			{RawNewsItem: types.RawNewsItem{ID: "retry-1"}, DisplayTitle: "Retry", Category: "AI与数码", InterestScore: 5, Selected: true},
		},
		callCnt: &callCnt,
	}

	items := []types.RawNewsItem{
		{ID: "retry-1", Source: "BBC", Title: "News", Summary: "S", Link: "https://x.com/1", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}

	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           3,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	result, err := TagNewItems(context.Background(), failingGraph, items,
		"categories", "guide", "examples", cfg)
	if err != nil {
		t.Fatalf("TagNewItems: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}
	if result[0].ID != "retry-1" {
		t.Errorf("result[0].ID: got %q, want %q", result[0].ID, "retry-1")
	}
}

func TestTagNewItems_AllBatchesFail(t *testing.T) {
	failingGraph := &mockTagGraph{err: context.DeadlineExceeded}

	items := []types.RawNewsItem{
		{ID: "fail-1", Source: "BBC", Title: "News", Summary: "S", Link: "https://x.com/1", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}
	// 更新测试用例中的TagBatchConfig类型引用为config.TagBatchConfig
	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	result, err := TagNewItems(context.Background(), failingGraph, items,
		"categories", "guide", "examples", cfg)
	if err != nil {
		t.Fatalf("expected no error (failed batches dropped), got %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 items, got %d", len(result))
	}
}

func TestTagNewItems_EmptyItems(t *testing.T) {
	graph := &mockTagGraph{results: nil}
	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	result, err := TagNewItems(context.Background(), graph, nil,
		"categories", "guide", "examples", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 items, got %d", len(result))
	}
}

func TestParallelTagItems_EmptyItems(t *testing.T) {
	tmpDir := t.TempDir()
	graph := &mockTagGraph{}
	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	tagged, fromCache, fromNew, err := ParallelTagItems(
		context.Background(), tmpDir, nil, graph,
		"categories", "guide", "examples", cfg,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tagged) != 0 {
		t.Errorf("expected 0 items, got %d", len(tagged))
	}
	if fromCache != 0 || fromNew != 0 {
		t.Errorf("expected 0/0, got %d/%d", fromCache, fromNew)
	}
}

func TestParallelTagItems_AllFromCache(t *testing.T) {
	tmpDir := t.TempDir()

	cachedItem := types.TaggedNewsItem{
		RawNewsItem: types.RawNewsItem{
			ID:          "cached-1",
			Source:      "BBC",
			Title:       "Cached News",
			Summary:     "S",
			Link:        "https://example.com/cached",
			PublishedAt: "2025-01-01T00:00:00Z",
			Lang:        "en",
		},
		DisplayTitle:  "缓存标题",
		Category:      "AI与数码",
		InterestScore: 8,
		Selected:      true,
	}
	if err := AppendTagCache(tmpDir, []types.TaggedNewsItem{cachedItem}); err != nil {
		t.Fatalf("AppendTagCache: %v", err)
	}

	items := []types.RawNewsItem{
		{ID: "cached-1", Source: "BBC", Title: "Cached News", Summary: "S", Link: "https://example.com/cached", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}

	graph := &mockTagGraph{}
	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	tagged, fromCache, fromNew, err := ParallelTagItems(
		context.Background(), tmpDir, items, graph,
		"categories", "guide", "examples", cfg,
	)
	if err != nil {
		t.Fatalf("ParallelTagItems: %v", err)
	}
	if len(tagged) != 1 {
		t.Fatalf("expected 1 item, got %d", len(tagged))
	}
	if fromCache != 1 {
		t.Errorf("fromCache: got %d, want 1", fromCache)
	}
	if fromNew != 0 {
		t.Errorf("fromNew: got %d, want 0", fromNew)
	}
	if tagged[0].ID != "cached-1" {
		t.Errorf("tagged[0].ID: got %q, want %q", tagged[0].ID, "cached-1")
	}
	if tagged[0].DisplayTitle != "缓存标题" {
		t.Errorf("tagged[0].DisplayTitle: got %q, want %q", tagged[0].DisplayTitle, "缓存标题")
	}
}

func TestParallelTagItems_PartialCache(t *testing.T) {
	tmpDir := t.TempDir()

	cachedItem := types.TaggedNewsItem{
		RawNewsItem: types.RawNewsItem{
			ID:          "cached-1",
			Source:      "BBC",
			Title:       "Cached",
			Summary:     "S",
			Link:        "https://example.com/cached",
			PublishedAt: "2025-01-01T00:00:00Z",
			Lang:        "en",
		},
		DisplayTitle:  "缓存",
		Category:      "AI与数码",
		InterestScore: 5,
		Selected:      true,
	}
	if err := AppendTagCache(tmpDir, []types.TaggedNewsItem{cachedItem}); err != nil {
		t.Fatalf("AppendTagCache: %v", err)
	}

	mockResult := []types.TaggedNewsItem{
		{RawNewsItem: types.RawNewsItem{ID: "new-1"}, DisplayTitle: "新", Category: "战争与地缘", InterestScore: 7, Selected: true},
	}

	graph := &mockTagGraph{results: mockResult}
	items := []types.RawNewsItem{
		{ID: "cached-1", Source: "BBC", Title: "Cached", Summary: "S", Link: "https://example.com/cached", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
		{ID: "new-1", Source: "CNN", Title: "New", Summary: "S", Link: "https://example.com/new", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}

	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	tagged, fromCache, fromNew, err := ParallelTagItems(
		context.Background(), tmpDir, items, graph,
		"categories", "guide", "examples", cfg,
	)
	if err != nil {
		t.Fatalf("ParallelTagItems: %v", err)
	}
	if len(tagged) != 2 {
		t.Fatalf("expected 2 items, got %d", len(tagged))
	}
	if fromCache != 1 {
		t.Errorf("fromCache: got %d, want 1", fromCache)
	}
	if fromNew != 1 {
		t.Errorf("fromNew: got %d, want 1", fromNew)
	}
}

func TestParallelTagItems_AllNew(t *testing.T) {
	tmpDir := t.TempDir()

	mockResult := []types.TaggedNewsItem{
		{RawNewsItem: types.RawNewsItem{ID: "new-1"}, DisplayTitle: "新1", Category: "AI与数码", InterestScore: 5, Selected: true},
		{RawNewsItem: types.RawNewsItem{ID: "new-2"}, DisplayTitle: "新2", Category: "战争与地缘", InterestScore: 6, Selected: true},
	}

	graph := &mockTagGraph{results: mockResult}
	items := []types.RawNewsItem{
		{ID: "new-1", Source: "BBC", Title: "N1", Summary: "S", Link: "https://x.com/1", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
		{ID: "new-2", Source: "CNN", Title: "N2", Summary: "S", Link: "https://x.com/2", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}

	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	tagged, fromCache, fromNew, err := ParallelTagItems(
		context.Background(), tmpDir, items, graph,
		"categories", "guide", "examples", cfg,
	)
	if err != nil {
		t.Fatalf("ParallelTagItems: %v", err)
	}
	if len(tagged) != 2 {
		t.Fatalf("expected 2 items, got %d", len(tagged))
	}
	if fromCache != 0 {
		t.Errorf("fromCache: got %d, want 0", fromCache)
	}
	if fromNew != 2 {
		t.Errorf("fromNew: got %d, want 2", fromNew)
	}

	loaded, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("LoadTagCache: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 cached items, got %d", len(loaded))
	}
}

func TestParallelTagItems_GraphError(t *testing.T) {
	tmpDir := t.TempDir()
	failingGraph := &mockTagGraph{err: context.DeadlineExceeded}

	items := []types.RawNewsItem{
		{ID: "fail-1", Source: "BBC", Title: "News", Summary: "S", Link: "https://x.com/1", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}

	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	tagged, fromCache, fromNew, err := ParallelTagItems(
		context.Background(), tmpDir, items, failingGraph,
		"categories", "guide", "examples", cfg,
	)
	if err != nil {
		t.Fatalf("expected no error (failed batches dropped), got %v", err)
	}
	if len(tagged) != 0 {
		t.Errorf("expected 0 items, got %d", len(tagged))
	}
	if fromCache != 0 || fromNew != 0 {
		t.Errorf("expected 0/0, got %d/%d", fromCache, fromNew)
	}
}

func TestParallelTagItems_CacheHitDifferentFromItems(t *testing.T) {
	tmpDir := t.TempDir()

	cachedItem := types.TaggedNewsItem{
		RawNewsItem: types.RawNewsItem{
			ID:          "cached-1",
			Source:      "BBC",
			Title:       "Cached",
			Summary:     "S",
			Link:        "https://example.com/same-link",
			PublishedAt: "2025-01-01T00:00:00Z",
			Lang:        "en",
		},
		DisplayTitle:  "缓存版本",
		Category:      "AI与数码",
		InterestScore: 9,
		Selected:      true,
	}
	if err := AppendTagCache(tmpDir, []types.TaggedNewsItem{cachedItem}); err != nil {
		t.Fatalf("AppendTagCache: %v", err)
	}

	items := []types.RawNewsItem{
		{ID: "different-id", Source: "Reuters", Title: "Different Title", Summary: "S", Link: "https://example.com/same-link", PublishedAt: "2025-01-01T00:00:00Z", Lang: "en"},
	}

	graph := &mockTagGraph{}
	cfg := config.TagBatchConfig{
		BatchSize:            10,
		MaxConcurrentBatches: 1,
		MaxRetries:           0,
		RetryBaseDelay:       10 * time.Millisecond,
		BatchTimeout:         5 * time.Second,
	}

	tagged, fromCache, fromNew, err := ParallelTagItems(
		context.Background(), tmpDir, items, graph,
		"categories", "guide", "examples", cfg,
	)
	if err != nil {
		t.Fatalf("ParallelTagItems: %v", err)
	}
	if len(tagged) != 1 {
		t.Fatalf("expected 1 item, got %d", len(tagged))
	}
	if fromCache != 1 {
		t.Errorf("fromCache: got %d, want 1", fromCache)
	}
	if fromNew != 0 {
		t.Errorf("fromNew: got %d, want 0", fromNew)
	}
	if tagged[0].ID != "cached-1" {
		t.Errorf("tagged[0].ID: got %q, want %q (should return cached version)", tagged[0].ID, "cached-1")
	}
}

func TestTagBatchConfig_Defaults(t *testing.T) {
	cfg := config.TagBatchConfig{}
	if cfg.BatchSize != 0 {
		t.Errorf("BatchSize: got %d, want 0", cfg.BatchSize)
	}
	if cfg.MaxConcurrentBatches != 0 {
		t.Errorf("MaxConcurrentBatches: got %d, want 0", cfg.MaxConcurrentBatches)
	}
}

type failingMockTagGraph struct {
	failCount     int32
	currentFail   int32
	successResult []types.TaggedNewsItem
	callCnt       *int32
}

func (m *failingMockTagGraph) Invoke(ctx context.Context, input map[string]any, opts ...compose.Option) ([]types.TaggedNewsItem, error) {
	if m.callCnt != nil {
		atomic.AddInt32(m.callCnt, 1)
	}
	current := atomic.AddInt32(&m.currentFail, 1)
	if current <= m.failCount {
		return nil, context.DeadlineExceeded
	}
	return m.successResult, nil
}

func (m *failingMockTagGraph) Stream(ctx context.Context, input map[string]any, opts ...compose.Option) (*schema.StreamReader[[]types.TaggedNewsItem], error) {
	return nil, nil
}

func (m *failingMockTagGraph) Collect(ctx context.Context, input *schema.StreamReader[map[string]any], opts ...compose.Option) ([]types.TaggedNewsItem, error) {
	return nil, nil
}

func (m *failingMockTagGraph) Transform(ctx context.Context, input *schema.StreamReader[map[string]any], opts ...compose.Option) (*schema.StreamReader[[]types.TaggedNewsItem], error) {
	return nil, nil
}

func TestLoadTagCache_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	original := []types.TaggedNewsItem{
		{
			RawNewsItem: types.RawNewsItem{
				ID:          "rt-1",
				Source:      "BBC",
				Title:       "Round",
				Summary:     "Trip",
				Link:        "https://example.com/rt",
				PublishedAt: "2025-01-01T00:00:00Z",
				Lang:        "en",
				FetchedAt:   "",
			},
			DisplayTitle:  "往返测试",
			Category:      "AI与数码",
			TopicTags:     []string{"AI", "测试"},
			Region:        "北美",
			InterestScore: 10,
			IsDuplicate:   false,
			Selected:      true,
			WhySelected:   "roundtrip test",
		},
	}

	if err := AppendTagCache(tmpDir, original); err != nil {
		t.Fatalf("AppendTagCache: %v", err)
	}

	loaded, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("LoadTagCache: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 item, got %d", len(loaded))
	}

	l := loaded[0]
	if l.ID != "rt-1" {
		t.Errorf("ID: got %q, want %q", l.ID, "rt-1")
	}
	if l.Source != "BBC" {
		t.Errorf("Source: got %q, want %q", l.Source, "BBC")
	}
	if l.Title != "Round" {
		t.Errorf("Title: got %q, want %q", l.Title, "Round")
	}
	if l.Summary != "Trip" {
		t.Errorf("Summary: got %q, want %q", l.Summary, "Trip")
	}
	if l.Link != "https://example.com/rt" {
		t.Errorf("Link: got %q, want %q", l.Link, "https://example.com/rt")
	}
	if l.PublishedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("PublishedAt: got %q, want %q", l.PublishedAt, "2025-01-01T00:00:00Z")
	}
	if l.Lang != "en" {
		t.Errorf("Lang: got %q, want %q", l.Lang, "en")
	}
	if l.DisplayTitle != "往返测试" {
		t.Errorf("DisplayTitle: got %q, want %q", l.DisplayTitle, "往返测试")
	}
	if l.Category != "AI与数码" {
		t.Errorf("Category: got %q, want %q", l.Category, "AI与数码")
	}
	joined := strings.Join(l.TopicTags, ",")
	if joined != "AI,测试" && joined != "测试,AI" {
		t.Errorf("TopicTags: got %v, want [AI, 测试]", l.TopicTags)
	}
	if l.Region != "北美" {
		t.Errorf("Region: got %q, want %q", l.Region, "北美")
	}
	if l.InterestScore != 10 {
		t.Errorf("InterestScore: got %d, want 10", l.InterestScore)
	}
	if l.IsDuplicate {
		t.Error("IsDuplicate should be false")
	}
	if !l.Selected {
		t.Error("Selected should be true")
	}
	if l.WhySelected != "roundtrip test" {
		t.Errorf("WhySelected: got %q, want %q", l.WhySelected, "roundtrip test")
	}
}

func TestLoadTagCache_VersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	today := time.Now().Format("20060102")
	yesterday := time.Now().Add(-24 * time.Hour).Format("20060102")

	oldPath := tmpDir + "/tagged_cache_" + yesterday + ".jsonl"
	oldData := `{"id":"old-1","source":"BBC","title":"Old","summary":"s","link":"https://x.com","published_at":"2025-01-01T00:00:00Z","lang":"en","display_title":"旧","category":"AI与数码","topic_tags":[],"region":"北美","interest_score":5,"is_duplicate":false,"selected":true,"why_selected":""}` + "\n"
	if err := os.WriteFile(oldPath, []byte(oldData), 0644); err != nil {
		t.Fatalf("write old cache: %v", err)
	}

	todayPath := tmpDir + "/tagged_cache_" + today + ".jsonl"
	todayData := `{"id":"today-1","source":"CNN","title":"Today","summary":"s","link":"https://x.com/2","published_at":"2025-01-01T00:00:00Z","lang":"en","display_title":"今","category":"战争与地缘","topic_tags":[],"region":"中东","interest_score":6,"is_duplicate":false,"selected":true,"why_selected":""}` + "\n"
	if err := os.WriteFile(todayPath, []byte(todayData), 0644); err != nil {
		t.Fatalf("write today cache: %v", err)
	}

	items, err := LoadTagCache(tmpDir)
	if err != nil {
		t.Fatalf("LoadTagCache: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected only today's 1 item, got %d", len(items))
	}
	if items[0].ID != "today-1" {
		t.Errorf("ID: got %q, want %q (should load only today's cache)", items[0].ID, "today-1")
	}
}
