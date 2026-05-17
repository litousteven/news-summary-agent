package pipeline

import (
	"context"
	"strings"
)

// DedupCluster groups items that share the same link or display_title using union-find.
// Returns a map of clusterRoot -> list of item indices.
func DedupCluster(items []MergedNewsItem) map[int][]int {
	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	linkIndex := make(map[string]int)
	titleIndex := make(map[string]int)

	for i, item := range items {
		link := strings.TrimSpace(item.Link)
		if link != "" {
			if j, ok := linkIndex[link]; ok {
				union(i, j)
			} else {
				linkIndex[link] = i
			}
		}

		title := strings.TrimSpace(item.DisplayTitle)
		if title == "" {
			title = strings.TrimSpace(item.Title)
		}
		title = strings.ToLower(title)
		if title != "" {
			if j, ok := titleIndex[title]; ok {
				union(i, j)
			} else {
				titleIndex[title] = i
			}
		}
	}

	clusters := make(map[int][]int)
	for i := range items {
		root := find(i)
		clusters[root] = append(clusters[root], i)
	}
	return clusters
}

// MergeExactDuplicates merges items with the same link or display_title into a single item.
// The merged item combines links from all sources and keeps the best source rank.
func MergeExactDuplicates(items []MergedNewsItem, clusters map[int][]int) []MergedNewsItem {
	result := make([]MergedNewsItem, 0, len(clusters))
	for _, indices := range clusters {
		if len(indices) == 1 {
			result = append(result, items[indices[0]])
			continue
		}

		best := indices[0]
		for _, idx := range indices[1:] {
			if SourceRank[items[idx].Source] < SourceRank[items[best].Source] {
				best = idx
			} else if SourceRank[items[idx].Source] == SourceRank[items[best].Source] && items[idx].InterestScore > items[best].InterestScore {
				best = idx
			}
		}

		merged := items[best]
		links := make([]string, 0, len(indices))
		seenLinks := make(map[string]bool)
		for _, idx := range indices {
			link := strings.TrimSpace(items[idx].Link)
			if link != "" && !seenLinks[link] {
				seenLinks[link] = true
				links = append(links, link)
			}
		}
		merged.Links = links

		result = append(result, merged)
	}
	return result
}

// FindSimilarItems finds semantically similar items and creates Refs between them.
// This does NOT merge items; all items remain independent, but are linked via Refs.
func FindSimilarItems(ctx context.Context, p *NewsPipeline, items []MergedNewsItem) {
	if p.Embedding == nil || len(items) <= 1 {
		return
	}

	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.DisplayTitle + " " + item.Summary
	}
	vecs, err := CachedEmbedStrings(ctx, p, texts)
	if err != nil || len(vecs) != len(items) {
		return
	}

	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			sim := cosineSimilarity(vecs[i], vecs[j])
			if sim >= p.GetClusterThreshold() && sim < 1.0 {
				refA := NewsReference{
					DisplayTitle: items[j].DisplayTitle,
					Source:       items[j].Source,
					Link:         items[j].Link,
					Similarity:   sim,
					RelationNote: "相关",
				}
				refB := NewsReference{
					DisplayTitle: items[i].DisplayTitle,
					Source:       items[i].Source,
					Link:         items[i].Link,
					Similarity:   sim,
					RelationNote: "相关",
				}
				items[i].Refs = append(items[i].Refs, refA)
				items[j].Refs = append(items[j].Refs, refB)
			}
		}
	}
}

func (p *NewsPipeline) dedupAndLinkBatch(ctx context.Context, items []MergedNewsItem) []MergedNewsItem {
	clusters := DedupCluster(items)
	merged := MergeExactDuplicates(items, clusters)
	FindSimilarItems(ctx, p, merged)
	return merged
}
