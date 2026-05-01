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
	merged := make([]MergedNewsItem, len(items))
	for i, item := range items {
		merged[i] = MergedNewsItem{TaggedNewsItem: item}
	}

	history, err := p.loadPushHistory()
	if err != nil {
		return nil, fmt.Errorf("load push history: %w", err)
	}
	if len(history) == 0 {
		for i := range merged {
			merged[i].SeenBefore = false
		}
		return merged, nil
	}

	var historyEmbeds [][]float64
	historyWithEmbed := make([]*PushHistoryRecord, 0)
	if p.Embedding != nil {
		for i := range history {
			if len(history[i].Embedding) > 0 {
				historyWithEmbed = append(historyWithEmbed, &history[i])
				historyEmbeds = append(historyEmbeds, history[i].Embedding)
			}
		}

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

	var itemEmbeds [][]float64
	if p.Embedding != nil && len(merged) > 0 {
		texts := make([]string, len(merged))
		for i, item := range merged {
			texts[i] = item.DisplayTitle + " " + item.Summary
		}
		vecs, err := p.Embedding.EmbedStrings(ctx, texts)
		if err == nil && len(vecs) == len(merged) {
			itemEmbeds = vecs
		}
	}

	historyByLink := make(map[string]*PushHistoryRecord)
	for i := range history {
		link := strings.TrimSpace(history[i].Link)
		if link != "" {
			historyByLink[link] = &history[i]
		}
	}

	historyByTitle := make(map[string]*PushHistoryRecord)
	for i := range history {
		t := strings.TrimSpace(history[i].DisplayTitle)
		if t != "" {
			historyByTitle[t] = &history[i]
		}
	}

	var llmCandidates []embedCandidate

	for i, item := range merged {
		link := strings.TrimSpace(item.Link)
		if link != "" {
			if prev, ok := historyByLink[link]; ok {
				merged[i].SeenBefore = true
				merged[i].HistoryNote = fmt.Sprintf("链接完全匹配，上次推送：%s", prev.PushTime)
				continue
			}
		}

		title := strings.TrimSpace(item.DisplayTitle)
		if prev, ok := historyByTitle[title]; ok {
			merged[i].SeenBefore = true
			merged[i].HistoryNote = fmt.Sprintf("标题完全匹配，上次推送：%s", prev.PushTime)
			continue
		}

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

	if len(llmCandidates) > 0 && p.ChatModel != nil {
		verified, err := p.llmVerifyDuplicatesMerged(ctx, merged, llmCandidates)
		if err != nil {
			log.Printf("[MergeHistory] LLM核查失败: %v，回退到embedding直接判重", err)
			for _, c := range llmCandidates {
				merged[c.itemIdx].SeenBefore = true
				merged[c.itemIdx].HistoryNote = fmt.Sprintf("语义相似推送(%.2f)，上次推送：%s", c.sim, c.record.PushTime)
			}
		} else {
			for idx, vr := range verified {
				c := llmCandidates[idx]
				if vr.IsDuplicate {
					merged[c.itemIdx].SeenBefore = true
					merged[c.itemIdx].HistoryNote = fmt.Sprintf("LLM确认重复(语义%.2f)，上次推送：%s", c.sim, c.record.PushTime)
				} else if vr.IsReference {
					ref := NewsReference{
						DisplayTitle: c.record.DisplayTitle,
						Source:       c.record.Source,
						Link:         c.record.Link,
						PushTime:     c.record.PushTime,
						FactSummary:  c.record.FactSummary,
						RelationNote: vr.Note,
					}
					merged[c.itemIdx].Refs = append(merged[c.itemIdx].Refs, ref)
				}
			}
		}
	} else if len(llmCandidates) > 0 {
		for _, c := range llmCandidates {
			merged[c.itemIdx].SeenBefore = true
			merged[c.itemIdx].HistoryNote = fmt.Sprintf("语义相似推送(%.2f)，上次推送：%s", c.sim, c.record.PushTime)
		}
	}

	for i := range merged {
		if len(merged[i].Refs) > 2 {
			merged[i].Refs = merged[i].Refs[:2]
		}
	}

	return merged, nil
}

// verifyResult holds the LLM verification outcome for one candidate pair.
type verifyResult struct {
	IsDuplicate bool
	IsReference bool
	Note        string
}

// llmVerifyDuplicatesMerged uses LLM to verify if embedding-matched candidates are truly duplicates.
func (p *NewsPipeline) llmVerifyDuplicatesMerged(ctx context.Context, items []MergedNewsItem, candidates []embedCandidate) ([]verifyResult, error) {
	var sb strings.Builder
	sb.WriteString("请判断以下新闻对的关系。对于每一对，回复以下三种之一：\n")
	sb.WriteString("- 重复：两条新闻描述的是同一个事件，内容没有实质进展\n")
	sb.WriteString("- 进展：两条新闻相关，但当前新闻有新的实质进展（如：事态升级、结果出炉、新增细节）\n")
	sb.WriteString("- 反转：两条新闻相关，但当前新闻对之前的报道有实质反转\n")
	sb.WriteString("- 无关：两条新闻不相关\n\n")

	for idx, c := range candidates {
		item := items[c.itemIdx]
		sb.WriteString(fmt.Sprintf("### 新闻对 %d\n", idx+1))
		sb.WriteString(fmt.Sprintf("**当前新闻**：标题：%s | 摘要：%s\n", item.DisplayTitle, truncateForLLM(item.Summary, 200)))
		sb.WriteString(fmt.Sprintf("**历史新闻**：标题：%s | 摘要：%s\n", c.record.DisplayTitle, truncateForLLM(c.record.FactSummary, 200)))
		sb.WriteString(fmt.Sprintf("（向量相似度：%.2f）\n\n", c.sim))
	}

	sb.WriteString("请按顺序回复，每行一个关键词：重复 / 进展 / 反转 / 无关。只回复关键词，不要其他内容。")

	messages := []*schema.Message{
		schema.SystemMessage("你是一名新闻去重与关联核查员。判断两条新闻的关系。只有核心事件完全相同且无新进展时才回答 重复；有新的实质进展回答 进展；有实质反转回答 反转；不相关回答 无关。"),
		schema.UserMessage(sb.String()),
	}

	resp, err := p.ChatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLM verify call: %w", err)
	}

	lines := strings.Split(resp.Content, "\n")
	results := make([]verifyResult, len(candidates))
	answerIdx := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if answerIdx >= len(candidates) {
			break
		}
		if strings.Contains(line, "重复") && !strings.Contains(line, "进展") && !strings.Contains(line, "反转") {
			results[answerIdx] = verifyResult{IsDuplicate: true}
			answerIdx++
		} else if strings.Contains(line, "进展") {
			results[answerIdx] = verifyResult{IsReference: true, Note: "相关"}
			answerIdx++
		} else if strings.Contains(line, "反转") {
			results[answerIdx] = verifyResult{IsReference: true, Note: "相关"}
			answerIdx++
		} else if strings.Contains(line, "无关") {
			results[answerIdx] = verifyResult{}
			answerIdx++
		}
	}

	for i := answerIdx; i < len(candidates); i++ {
		results[i] = verifyResult{IsDuplicate: true}
	}

	return results, nil
}

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
