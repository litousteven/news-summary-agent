package pipeline

import (
	"context"
	"testing"

	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

func TestDedupCluster_SameLink(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/news/1",
					Source: "央视新闻",
				},
				DisplayTitle: "标题1",
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/news/1", // same link
					Source: "BBC",
				},
				DisplayTitle: "标题1",
			},
		},
	}
	clusters := DedupCluster(items)
	total := 0
	for _, indices := range clusters {
		total += len(indices)
	}
	if total != len(items) {
		t.Errorf("expected all items in clusters, got %d/%d", total, len(items))
	}
	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(clusters))
	}
}

func TestDedupCluster_SameTitle(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/news/2",
					Source: "中新网",
				},
				DisplayTitle: "相同标题",
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/news/3", // different link
					Source: "BBC",
				},
				DisplayTitle: "相同标题", // same title
			},
		},
	}
	clusters := DedupCluster(items)
	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(clusters))
	}
}

func TestDedupCluster_NoDuplicates(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/news/4",
					Source: "中新网",
				},
				DisplayTitle: "标题A",
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/news/5",
					Source: "BBC",
				},
				DisplayTitle: "标题B",
			},
		},
	}
	clusters := DedupCluster(items)
	if len(clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(clusters))
	}
}

func TestDedupCluster_ChainDuplicates(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/a",
					Source: "中新网",
				},
				DisplayTitle: "标题X",
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/b",
					Source: "BBC",
				},
				DisplayTitle: "标题X", // same title as A
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/b", // same link as B
					Source: "NPR",
				},
				DisplayTitle: "标题Y",
			},
		},
	}
	clusters := DedupCluster(items)
	if len(clusters) != 1 {
		t.Errorf("expected 1 cluster (chain), got %d", len(clusters))
	}
}

func TestMergeExactDuplicates_CombinesLinks(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/a",
					Source: "央视新闻",
				},
				DisplayTitle: "标题X",
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/b",
					Source: "BBC",
				},
				DisplayTitle: "标题X", // same title
			},
		},
	}
	clusters := DedupCluster(items)
	result := MergeExactDuplicates(items, clusters)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged item, got %d", len(result))
	}
	if result[0].Source != "央视新闻" {
		t.Errorf("expected source 央视新闻, got %s", result[0].Source)
	}
	if len(result[0].Links) != 2 {
		t.Errorf("expected 2 links, got %d: %v", len(result[0].Links), result[0].Links)
	}
}

func TestMergeExactDuplicates_KeepsDifferentItems(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/a",
					Source: "央视新闻",
				},
				DisplayTitle: "标题X",
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/b",
					Source: "BBC",
				},
				DisplayTitle: "标题Y", // different title
			},
		},
	}
	clusters := DedupCluster(items)
	result := MergeExactDuplicates(items, clusters)
	if len(result) != 2 {
		t.Errorf("expected 2 items (no exact duplicates), got %d", len(result))
	}
}

func TestDedupAndLinkBatch_KeepsSimilarItems(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:    "https://example.com/a",
					Source:  "央视新闻",
					Summary: "摘要A",
				},
				DisplayTitle: "标题X",
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:    "https://example.com/b",
					Source:  "BBC",
					Summary: "摘要B",
				},
				DisplayTitle: "标题Y", // different title, should be kept
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:    "https://example.com/c",
					Source:  "NPR",
					Summary: "摘要C",
				},
				DisplayTitle: "标题Z", // different title, should be kept
			},
		},
	}
	p := &NewsPipeline{Embedding: nil}
	result := p.dedupAndLinkBatch(context.Background(), items)
	// All 3 items should be kept (no exact duplicates)
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d", len(result))
	}
}

func TestBuildDigest_ExcludesRefsFromFinalList(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/a",
					Source: "中新网",
				},
				DisplayTitle:  "新闻A",
				Category:      "战争与地缘",
				InterestScore: 10,
			},
			Refs: []types.NewsReference{
				{DisplayTitle: "新闻B", Link: "https://example.com/b", Source: "BBC", RelationNote: "相关报道"},
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/b",
					Source: "BBC",
				},
				DisplayTitle:  "新闻B",
				Category:      "战争与地缘",
				InterestScore: 9,
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/c",
					Source: "NPR",
				},
				DisplayTitle:  "新闻C",
				Category:      "战争与地缘",
				InterestScore: 8,
			},
		},
	}

	p := &NewsPipeline{}
	digest, err := p.buildDigest(context.Background(), items)
	if err != nil {
		t.Fatalf("buildDigest error: %v", err)
	}

	if len(digest.Items) != 2 {
		t.Fatalf("expected 2 digest items (A and C, B removed because it's in A's refs), got %d", len(digest.Items))
	}
	if digest.Items[0].DisplayTitle != "新闻A" {
		t.Errorf("expected first item 新闻A, got %s", digest.Items[0].DisplayTitle)
	}
	if digest.Items[1].DisplayTitle != "新闻C" {
		t.Errorf("expected second item 新闻C, got %s", digest.Items[1].DisplayTitle)
	}
}

// 核心回归：无来源条目不得进入简报。
//
// 上游 ParseTaggedItems 曾漏出 source/title/link 全空的条目（见 tag 包的
// TestParseTaggedItems_DropsUnmatchedResult），这里是第二道防线。即使上游
// 再漏，简报也必须拦住——宁可少推一条，也不能推一条读者无法自查的内容。
func TestBuildDigest_DropsItemsWithoutSource(t *testing.T) {
	items := []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Link:   "https://example.com/real",
					Source: "中新网",
					Title:  "真实新闻",
				},
				DisplayTitle:  "真实新闻",
				Category:      "全球经济",
				InterestScore: 7,
			},
		},
		{
			// 复刻事故条目：只有模型生成的 display_title，原始字段全空
			TaggedNewsItem: types.TaggedNewsItem{
				DisplayTitle:  "中国车企加快自研电池布局，导致宁德时代股价大幅下跌超过6%",
				Category:      "全球经济",
				InterestScore: 10, // 分数比真实新闻更高，确保它不是「因为分低才没入选」
			},
		},
		{
			// 只有空白字符的来源同样视为无来源
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem:   types.RawNewsItem{Source: "   ", Link: "https://example.com/blank"},
				DisplayTitle:  "来源为空白字符",
				Category:      "全球经济",
				InterestScore: 9,
			},
		},
	}

	p := &NewsPipeline{}
	digest, err := p.buildDigest(context.Background(), items)
	if err != nil {
		t.Fatalf("buildDigest error: %v", err)
	}

	if len(digest.Items) != 1 {
		t.Fatalf("只应留下 1 条有来源的条目，实际 %d 条", len(digest.Items))
	}
	if digest.Items[0].DisplayTitle != "真实新闻" {
		t.Errorf("应留下「真实新闻」，实际 %q", digest.Items[0].DisplayTitle)
	}
	// 统计里的入选数也要跟着降下来，避免下游统计与实际不符
	if digest.Stats.TotalSelected != 1 {
		t.Errorf("Stats.TotalSelected = %d，期望 1", digest.Stats.TotalSelected)
	}
}
