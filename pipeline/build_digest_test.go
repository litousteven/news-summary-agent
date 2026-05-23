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
	if result[0].Source != "中新网" {
		t.Errorf("expected source 中新网, got %s", result[0].Source)
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
