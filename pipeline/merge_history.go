package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// embedCandidate holds an embedding-matched pair pending LLM verification.
type embedCandidate struct {
	itemIdx int
	record  *PushHistoryRecord
	sim     float64
}

// mergeHistory merges tagged news items with push history for deduplication.
// Strategy: link exact match > display_title exact match > embedding screening > LLM verification.
func (p *NewsPipeline) mergeHistory(ctx context.Context, items []TaggedNewsItem) ([]MergedNewsItem, error) {
	// Load push history (today + yesterday)
	history, err := p.loadPushHistory()
	if err != nil {
		return nil, fmt.Errorf("load push history: %w", err)
	}
	if len(history) == 0 {
		// No history, all items are new
		result := make([]MergedNewsItem, len(items))
		for i, item := range items {
			result[i] = MergedNewsItem{
				TaggedNewsItem: item,
				ShouldPush:     true,
			}
		}
		return result, nil
	}

	// Build history embedding index
	var historyEmbeds [][]float64
	historyWithEmbed := make([]*PushHistoryRecord, 0)
	if p.Embedding != nil {
		// Collect history items that have embeddings already
		for i := range history {
			if len(history[i].Embedding) > 0 {
				historyWithEmbed = append(historyWithEmbed, &history[i])
				historyEmbeds = append(historyEmbeds, history[i].Embedding)
			}
		}

		// For history items without embeddings, compute them now
		var missing []string
		var missingIdx []int
		for i := range history {
			if len(history[i].Embedding) == 0 {
				missing = append(missing, history[i].DisplayTitle+" "+history[i].FactSummary)
				missingIdx = append(missingIdx, i)
			}
		}
		if len(missing) > 0 {
			vecs, err := p.Embedding.EmbedStrings(ctx, missing)
			if err == nil && len(vecs) == len(missing) {
				for j, idx := range missingIdx {
					history[idx].Embedding = vecs[j]
					historyWithEmbed = append(historyWithEmbed, &history[idx])
					historyEmbeds = append(historyEmbeds, vecs[j])
				}
			}
		}
	}

	// Compute embeddings for current items
	var itemEmbeds [][]float64
	if p.Embedding != nil && len(items) > 0 {
		texts := make([]string, len(items))
		for i, item := range items {
			texts[i] = item.DisplayTitle + " " + item.Summary
		}
		vecs, err := p.Embedding.EmbedStrings(ctx, texts)
		if err == nil && len(vecs) == len(items) {
			itemEmbeds = vecs
		}
	}

	// Build link index for history (highest priority dedup)
	historyByLink := make(map[string]*PushHistoryRecord)
	for i := range history {
		link := strings.TrimSpace(history[i].Link)
		if link != "" {
			historyByLink[link] = &history[i]
		}
	}

	// Build display_title index for history
	historyByTitle := make(map[string]*PushHistoryRecord)
	for i := range history {
		t := strings.TrimSpace(history[i].DisplayTitle)
		if t != "" {
			historyByTitle[t] = &history[i]
		}
	}

	// First pass: exact matches + collect embedding candidates for LLM verification
	var llmCandidates []embedCandidate

	result := make([]MergedNewsItem, len(items))
	for i, item := range items {
		result[i] = MergedNewsItem{
			TaggedNewsItem: item,
			ShouldPush:     true,
		}

		// 1. Exact link match (highest priority, most reliable)
		link := strings.TrimSpace(item.Link)
		if link != "" {
			if prev, ok := historyByLink[link]; ok {
				result[i].SeenBefore = true
				result[i].LastPushTime = prev.PushTime
				result[i].LastFactSummary = prev.FactSummary
				result[i].HistoryNote = fmt.Sprintf("链接完全匹配，时间：%s", prev.PushTime)
				if item.InterestScore < SeenBeforePushThreshold {
					result[i].ShouldPush = false
				}
				continue
			}
		}

		// 2. Exact display_title match
		title := strings.TrimSpace(item.DisplayTitle)
		if prev, ok := historyByTitle[title]; ok {
			result[i].SeenBefore = true
			result[i].LastPushTime = prev.PushTime
			result[i].LastFactSummary = prev.FactSummary
			result[i].HistoryNote = fmt.Sprintf("标题完全匹配，时间：%s", prev.PushTime)
			if item.InterestScore < SeenBeforePushThreshold {
				result[i].ShouldPush = false
			}
			continue
		}

		// 3. Embedding screening: collect candidates for LLM verification
		if len(itemEmbeds) > 0 && i < len(itemEmbeds) && len(itemEmbeds[i]) > 0 && len(historyEmbeds) > 0 {
			bestSim := 0.0
			var bestRecord *PushHistoryRecord
			for j, hEmb := range historyEmbeds {
				if j >= len(historyWithEmbed) {
					break
				}
				sim := cosineSimilarity(itemEmbeds[i], hEmb)
				if sim >= p.GetClusterThreshold() && sim > bestSim {
					bestSim = sim
					bestRecord = historyWithEmbed[j]
				}
			}
			if bestRecord != nil {
				llmCandidates = append(llmCandidates, embedCandidate{
					itemIdx: i,
					record:  bestRecord,
					sim:     bestSim,
				})
			}
		}
	}

	// Second pass: LLM verification for embedding candidates
	if len(llmCandidates) > 0 && p.ChatModel != nil {
		// Batch verify: group candidates to minimize LLM calls
		verified, err := p.llmVerifyDuplicates(ctx, items, result, llmCandidates)
		if err != nil {
			log.Printf("[MergeHistory] LLM核查失败: %v，回退到embedding直接判重", err)
			// Fallback: trust embedding similarity
			for _, c := range llmCandidates {
				result[c.itemIdx].SeenBefore = true
				result[c.itemIdx].LastPushTime = c.record.PushTime
				result[c.itemIdx].LastFactSummary = c.record.FactSummary
				result[c.itemIdx].HistoryNote = fmt.Sprintf("语义相似推送(%.2f)，时间：%s", c.sim, c.record.PushTime)
				if result[c.itemIdx].InterestScore < SeenBeforePushThreshold {
					result[c.itemIdx].ShouldPush = false
				}
			}
		} else {
			for idx, isDup := range verified {
				c := llmCandidates[idx]
				if isDup {
					result[c.itemIdx].SeenBefore = true
					result[c.itemIdx].LastPushTime = c.record.PushTime
					result[c.itemIdx].LastFactSummary = c.record.FactSummary
					result[c.itemIdx].HistoryNote = fmt.Sprintf("LLM确认重复(语义%.2f)，时间：%s", c.sim, c.record.PushTime)
					if result[c.itemIdx].InterestScore < SeenBeforePushThreshold {
						result[c.itemIdx].ShouldPush = false
					}
				}
				// If LLM says not duplicate, leave SeenBefore=false
			}
		}
	} else if len(llmCandidates) > 0 {
		// No ChatModel available, fallback to embedding direct judgment
		for _, c := range llmCandidates {
			result[c.itemIdx].SeenBefore = true
			result[c.itemIdx].LastPushTime = c.record.PushTime
			result[c.itemIdx].LastFactSummary = c.record.FactSummary
			result[c.itemIdx].HistoryNote = fmt.Sprintf("语义相似推送(%.2f)，时间：%s", c.sim, c.record.PushTime)
			if result[c.itemIdx].InterestScore < SeenBeforePushThreshold {
				result[c.itemIdx].ShouldPush = false
			}
		}
	}

	return result, nil
}

// llmVerifyDuplicates uses LLM to verify if embedding-matched candidates are truly duplicates.
// Returns a boolean slice where true means "is duplicate".
func (p *NewsPipeline) llmVerifyDuplicates(ctx context.Context, items []TaggedNewsItem, result []MergedNewsItem, candidates []embedCandidate) ([]bool, error) {
	// Build verification prompt
	var sb strings.Builder
	sb.WriteString("请判断以下新闻对是否描述的是同一个事件。对于每一对，回复 是 或 否。\n\n")

	for idx, c := range candidates {
		item := items[c.itemIdx]
		sb.WriteString(fmt.Sprintf("### 新闻对 %d\n", idx+1))
		sb.WriteString(fmt.Sprintf("**当前新闻**：标题：%s | 摘要：%s\n", item.DisplayTitle, truncateForLLM(item.Summary, 200)))
		sb.WriteString(fmt.Sprintf("**历史新闻**：标题：%s | 摘要：%s\n", c.record.DisplayTitle, truncateForLLM(c.record.FactSummary, 200)))
		sb.WriteString(fmt.Sprintf("（向量相似度：%.2f）\n\n", c.sim))
	}

	sb.WriteString("请按顺序回复，每行一个 是 或 否。只回复是/否，不要其他内容。")

	messages := []*schema.Message{
		schema.SystemMessage("你是一名新闻去重核查员。判断两条新闻是否描述同一事件。只有核心事件相同时才回答 是。"),
		schema.UserMessage(sb.String()),
	}

	resp, err := p.ChatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLM verify call: %w", err)
	}

	// Parse LLM response: extract 是/否 for each pair
	lines := strings.Split(resp.Content, "\n")
	verified := make([]bool, len(candidates))
	answerIdx := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if answerIdx >= len(candidates) {
			break
		}
		if strings.Contains(line, "是") && !strings.Contains(line, "否") {
			verified[answerIdx] = true
			answerIdx++
		} else if strings.Contains(line, "否") {
			verified[answerIdx] = false
			answerIdx++
		}
	}

	// If LLM didn't answer all, default to embedding judgment for remaining
	for i := answerIdx; i < len(candidates); i++ {
		verified[i] = true // fallback: trust embedding
	}

	return verified, nil
}

// truncateForLLM truncates text for LLM prompt context.
func truncateForLLM(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// loadPushHistory reads push history from today's and yesterday's per-day files.
func (p *NewsPipeline) loadPushHistory() ([]PushHistoryRecord, error) {
	now := time.Now()
	var records []PushHistoryRecord

	for _, t := range []time.Time{now, now.Add(-24 * time.Hour)} {
		path := p.DataDir + "/push_history_" + t.Format("20060102") + ".jsonl"
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var r PushHistoryRecord
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				continue
			}
			records = append(records, r)
		}
	}
	return records, nil
}
