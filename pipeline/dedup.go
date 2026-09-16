package pipeline

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/litousteven/news-summary-agent/pipeline/embedding"
	fetchrss "github.com/litousteven/news-summary-agent/pipeline/fetch_rss"
	types "github.com/litousteven/news-summary-agent/pipeline/types"
	"github.com/litousteven/news-summary-agent/pipeline/util"
)

// DedupCluster groups items that share the same link or display_title using union-find.
// Returns a map of clusterRoot -> list of item indices.
func DedupCluster(items []types.MergedNewsItem) map[int][]int {
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
func MergeExactDuplicates(items []types.MergedNewsItem, clusters map[int][]int) []types.MergedNewsItem {
	result := make([]types.MergedNewsItem, 0, len(clusters))
	for _, indices := range clusters {
		if len(indices) == 1 {
			result = append(result, items[indices[0]])
			continue
		}

		best := indices[0]
		for _, idx := range indices[1:] {
			if fetchrss.RankOf(items[idx].Source) < fetchrss.RankOf(items[best].Source) {
				best = idx
			} else if fetchrss.RankOf(items[idx].Source) == fetchrss.RankOf(items[best].Source) && items[idx].InterestScore > items[best].InterestScore {
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
// duplicateLink 决定重复对里谁「胜出」：先比 interest_score，再比来源档位。
// 胜出的一方保留，另一方被它引用——编排的第二遍会移除被引用项。
func duplicateLink(items []types.MergedNewsItem, i, j int) (winner, loser int) {
	si, sj := items[i].InterestScore, items[j].InterestScore
	if si != sj {
		if si > sj {
			return i, j
		}
		return j, i
	}
	if fetchrss.RankOf(items[i].Source) <= fetchrss.RankOf(items[j].Source) {
		return i, j
	}
	return j, i
}

// linkDuplicate 在胜出方上挂一条指向另一方的「相关」引用。
//
// 刻意只挂单向。编排第二遍的规则是「被任一入选条目的 Refs 引用到就移除」，
// 若像早期版本那样互挂，两条又都入选时会被一起删掉——整个事件从简报里消失。
func linkDuplicate(items []types.MergedNewsItem, i, j int, sim float64) {
	winner, loser := duplicateLink(items, i, j)
	items[winner].Refs = append(items[winner].Refs, types.NewsReference{
		DisplayTitle: items[loser].DisplayTitle,
		Source:       items[loser].Source,
		Link:         items[loser].Link,
		Similarity:   sim,
		RelationNote: "相关",
	})
}

// FindSimilarItems 找出同一事件的不同报道，并让高优先级的一方引用另一方，
// 交由编排的第二遍去重。两种判定路径：
//
//	相似度 ≥ cluster_threshold       直接认定（同一事件的不同措辞）
//	相似度 ∈ [verify_floor, 阈值)    批量交给 LLM 判断是否同一事件
//
// 之所以需要第二条路径：同一场发布会/同一份报告会产出多篇稿件，各自讲一个
// 侧面（一个讲数据、一个讲表态），标题差异大，向量相似度可能只有 0.4–0.6，
// 达不到阈值。2026-09-16 那期就有两组这样的稿件同时入选，占了一整期的一半。
func FindSimilarItems(ctx context.Context, p *NewsPipeline, items []types.MergedNewsItem) {
	if p.Embedding == nil || len(items) <= 1 {
		return
	}

	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.DisplayTitle + " " + item.Summary
	}
	vecs, err := embedding.CachedEmbedStrings(ctx, p.EmbedCache, p.Embedding, texts)
	if err != nil || len(vecs) != len(items) {
		return
	}

	threshold := p.GetClusterThreshold()
	floor := p.GetDedupVerifyFloor()

	var midBand []candidatePair
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			sim := embedding.CosineSimilarity(vecs[i], vecs[j])
			switch {
			case sim >= threshold && sim < 1.0:
				linkDuplicate(items, i, j, sim)
			case floor > 0 && sim >= floor && sim < threshold:
				midBand = append(midBand, candidatePair{i: i, j: j, sim: sim})
			}
		}
	}

	if len(midBand) == 0 {
		return
	}
	p.verifyMidBandDuplicates(ctx, items, midBand)
}

// candidatePair 是相似度落在中间带、需要 LLM 复核的一对条目。
type candidatePair struct {
	i, j int
	sim  float64
}

// verifyMidBandDuplicates 用一次 LLM 调用批量判断中间带候选是否同一事件，
// 判为是的挂上引用（随后由编排第二遍移除低优先级的一方）。
func (p *NewsPipeline) verifyMidBandDuplicates(ctx context.Context, items []types.MergedNewsItem, pairs []candidatePair) {
	if p.ChatModel == nil {
		return
	}

	// 按相似度降序，超出上限时只核查最像的那部分，控制单次提示词规模。
	sort.Slice(pairs, func(a, b int) bool { return pairs[a].sim > pairs[b].sim })
	if max := p.GetDedupVerifyMaxPairs(); len(pairs) > max {
		log.Printf("[Dedup] 中间带候选 %d 对，超过上限 %d，只核查相似度最高的部分", len(pairs), max)
		pairs = pairs[:max]
	}

	verdicts, err := p.verifySameEvent(ctx, items, pairs)
	if err != nil {
		// 核查失败只是少去一层重，不该影响整条管线
		log.Printf("[Dedup] 中间带核查失败，本轮跳过去重: %v", err)
		return
	}

	confirmed := 0
	for k, pair := range pairs {
		if !verdicts[k] {
			continue
		}
		linkDuplicate(items, pair.i, pair.j, pair.sim)
		confirmed++
		log.Printf("[Dedup] 判定同一事件（相似度 %.3f）: %q ↔ %q",
			pair.sim, items[pair.i].DisplayTitle, items[pair.j].DisplayTitle)
	}
	log.Printf("[Dedup] 中间带核查: %d 对候选（阈值 %.2f–%.2f），%d 对判定为同一事件",
		len(pairs), p.GetDedupVerifyFloor(), p.GetClusterThreshold(), confirmed)
}

// verifySameEvent 询问模型每一对是否报道同一事件，返回与 pairs 等长的判定。
func (p *NewsPipeline) verifySameEvent(ctx context.Context, items []types.MergedNewsItem, pairs []candidatePair) ([]bool, error) {
	var sb strings.Builder
	sb.WriteString("以下每对新闻，请判断是否在报道【同一事件】。\n")
	sb.WriteString("同一事件 = 同一场发布会、同一份报告或数据、同一次会议、同一起事故。\n")
	sb.WriteString("即使两篇稿件各自讲的要点不同（一篇讲数据、一篇讲表态），仍算同一事件。\n")
	sb.WriteString("只是话题相近（例如都属司法改革、都属新能源车）不算同一事件。\n\n")

	for idx, pair := range pairs {
		a, b := items[pair.i], items[pair.j]
		sb.WriteString(fmt.Sprintf("### 第 %d 对\n", idx+1))
		sb.WriteString(fmt.Sprintf("A：%s | %s\n", a.DisplayTitle, util.TruncateSummaryForLLM(a.Summary, 150)))
		sb.WriteString(fmt.Sprintf("B：%s | %s\n\n", b.DisplayTitle, util.TruncateSummaryForLLM(b.Summary, 150)))
	}
	sb.WriteString("请按顺序回复，每行一个词：同一事件 或 不同事件。只回复词语，不要其他内容。")

	messages := []*schema.Message{
		schema.SystemMessage("你是一名新闻编辑，负责判断两篇稿件是否报道同一事件。只有核心事件相同才算同一事件；话题相近、领域相同都不算。"),
		schema.UserMessage(sb.String()),
	}

	resp, err := p.ChatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLM 同一事件核查: %w", err)
	}

	verdicts := make([]bool, len(pairs))
	idx := 0
	for _, line := range strings.Split(resp.Content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx >= len(pairs) {
			break
		}
		// 先判「不同事件」：它包含「同事件」但不包含「同一事件」，顺序反了会误判
		switch {
		case strings.Contains(line, "不同事件"), strings.Contains(line, "不是同一事件"):
			verdicts[idx] = false
		case strings.Contains(line, "同一事件"):
			verdicts[idx] = true
		default:
			continue // 无法识别的行不计入，避免错位
		}
		idx++
	}
	if idx < len(pairs) {
		log.Printf("[Dedup] 中间带核查只解析出 %d/%d 个回答，未解析的按「不同事件」处理", idx, len(pairs))
	}
	return verdicts, nil
}

func (p *NewsPipeline) dedupAndLinkBatch(ctx context.Context, items []types.MergedNewsItem) []types.MergedNewsItem {
	clusters := DedupCluster(items)
	merged := MergeExactDuplicates(items, clusters)
	FindSimilarItems(ctx, p, merged)
	return merged
}
