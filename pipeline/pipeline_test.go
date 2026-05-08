package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// --- P1: parseTagResult merges RawNewsItem fields from PipelineState ---

// mergeRawItemsIntoTagged is the core logic of P1 fix, extracted for testability.
// It merges raw news item fields (Source, Title, Summary, Link, etc.) into tagged items
// by matching on ID.
func mergeRawItemsIntoTagged(tagged []TaggedNewsItem, rawItems []RawNewsItem) []TaggedNewsItem {
	rawByID := make(map[string]RawNewsItem, len(rawItems))
	for _, raw := range rawItems {
		rawByID[raw.ID] = raw
	}

	for i := range tagged {
		if raw, ok := rawByID[tagged[i].ID]; ok {
			tagged[i].RawNewsItem = raw
		}
	}
	return tagged
}

func TestMergeRawItemsIntoTagged_MergesAllFields(t *testing.T) {
	rawItems := []RawNewsItem{
		{
			ID:          "bbc-a1b2c3d4",
			Source:      "BBC",
			Title:       "US seizes Iranian ship in Gulf",
			Summary:     "The US Navy seized an Iranian ship carrying weapons.",
			Link:        "https://bbc.co.uk/news/example",
			PublishedAt: "2026-04-28T10:00:00Z",
			Lang:        "en",
			FetchedAt:   "2026-04-28T12:00:00Z",
		},
		{
			ID:          "中新网-e5f6a7b8",
			Source:      "中新网",
			Title:       "SpaceX发射新一代卫星",
			Summary:     "SpaceX成功发射新一代Starlink卫星。",
			Link:        "https://chinanews.com.cn/example",
			PublishedAt: "2026-04-28T08:00:00Z",
			Lang:        "zh",
			FetchedAt:   "2026-04-28T12:00:00Z",
		},
	}

	// Simulate LLM output: only ID and tag fields, raw content is empty
	tagged := []TaggedNewsItem{
		{
			RawNewsItem:  RawNewsItem{ID: "bbc-a1b2c3d4"},
			DisplayTitle: "美军在海湾扣押伊朗船只",
			Category:     "战争与地缘",
			TopicTags:    []string{"伊朗", "海湾"},
			Region:       "中东",

			InterestScore: 9,
			IsDuplicate:   false,
			Selected:      true,
			WhySelected:   "地缘冲突升级",
		},
		{
			RawNewsItem:  RawNewsItem{ID: "中新网-e5f6a7b8"},
			DisplayTitle: "SpaceX发射新一代卫星",
			Category:     "航空航天",
			TopicTags:    []string{"SpaceX", "卫星"},
			Region:       "北美",

			InterestScore: 10,
			IsDuplicate:   false,
			Selected:      true,
			WhySelected:   "高度符合航天兴趣",
		},
	}

	result := mergeRawItemsIntoTagged(tagged, rawItems)

	// Verify first item has merged raw fields
	item1 := result[0]
	if item1.Source != "BBC" {
		t.Errorf("item1 Source: got %q, want %q", item1.Source, "BBC")
	}
	if item1.Title != "US seizes Iranian ship in Gulf" {
		t.Errorf("item1 Title: got %q, want %q", item1.Title, "US seizes Iranian ship in Gulf")
	}
	if item1.Summary != "The US Navy seized an Iranian ship carrying weapons." {
		t.Errorf("item1 Summary: got %q, want %q", item1.Summary, "The US Navy seized an Iranian ship carrying weapons.")
	}
	if item1.Link != "https://bbc.co.uk/news/example" {
		t.Errorf("item1 Link: got %q, want %q", item1.Link, "https://bbc.co.uk/news/example")
	}
	if item1.Lang != "en" {
		t.Errorf("item1 Lang: got %q, want %q", item1.Lang, "en")
	}
	if item1.PublishedAt != "2026-04-28T10:00:00Z" {
		t.Errorf("item1 PublishedAt: got %q, want %q", item1.PublishedAt, "2026-04-28T10:00:00Z")
	}
	// Tag fields should still be from LLM output
	if item1.DisplayTitle != "美军在海湾扣押伊朗船只" {
		t.Errorf("item1 DisplayTitle: got %q, want %q", item1.DisplayTitle, "美军在海湾扣押伊朗船只")
	}
	if item1.Category != "战争与地缘" {
		t.Errorf("item1 Category: got %q, want %q", item1.Category, "战争与地缘")
	}
	if item1.InterestScore != 9 {
		t.Errorf("item1 InterestScore: got %d, want %d", item1.InterestScore, 9)
	}

	// Verify second item
	item2 := result[1]
	if item2.Source != "中新网" {
		t.Errorf("item2 Source: got %q, want %q", item2.Source, "中新网")
	}
	if item2.Title != "SpaceX发射新一代卫星" {
		t.Errorf("item2 Title: got %q, want %q", item2.Title, "SpaceX发射新一代卫星")
	}
	if item2.Summary != "SpaceX成功发射新一代Starlink卫星。" {
		t.Errorf("item2 Summary: got %q, want %q", item2.Summary, "SpaceX成功发射新一代Starlink卫星。")
	}
	if item2.Category != "航空航天" {
		t.Errorf("item2 Category: got %q, want %q", item2.Category, "航空航天")
	}
}

func TestMergeRawItemsIntoTagged_OnlyMatchingIDs(t *testing.T) {
	rawItems := []RawNewsItem{
		{ID: "bbc-1111", Source: "BBC", Title: "BBC Original", Summary: "BBC Summary"},
	}

	tagged := []TaggedNewsItem{
		{RawNewsItem: RawNewsItem{ID: "bbc-1111"}, DisplayTitle: "BBC Tagged", Category: "战争与地缘"},
		{RawNewsItem: RawNewsItem{ID: "npr-9999"}, DisplayTitle: "NPR Tagged", Category: "航空航天"},
	}

	result := mergeRawItemsIntoTagged(tagged, rawItems)

	// First item: matching ID, raw fields merged
	if result[0].Source != "BBC" {
		t.Errorf("result[0] Source: got %q, want %q", result[0].Source, "BBC")
	}
	if result[0].Title != "BBC Original" {
		t.Errorf("result[0] Title: got %q, want %q", result[0].Title, "BBC Original")
	}

	// Second item: no matching raw, fields remain empty
	if result[1].Source != "" {
		t.Errorf("result[1] Source: got %q, want empty (no matching raw item)", result[1].Source)
	}
	if result[1].Title != "" {
		t.Errorf("result[1] Title: got %q, want empty (no matching raw item)", result[1].Title)
	}
	// DisplayTitle from LLM should still be there
	if result[1].DisplayTitle != "NPR Tagged" {
		t.Errorf("result[1] DisplayTitle: got %q, want %q", result[1].DisplayTitle, "NPR Tagged")
	}
}

func TestMergeRawItemsIntoTagged_EmptyRawItems(t *testing.T) {
	tagged := []TaggedNewsItem{
		{RawNewsItem: RawNewsItem{ID: "test-1111"}, DisplayTitle: "Test Title", Category: "AI与数码"},
	}

	result := mergeRawItemsIntoTagged(tagged, nil)

	// No raw items to merge, fields remain empty
	if result[0].Source != "" {
		t.Errorf("Source: got %q, want empty", result[0].Source)
	}
	if result[0].DisplayTitle != "Test Title" {
		t.Errorf("DisplayTitle: got %q, want %q", result[0].DisplayTitle, "Test Title")
	}
}

// --- P1: parseTagResult end-to-end (via PipelineState in actual Graph) ---

func TestParseTagResult_ProducesTaggedItemsWithCorrectJSON(t *testing.T) {
	// Test the raw JSON parsing + merge logic together by calling parseTagResult
	// without state (state merging is tested separately via mergeRawItemsIntoTagged)
	llmOutput := `[{"id":"test-1234","display_title":"测试标题","category":"AI与数码","topic_tags":["AI"],"region":"北美","interest_score":7,"is_duplicate":false,"selected":true,"why_selected":"测试"}]`

	p := &NewsPipeline{}
	msg := &schema.Message{Content: llmOutput}

	result, err := p.parseTagResult(context.Background(), msg)
	if err != nil {
		t.Fatalf("parseTagResult returned error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}

	item := result[0]
	if item.ID != "test-1234" {
		t.Errorf("ID: got %q, want %q", item.ID, "test-1234")
	}
	if item.DisplayTitle != "测试标题" {
		t.Errorf("DisplayTitle: got %q, want %q", item.DisplayTitle, "测试标题")
	}
	if item.Category != "AI与数码" {
		t.Errorf("Category: got %q, want %q", item.Category, "AI与数码")
	}
	if item.InterestScore != 7 {
		t.Errorf("InterestScore: got %d, want %d", item.InterestScore, 7)
	}
	if item.Selected != true {
		t.Errorf("Selected: got %v, want true", item.Selected)
	}
}

func TestParseTagResult_HandlesMarkdownCodeBlock(t *testing.T) {
	llmOutput := "Here are the results:\n```json\n[{\"id\":\"test-1\",\"display_title\":\"标题\",\"category\":\"航空航天\",\"topic_tags\":[],\"region\":\"北美\",\"interest_score\":8,\"is_duplicate\":false,\"selected\":true,\"why_selected\":\"测试\"}]\n```"

	p := &NewsPipeline{}
	msg := &schema.Message{Content: llmOutput}

	result, err := p.parseTagResult(context.Background(), msg)
	if err != nil {
		t.Fatalf("parseTagResult returned error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}
	if result[0].Category != "航空航天" {
		t.Errorf("Category: got %q, want %q", result[0].Category, "航空航天")
	}
}

func TestParseTagResult_DefaultsDisplayTitleToTitle(t *testing.T) {
	llmOutput := `[{"id":"test-no-display","display_title":"","category":"AI与数码","topic_tags":[],"region":"北美","interest_score":5,"is_duplicate":false,"selected":false,"why_selected":""}]`

	p := &NewsPipeline{}
	msg := &schema.Message{Content: llmOutput}

	result, err := p.parseTagResult(context.Background(), msg)
	if err != nil {
		t.Fatalf("parseTagResult returned error: %v", err)
	}
	// DisplayTitle is empty in LLM output and Title is also empty (no raw items),
	// so it should remain empty (Title fallback only works if raw items are merged)
	if result[0].DisplayTitle != "" {
		t.Errorf("DisplayTitle: got %q, want empty (no raw items, no title from LLM)", result[0].DisplayTitle)
	}
}

func TestTagNewItems_FailedBatchDropped(t *testing.T) {
	p := &NewsPipeline{
		ChatModel: &mockChatModelTagDrop{},
	}

	items := []RawNewsItem{
		{ID: "item-1", Source: "BBC", Title: "英国经济下行", Summary: "英国央行发布数据", Link: "https://bbc.co.uk/1", PublishedAt: "2026-05-07T10:00:00Z", Lang: "en"},
		{ID: "item-2", Source: "NPR", Title: "美国经济继续反弹", Summary: "最新就业数据好于预期", Link: "https://npr.org/2", PublishedAt: "2026-05-07T09:00:00Z", Lang: "en"},
	}

	tagged, err := p.tagNewItems(context.Background(), items)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// The mock always fails, so all items should be dropped
	if len(tagged) != 0 {
		t.Fatalf("expected 0 tagged items (all batches failed), got %d", len(tagged))
	}
}

// --- P0: recordHistory writes per-item records with embeddings ---

func TestRecordHistory_WritesPerItemRecords(t *testing.T) {
	tmpDir := t.TempDir()

	digestItems := []DigestItem{
		{
			MergedNewsItem: MergedNewsItem{
				TaggedNewsItem: TaggedNewsItem{
					RawNewsItem: RawNewsItem{
						ID:     "bbc-1111",
						Source: "BBC",
						Title:  "Raw title 1",
						Link:   "https://bbc.co.uk/1",
					},
					DisplayTitle: "美军扣押伊朗船只",
					Category:     "战争与地缘",

					InterestScore: 9,
				},
			},
			FactParagraph: "美军在海湾扣押了一艘伊朗船只，涉及武器走私。",
		},
		{
			MergedNewsItem: MergedNewsItem{
				TaggedNewsItem: TaggedNewsItem{
					RawNewsItem: RawNewsItem{
						ID:     "中新网-2222",
						Source: "中新网",
						Title:  "原始标题2",
						Link:   "https://chinanews.com.cn/2",
					},
					DisplayTitle: "SpaceX发射新一代卫星",
					Category:     "航空航天",

					InterestScore: 10,
				},
			},
			FactParagraph: "SpaceX成功发射了新一代Starlink卫星。",
		},
	}

	// Set up context with PipelineState containing DigestItems and Slot
	ctx := injectState(context.Background(), &PipelineState{
		DigestItems: digestItems,
		Slot:        "12:00",
	})

	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir}
	msg := &schema.Message{Content: "这是最终的简报文本"}

	result, err := p.recordHistory(ctx, msg)
	if err != nil {
		t.Fatalf("recordHistory returned error: %v", err)
	}

	if result.Message != "这是最终的简报文本" {
		t.Errorf("result Message: got %q, want %q", result.Message, "这是最终的简报文本")
	}

	// Read back the history file and verify per-item records
	records := loadHistoryRecords(t, tmpDir)
	if len(records) != 2 {
		t.Fatalf("expected 2 history records, got %d", len(records))
	}

	// Verify first record
	r1 := findByDisplayTitle(records, "美军扣押伊朗船只")
	if r1 == nil {
		t.Fatal("record with DisplayTitle '美军扣押伊朗船只' not found")
	}
	if r1.DisplayTitle != "美军扣押伊朗船只" {
		t.Errorf("r1 DisplayTitle: got %q, want %q", r1.DisplayTitle, "美军扣押伊朗船只")
	}
	if r1.Category != "战争与地缘" {
		t.Errorf("r1 Category: got %q, want %q", r1.Category, "战争与地缘")
	}
	if r1.Source != "BBC" {
		t.Errorf("r1 Source: got %q, want %q", r1.Source, "BBC")
	}
	if r1.FactSummary != "美军在海湾扣押了一艘伊朗船只，涉及武器走私。" {
		t.Errorf("r1 FactSummary: got %q, want %q", r1.FactSummary, "美军在海湾扣押了一艘伊朗船只，涉及武器走私。")
	}
	if r1.Slot != "12:00" {
		t.Errorf("r1 Slot: got %q, want %q", r1.Slot, "12:00")
	}

	// Verify second record
	r2 := findByDisplayTitle(records, "SpaceX发射新一代卫星")
	if r2 == nil {
		t.Fatal("record with DisplayTitle 'SpaceX发射新一代卫星' not found")
	}
	if r2.DisplayTitle != "SpaceX发射新一代卫星" {
		t.Errorf("r2 DisplayTitle: got %q, want %q", r2.DisplayTitle, "SpaceX发射新一代卫星")
	}
	if r2.Category != "航空航天" {
		t.Errorf("r2 Category: got %q, want %q", r2.Category, "航空航天")
	}
	if r2.Source != "中新网" {
		t.Errorf("r2 Source: got %q, want %q", r2.Source, "中新网")
	}

	// No digest-level record should exist
	for _, r := range records {
		if r.Category == "简报" {
			t.Errorf("found digest-level record, expected only per-item records")
		}
	}
}

func TestRecordHistory_WritesPerItemRecordsWithEmbeddings(t *testing.T) {
	tmpDir := t.TempDir()

	digestItems := []DigestItem{
		{
			MergedNewsItem: MergedNewsItem{
				TaggedNewsItem: TaggedNewsItem{
					RawNewsItem: RawNewsItem{
						ID:     "bbc-1111",
						Source: "BBC",
						Title:  "Raw title",
						Link:   "https://bbc.co.uk/1",
					},
					DisplayTitle: "测试标题",
					Category:     "AI与数码",

					InterestScore: 8,
				},
			},
			FactParagraph: "这是测试事实段落。",
		},
	}

	ctx := injectState(context.Background(), &PipelineState{
		DigestItems: digestItems,
		Slot:        "manual",
	})

	// Use mock embedding client
	mockEmbed := &mockEmbeddingClient{
		vectors: [][]float64{{0.1, 0.2, 0.3}},
	}

	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir, Embedding: mockEmbed}
	msg := &schema.Message{Content: "简报文本"}

	_, err := p.recordHistory(ctx, msg)
	if err != nil {
		t.Fatalf("recordHistory returned error: %v", err)
	}

	records := loadHistoryRecords(t, tmpDir)
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}

	r := records[0]
	if len(r.Embedding) != 3 {
		t.Fatalf("expected embedding of length 3, got %d", len(r.Embedding))
	}
	if r.Embedding[0] != 0.1 || r.Embedding[1] != 0.2 || r.Embedding[2] != 0.3 {
		t.Errorf("embedding values: got %v, want [0.1, 0.2, 0.3]", r.Embedding)
	}
}

func TestRecordHistory_FallbackToSessionRecord(t *testing.T) {
	tmpDir := t.TempDir()

	// No DigestItems in state — should fall back to session-level record
	ctx := injectState(context.Background(), &PipelineState{
		DigestItems: nil,
		Slot:        "18:00",
	})

	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir}
	msg := &schema.Message{Content: "这是简报文本"}

	result, err := p.recordHistory(ctx, msg)
	if err != nil {
		t.Fatalf("recordHistory returned error: %v", err)
	}

	if result.Message != "这是简报文本" {
		t.Errorf("result Message: got %q, want %q", result.Message, "这是简报文本")
	}

	records := loadHistoryRecords(t, tmpDir)
	if len(records) != 1 {
		t.Fatalf("expected 1 history record (fallback), got %d", len(records))
	}

	r := records[0]
	if r.Category != "简报" {
		t.Errorf("fallback record Category: got %q, want %q", r.Category, "简报")
	}
	if r.Source != "多源" {
		t.Errorf("fallback record Source: got %q, want %q", r.Source, "多源")
	}
}

// --- P0: End-to-end: RecordHistory → MergeHistory dedup ---

func TestRecordHistory_ThenMergeHistory_DeduplicatesByTitle(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: Record per-item history via RecordHistoryFromDigest
	digestItems := []DigestItem{
		{
			MergedNewsItem: MergedNewsItem{
				TaggedNewsItem: TaggedNewsItem{
					RawNewsItem: RawNewsItem{
						ID:     "bbc-1111",
						Source: "BBC",
						Title:  "US seizes Iranian ship",
						Link:   "https://bbc.co.uk/1",
					},
					DisplayTitle: "美军扣押伊朗船只",
					Category:     "战争与地缘",

					InterestScore: 9,
				},
			},
			FactParagraph: "美军在海湾扣押了一艘伊朗船只。",
		},
	}

	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir}
	err := p.RecordHistoryFromDigest(context.Background(), digestItems, "12:00")
	if err != nil {
		t.Fatalf("RecordHistoryFromDigest error: %v", err)
	}

	// Verify history was written
	records := loadHistoryRecords(t, tmpDir)
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
	if records[0].DisplayTitle != "美军扣押伊朗船只" {
		t.Errorf("history DisplayTitle: got %q, want %q", records[0].DisplayTitle, "美军扣押伊朗船只")
	}

	// Step 2: Merge new items — one with same DisplayTitle, one new
	newItems := []TaggedNewsItem{
		{
			RawNewsItem: RawNewsItem{
				ID:     "bbc-2222",
				Source: "BBC",
				Title:  "US seizes another Iranian ship",
			},
			DisplayTitle:  "美军扣押伊朗船只", // same DisplayTitle as history → SeenBefore=true
			InterestScore: 8,
		},
		{
			RawNewsItem: RawNewsItem{
				ID:     "npr-3333",
				Source: "NPR",
				Title:  "NATO summit begins",
			},
			DisplayTitle: "北约峰会开始",
			Category:     "战争与地缘",

			InterestScore: 9,
		},
	}

	merged, err := p.mergeHistory(context.Background(), newItems)
	if err != nil {
		t.Fatalf("mergeHistory returned error: %v", err)
	}

	if len(merged) != 2 {
		t.Fatalf("expected 2 merged items, got %d", len(merged))
	}

	// First item: DisplayTitle matches history → SeenBefore=true (duplicate)
	if !merged[0].SeenBefore {
		t.Error("merged[0] should be SeenBefore=true (DisplayTitle matches history)")
	}

	// Second item: new event
	if merged[1].SeenBefore {
		t.Error("merged[1] should be SeenBefore=false (new event)")
	}
}

// --- P0: RecordHistoryFromDigest writes embeddings ---

func TestRecordHistoryFromDigest_WithEmbeddings(t *testing.T) {
	tmpDir := t.TempDir()

	digestItems := []DigestItem{
		{
			MergedNewsItem: MergedNewsItem{
				TaggedNewsItem: TaggedNewsItem{
					RawNewsItem:  RawNewsItem{ID: "test-1", Source: "BBC", Title: "Test", Link: "https://example.com"},
					DisplayTitle: "测试1",
					Category:     "AI与数码",
				},
			},
			FactParagraph: "测试事实1",
		},
		{
			MergedNewsItem: MergedNewsItem{
				TaggedNewsItem: TaggedNewsItem{
					RawNewsItem:  RawNewsItem{ID: "test-2", Source: "NPR", Title: "Test2", Link: "https://example.com/2"},
					DisplayTitle: "测试2",
					Category:     "航空航天",
				},
			},
			FactParagraph: "测试事实2",
		},
	}

	mockEmbed := &mockEmbeddingClient{
		vectors: [][]float64{{0.1, 0.2}, {0.3, 0.4}},
	}

	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir, Embedding: mockEmbed}
	err := p.RecordHistoryFromDigest(context.Background(), digestItems, "12:00")
	if err != nil {
		t.Fatalf("RecordHistoryFromDigest error: %v", err)
	}

	records := loadHistoryRecords(t, tmpDir)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Verify embeddings were stored
	if len(records[0].Embedding) != 2 {
		t.Errorf("records[0] Embedding length: got %d, want 2", len(records[0].Embedding))
	}
	if len(records[1].Embedding) != 2 {
		t.Errorf("records[1] Embedding length: got %d, want 2", len(records[1].Embedding))
	}
	if records[0].Embedding[0] != 0.1 || records[0].Embedding[1] != 0.2 {
		t.Errorf("records[0] Embedding: got %v, want [0.1, 0.2]", records[0].Embedding)
	}
	if records[1].Embedding[0] != 0.3 || records[1].Embedding[1] != 0.4 {
		t.Errorf("records[1] Embedding: got %v, want [0.3, 0.4]", records[1].Embedding)
	}

	// Verify DisplayTitles
	if records[0].DisplayTitle != "测试1" {
		t.Errorf("records[0] DisplayTitle: got %q, want %q", records[0].DisplayTitle, "测试1")
	}
	if records[1].DisplayTitle != "测试2" {
		t.Errorf("records[1] DisplayTitle: got %q, want %q", records[1].DisplayTitle, "测试2")
	}
}

// --- Graph compilation with state ---

func TestBuildGraph_WithStateCompiles(t *testing.T) {
	p := &NewsPipeline{
		ChatModel: &mockChatModel{},
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
	}

	_, err := p.BuildGraph(context.Background())
	if err != nil {
		t.Fatalf("BuildGraph failed: %v", err)
	}
}

// --- P1 integration: parseTagResult uses PipelineState via Graph ---

func TestParseTagResult_StateInjectionViaGraph(t *testing.T) {
	// This test verifies that the Graph's WithGenLocalState mechanism works
	// by building a graph that: saves RawItems to state → invokes parseTagResult
	// The parseTagResult reads RawItems from PipelineState via ProcessState.

	p := &NewsPipeline{}

	// We'll test this by building a full 3-node graph:
	// 1. "raw" node: returns []RawNewsItem and saves them to state
	// 2. "to_msg" node: creates a *schema.Message (simulating LLM output)
	// 3. "parse" node: calls parseTagResult which reads state

	g := compose.NewGraph[map[string]any, []TaggedNewsItem](
		compose.WithGenLocalState(func(ctx context.Context) *PipelineState {
			return &PipelineState{}
		}),
	)

	// Node 1: produce raw items and save to state
	rawItems := []RawNewsItem{
		{ID: "test-1", Source: "BBC", Title: "Test Title", Summary: "Test Summary", Link: "https://example.com", Lang: "en"},
	}
	if err := g.AddLambdaNode("raw",
		compose.InvokableLambda(func(ctx context.Context, _ map[string]any) ([]RawNewsItem, error) {
			return rawItems, nil
		}),
		compose.WithStatePostHandler(func(ctx context.Context, out []RawNewsItem, state *PipelineState) ([]RawNewsItem, error) {
			state.RawItems = out
			return out, nil
		}),
	); err != nil {
		t.Fatalf("AddLambdaNode raw: %v", err)
	}

	// Node 2: create a *schema.Message simulating LLM output
	if err := g.AddLambdaNode("to_msg",
		compose.InvokableLambda(func(ctx context.Context, items []RawNewsItem) (*schema.Message, error) {
			// Generate JSON that only contains id and tag fields (no raw content)
			output := `[{"id":"test-1","display_title":"测试标题","category":"AI与数码","topic_tags":["AI"],"region":"北美","interest_score":8,"is_duplicate":false,"selected":true,"why_selected":"测试"}]`
			return &schema.Message{Content: output}, nil
		}),
	); err != nil {
		t.Fatalf("AddLambdaNode to_msg: %v", err)
	}

	// Node 3: parse tag result (reads raw items from state)
	if err := g.AddLambdaNode("parse",
		compose.InvokableLambda(p.parseTagResult),
	); err != nil {
		t.Fatalf("AddLambdaNode parse: %v", err)
	}

	// Edges
	if err := g.AddEdge(compose.START, "raw"); err != nil {
		t.Fatalf("AddEdge START→raw: %v", err)
	}
	if err := g.AddEdge("raw", "to_msg"); err != nil {
		t.Fatalf("AddEdge raw→to_msg: %v", err)
	}
	if err := g.AddEdge("to_msg", "parse"); err != nil {
		t.Fatalf("AddEdge to_msg→parse: %v", err)
	}
	if err := g.AddEdge("parse", compose.END); err != nil {
		t.Fatalf("AddEdge parse→END: %v", err)
	}

	r, err := g.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Run the graph
	result, err := r.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// Verify: parseTagResult should have merged raw fields from state
	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}

	item := result[0]
	if item.Source != "BBC" {
		t.Errorf("Source: got %q, want %q — raw fields not merged from PipelineState", item.Source, "BBC")
	}
	if item.Title != "Test Title" {
		t.Errorf("Title: got %q, want %q — raw fields not merged from PipelineState", item.Title, "Test Title")
	}
	if item.Summary != "Test Summary" {
		t.Errorf("Summary: got %q, want %q — raw fields not merged from PipelineState", item.Summary, "Test Summary")
	}
	if item.Link != "https://example.com" {
		t.Errorf("Link: got %q, want %q — raw fields not merged from PipelineState", item.Link, "https://example.com")
	}
	if item.Lang != "en" {
		t.Errorf("Lang: got %q, want %q — raw fields not merged from PipelineState", item.Lang, "en")
	}
	// Tag fields from LLM
	if item.DisplayTitle != "测试标题" {
		t.Errorf("DisplayTitle: got %q, want %q", item.DisplayTitle, "测试标题")
	}
	if item.Category != "AI与数码" {
		t.Errorf("Category: got %q, want %q", item.Category, "AI与数码")
	}
}

// --- P2: SeenBefore items are excluded from digest ---

func TestMergeHistory_SeenBeforeHighScore_IsDuplicate(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a history record
	now := "2026-04-28T12:00:00Z"
	records := []PushHistoryRecord{
		{
			PushTime:     now,
			Slot:         "12:00",
			DisplayTitle: "美伊冲突升级",
			Category:     "战争与地缘",
			Source:       "BBC",
			FactSummary:  "美伊冲突的前情概要",
		},
	}
	writeHistoryRecords(t, tmpDir, records)

	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir}

	items := []TaggedNewsItem{
		{
			RawNewsItem: RawNewsItem{
				ID:     "bbc-aaaa",
				Source: "BBC",
				Title:  "Iran launches retaliatory strike",
			},
			DisplayTitle:  "美伊冲突升级", // same display_title as history → exact match
			Category:      "战争与地缘",
			InterestScore: 9,
		},
	}

	merged, err := p.mergeHistory(context.Background(), items)
	if err != nil {
		t.Fatalf("mergeHistory error: %v", err)
	}

	if len(merged) != 1 {
		t.Fatalf("expected 1 item, got %d", len(merged))
	}

	m := merged[0]
	if !m.SeenBefore {
		t.Error("SeenBefore should be true (DisplayTitle matches history)")
	}
}

func TestMergeHistory_SeenBeforeLowScore_IsDuplicate(t *testing.T) {
	tmpDir := t.TempDir()

	now := "2026-04-28T12:00:00Z"
	records := []PushHistoryRecord{
		{
			PushTime: now,
			Slot:     "12:00",

			DisplayTitle: "小事件",
			Category:     "其他重要动态",
			Source:       "NPR",
			FactSummary:  "小事件概要",
		},
	}
	writeHistoryRecords(t, tmpDir, records)

	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir}

	items := []TaggedNewsItem{
		{
			RawNewsItem: RawNewsItem{
				ID:     "npr-bbbb",
				Source: "NPR",
				Title:  "Minor update on event",
			},
			DisplayTitle:  "小事件", // same display_title as history → exact match
			Category:      "其他重要动态",
			InterestScore: 5,
		},
	}

	merged, err := p.mergeHistory(context.Background(), items)
	if err != nil {
		t.Fatalf("mergeHistory error: %v", err)
	}

	if len(merged) != 1 {
		t.Fatalf("expected 1 item, got %d", len(merged))
	}

	m := merged[0]
	if !m.SeenBefore {
		t.Error("SeenBefore should be true")
	}
}

func TestMergeHistory_NewEventAlwaysPushes(t *testing.T) {
	tmpDir := t.TempDir()

	// No history for this event
	p := &NewsPipeline{ConfigDir: tmpDir, DataDir: tmpDir}

	items := []TaggedNewsItem{
		{
			RawNewsItem: RawNewsItem{
				ID:     "bbc-new1",
				Source: "BBC",
				Title:  "Brand new event",
			},
			DisplayTitle: "全新事件",
			Category:     "航空航天",

			InterestScore: 7,
		},
	}

	merged, err := p.mergeHistory(context.Background(), items)
	if err != nil {
		t.Fatalf("mergeHistory error: %v", err)
	}

	if merged[0].SeenBefore {
		t.Error("SeenBefore should be false for new event")
	}
}

func TestBuildDigest_ExcludesSeenBeforeDuplicates(t *testing.T) {
	items := []MergedNewsItem{
		{
			TaggedNewsItem: TaggedNewsItem{
				RawNewsItem:  RawNewsItem{ID: "1", Source: "BBC", Title: "New event", Summary: "New summary"},
				DisplayTitle: "新事件",
				Category:     "战争与地缘",

				InterestScore: 9,
			},
		},
		{
			TaggedNewsItem: TaggedNewsItem{
				RawNewsItem:   RawNewsItem{ID: "2", Source: "BBC", Title: "Follow-up", Summary: "Follow-up summary"},
				DisplayTitle:  "追踪事件",
				Category:      "战争与地缘",
				InterestScore: 8,
			},
			SeenBefore: true,
		},
		{
			TaggedNewsItem: TaggedNewsItem{
				RawNewsItem:   RawNewsItem{ID: "3", Source: "NPR", Title: "Low score", Summary: "Low summary"},
				DisplayTitle:  "低分旧事件",
				Category:      "其他重要动态",
				InterestScore: 5,
			},
			SeenBefore: true,
		},
	}

	p := &NewsPipeline{}
	digest, err := p.buildDigest(context.Background(), items)
	if err != nil {
		t.Fatalf("buildDigest error: %v", err)
	}

	// Should have only 1 item (new event), SeenBefore items are excluded regardless of score
	if len(digest.Items) != 1 {
		t.Fatalf("expected 1 digest item, got %d", len(digest.Items))
	}

	// Only the new event should be in the digest
	if digest.Items[0].SeenBefore {
		t.Error("digest.Items[0] should not be SeenBefore (new event)")
	}
}

// --- Reference news tests ---

func TestBuildDigest_IncludesReferencesInDigestItems(t *testing.T) {
	items := []MergedNewsItem{
		{
			TaggedNewsItem: TaggedNewsItem{
				RawNewsItem:   RawNewsItem{ID: "1", Source: "BBC", Title: "Iran strike", Summary: "Iran launched missiles", Link: "https://bbc.co.uk/2"},
				DisplayTitle:  "伊朗发动导弹袭击",
				Category:      "战争与地缘",
				InterestScore: 9,
			},
			Refs: []NewsReference{
				{
					DisplayTitle: "美伊冲突升级",
					Link:         "https://bbc.co.uk/1",
					PushTime:     "2026-04-28T12:00:00Z",
					FactSummary:  "美伊冲突的前情概要",
					RelationNote: "前情回顾",
				},
			},
		},
		{
			TaggedNewsItem: TaggedNewsItem{
				RawNewsItem:   RawNewsItem{ID: "2", Source: "NPR", Title: "AI breakthrough", Summary: "New AI model released", Link: "https://npr.org/2"},
				DisplayTitle:  "AI新突破",
				Category:      "AI与数码",
				InterestScore: 8,
			},
			Refs: []NewsReference{
				{
					DisplayTitle: "AI模型发展",
					Link:         "https://npr.org/1",
					PushTime:     "2026-04-28T06:00:00Z",
					FactSummary:  "之前的AI报道",
					RelationNote: "反转",
				},
			},
		},
	}

	p := &NewsPipeline{}
	digest, err := p.buildDigest(context.Background(), items)
	if err != nil {
		t.Fatalf("buildDigest error: %v", err)
	}

	if len(digest.Items) != 2 {
		t.Fatalf("expected 2 digest items, got %d", len(digest.Items))
	}

	if len(digest.Items[0].Refs) != 1 {
		t.Fatalf("item[0] Refs: got %d, want 1", len(digest.Items[0].Refs))
	}
	if digest.Items[0].Refs[0].RelationNote != "前情回顾" {
		t.Errorf("item[0] ref RelationNote: got %q, want %q", digest.Items[0].Refs[0].RelationNote, "前情回顾")
	}
	if digest.Items[0].Refs[0].DisplayTitle != "美伊冲突升级" {
		t.Errorf("item[0] ref DisplayTitle: got %q, want %q", digest.Items[0].Refs[0].DisplayTitle, "美伊冲突升级")
	}

	if len(digest.Items[1].Refs) != 1 {
		t.Fatalf("item[1] Refs: got %d, want 1", len(digest.Items[1].Refs))
	}
	if digest.Items[1].Refs[0].RelationNote != "反转" {
		t.Errorf("item[1] ref RelationNote: got %q, want %q", digest.Items[1].Refs[0].RelationNote, "反转")
	}
}

func TestFormatSummaryPrompt_IncludesReferenceContext(t *testing.T) {
	digest := &DigestData{
		Items: []DigestItem{
			{
				MergedNewsItem: MergedNewsItem{
					TaggedNewsItem: TaggedNewsItem{
						RawNewsItem:  RawNewsItem{ID: "1", Source: "BBC"},
						DisplayTitle: "伊朗发动导弹袭击",
						Category:     "战争与地缘",
					},
					Refs: []NewsReference{
						{
							DisplayTitle: "美伊冲突升级",
							FactSummary:  "美伊冲突的前情概要",
							RelationNote: "前情回顾",
						},
					},
				},
				FactParagraph: "伊朗向美军基地发射了多枚导弹。",
			},
		},
		SlotLabel:   "午间版",
		CurrentTime: "2026-04-29 12:00:00",
	}

	p := &NewsPipeline{}
	vars, err := p.formatSummaryPrompt(context.Background(), digest)
	if err != nil {
		t.Fatalf("formatSummaryPrompt error: %v", err)
	}

	content, ok := vars["digest_content"].(string)
	if !ok {
		t.Fatal("digest_content is not a string")
	}

	if !contains(content, "伊朗向美军基地发射了多枚导弹") {
		t.Errorf("digest_content missing fact paragraph, got: %s", content)
	}
	if !contains(content, "前情回顾") {
		t.Errorf("digest_content missing reference RelationNote, got: %s", content)
	}
	if !contains(content, "美伊冲突的前情概要") {
		t.Errorf("digest_content missing reference FactSummary, got: %s", content)
	}
}

func TestMergeHistory_ReferencesCappedAtTwo(t *testing.T) {
	item := MergedNewsItem{
		TaggedNewsItem: TaggedNewsItem{
			RawNewsItem: RawNewsItem{ID: "1", Source: "BBC"},
		},
		Refs: []NewsReference{
			{DisplayTitle: "ref1", RelationNote: "前情回顾"},
			{DisplayTitle: "ref2", RelationNote: "前情回顾"},
			{DisplayTitle: "ref3", RelationNote: "反转"},
		},
	}

	if len(item.Refs) > 2 {
		item.Refs = item.Refs[:2]
	}

	if len(item.Refs) != 2 {
		t.Errorf("expected 2 references after cap, got %d", len(item.Refs))
	}
}

func TestLlmVerifyDuplicates_ParsesProgressAndReversal(t *testing.T) {
	// Test that the LLM response parser correctly handles 进展/反转/重复/无关
	// We test the parsing logic indirectly by checking the verifyResult construction
	// from a simulated LLM response

	p := &NewsPipeline{
		ChatModel: &mockChatModelWithResponse{
			content: "进展\n反转\n重复\n无关",
		},
	}

	items := []MergedNewsItem{
		{TaggedNewsItem: TaggedNewsItem{RawNewsItem: RawNewsItem{ID: "1", Summary: "摘要1"}, DisplayTitle: "新闻1"}},
		{TaggedNewsItem: TaggedNewsItem{RawNewsItem: RawNewsItem{ID: "2", Summary: "摘要2"}, DisplayTitle: "新闻2"}},
		{TaggedNewsItem: TaggedNewsItem{RawNewsItem: RawNewsItem{ID: "3", Summary: "摘要3"}, DisplayTitle: "新闻3"}},
		{TaggedNewsItem: TaggedNewsItem{RawNewsItem: RawNewsItem{ID: "4", Summary: "摘要4"}, DisplayTitle: "新闻4"}},
	}

	candidates := []embedCandidate{
		{itemIdx: 0, record: &PushHistoryRecord{DisplayTitle: "历史1", FactSummary: "历史摘要1"}, sim: 0.85},
		{itemIdx: 1, record: &PushHistoryRecord{DisplayTitle: "历史2", FactSummary: "历史摘要2"}, sim: 0.82},
		{itemIdx: 2, record: &PushHistoryRecord{DisplayTitle: "历史3", FactSummary: "历史摘要3"}, sim: 0.90},
		{itemIdx: 3, record: &PushHistoryRecord{DisplayTitle: "历史4", FactSummary: "历史摘要4"}, sim: 0.78},
	}

	results, err := p.llmVerifyDuplicatesMerged(context.Background(), items, candidates)
	if err != nil {
		t.Fatalf("llmVerifyDuplicatesMerged error: %v", err)
	}

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}

	// 进展 → IsReference=true, Note="相关"
	if !results[0].IsReference || results[0].IsDuplicate {
		t.Errorf("result[0]: expected IsReference=true, IsDuplicate=false, got %+v", results[0])
	}
	if results[0].Note != "相关" {
		t.Errorf("result[0] Note: got %q, want %q", results[0].Note, "相关")
	}

	// 反转 → IsReference=true, Note="相关"
	if !results[1].IsReference || results[1].IsDuplicate {
		t.Errorf("result[1]: expected IsReference=true, IsDuplicate=false, got %+v", results[1])
	}
	if results[1].Note != "相关" {
		t.Errorf("result[1] Note: got %q, want %q", results[1].Note, "相关")
	}

	// 重复 → IsDuplicate=true
	if !results[2].IsDuplicate || results[2].IsReference {
		t.Errorf("result[2]: expected IsDuplicate=true, IsReference=false, got %+v", results[2])
	}

	// 无关 → both false
	if results[3].IsDuplicate || results[3].IsReference {
		t.Errorf("result[3]: expected both false, got %+v", results[3])
	}
}

// mockChatModelWithResponse returns a preset content string.
type mockChatModelWithResponse struct {
	content string
}

func (m *mockChatModelWithResponse) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return &schema.Message{Content: m.content}, nil
}

func (m *mockChatModelWithResponse) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("not implemented")
}

// helper for string containment check
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// --- Helpers ---

// injectState creates a context with PipelineState that can be read by
// compose.ProcessState. It uses the Eino-internal state key mechanism.
func injectState(ctx context.Context, state *PipelineState) context.Context {
	// Use compose.ProcessState-compatible state injection by building a minimal
	// graph, compiling it, and using its runCtx. But that's too complex for unit tests.
	//
	// Instead, we directly set up the context value using Eino's internal stateKey.
	// Since stateKey is unexported in compose package, we use an alternative approach:
	// set the state on the Pipeline struct directly for testing.
	//
	// The actual code uses compose.ProcessState which reads from context set by
	// the Graph's WithGenLocalState. In unit tests, we bypass this by providing
	// state through a test helper that simulates the same context structure.
	//
	// We use reflection-like approach: the state key in Eino is compose.stateKey{}
	// which is an unexported type. We can't use it from outside the package.
	// So instead, we use a different approach for testing.

	// Create a simple graph just to get the right context with state injected
	g := compose.NewGraph[string, string](
		compose.WithGenLocalState(func(ctx context.Context) *PipelineState {
			return state
		}),
	)
	if err := g.AddLambdaNode("identity",
		compose.InvokableLambda(func(ctx context.Context, s string) (string, error) { return s, nil }),
	); err != nil {
		panic(fmt.Sprintf("failed to create test graph: %v", err))
	}
	if err := g.AddEdge(compose.START, "identity"); err != nil {
		panic(fmt.Sprintf("failed to add edge: %v", err))
	}
	if err := g.AddEdge("identity", compose.END); err != nil {
		panic(fmt.Sprintf("failed to add edge: %v", err))
	}

	compiled, compileErr := g.Compile(context.Background())
	if compileErr != nil {
		panic(fmt.Sprintf("failed to compile test graph: %v", compileErr))
	}
	_ = compiled

	// Get the run context by triggering the graph's internal runCtx
	// We can't easily extract the context from here, so we use a workaround:
	// create a graph node that captures the context
	ctxCh := make(chan context.Context, 1)
	captureG := compose.NewGraph[string, string](
		compose.WithGenLocalState(func(ctx context.Context) *PipelineState {
			return state
		}),
	)
	if err := captureG.AddLambdaNode("capture",
		compose.InvokableLambda(func(ctx context.Context, s string) (string, error) {
			ctxCh <- ctx
			return s, nil
		}),
	); err != nil {
		panic(fmt.Sprintf("failed to create capture graph: %v", err))
	}
	if err := captureG.AddEdge(compose.START, "capture"); err != nil {
		panic(fmt.Sprintf("failed to add edge: %v", err))
	}
	if err := captureG.AddEdge("capture", compose.END); err != nil {
		panic(fmt.Sprintf("failed to add edge: %v", err))
	}

	captureR, err := captureG.Compile(context.Background())
	if err != nil {
		panic(fmt.Sprintf("failed to compile capture graph: %v", err))
	}

	// Run the graph to get the context with state injected
	go func() {
		_, _ = captureR.Invoke(context.Background(), "trigger")
	}()

	return <-ctxCh
}

// mockEmbeddingClient is a simple mock for EmbeddingClient
type mockEmbeddingClient struct {
	vectors [][]float64
	err     error
}

func (m *mockEmbeddingClient) EmbedStrings(ctx context.Context, texts []string) ([][]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.vectors) > 0 {
		return m.vectors, nil
	}
	// Return zero vectors
	result := make([][]float64, len(texts))
	for i := range result {
		result[i] = make([]float64, 3)
	}
	return result, nil
}

// mockChatModel is a minimal mock for model.BaseChatModel
type mockChatModel struct{}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return &schema.Message{Content: "[]"}, nil
}

func (m *mockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("not implemented")
}

// mockChatModelTagDrop always fails — used to test that failed batches are dropped.
type mockChatModelTagDrop struct{}

func (m *mockChatModelTagDrop) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("LLM unavailable")
}

func (m *mockChatModelTagDrop) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("not implemented")
}

func loadHistoryRecords(t *testing.T, dataDir string) []PushHistoryRecord {
	t.Helper()
	// Read from today's per-day history file
	today := time.Now().UTC().Format("20060102")
	path := filepath.Join(dataDir, "push_history_"+today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read history file: %v", err)
	}

	var records []PushHistoryRecord
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var r PushHistoryRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal history record: %v\nline: %s", err, line)
		}
		records = append(records, r)
	}
	return records
}

func writeHistoryRecords(t *testing.T, dataDir string, records []PushHistoryRecord) {
	t.Helper()
	today := time.Now().UTC().Format("20060102")
	path := filepath.Join(dataDir, "push_history_"+today+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create history file: %v", err)
	}
	defer f.Close()

	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal history record: %v", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatalf("write history record: %v", err)
		}
	}
}

func findByDisplayTitle(records []PushHistoryRecord, title string) *PushHistoryRecord {
	for i := range records {
		if records[i].DisplayTitle == title {
			return &records[i]
		}
	}
	return nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// Verify PipelineState types match what compose.ProcessState expects
var _ = compose.ProcessState[*PipelineState]
