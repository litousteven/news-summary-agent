package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/litousteven/news-summary-agent/pipeline/types"
)

// digItem builds a DigestItem with the embedded type chain filled in,
// so tests stay readable (DigestItem -> MergedNewsItem -> TaggedNewsItem -> RawNewsItem).
func digItem(category, source, link, factParagraph, itemSummary string, refs []types.NewsReference) types.DigestItem {
	return types.DigestItem{
		MergedNewsItem: types.MergedNewsItem{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem: types.RawNewsItem{
					Source: source,
					Link:   link,
				},
				Category: category,
			},
			Refs: refs,
		},
		FactParagraph: factParagraph,
		ItemSummary:   itemSummary,
	}
}

func readOnlyDigestMD(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取输出目录失败: %v", err)
	}
	var found string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "digest_") && strings.HasSuffix(e.Name(), ".md") {
			if found != "" {
				t.Fatalf("期望只生成一个 digest md，实际有多个: %s / %s", found, e.Name())
			}
			found = e.Name()
		}
	}
	if found == "" {
		t.Fatal("没有生成 digest_*.md 文件")
	}
	body, err := os.ReadFile(filepath.Join(dir, found))
	if err != nil {
		t.Fatalf("读取 digest 文件失败: %v", err)
	}
	return string(body)
}

// 核心回归：md 必须优先使用中文 ItemSummary，不能泄漏 FactParagraph 里的英文原文。
func TestWriteDigestMDPrefersItemSummary(t *testing.T) {
	dir := t.TempDir()
	result := &types.NewsSummaryResult{
		DigestItems: []types.DigestItem{
			digItem(
				"国际关系与地缘政治", "联合早报", "https://example.com/a",
				"中文标题。US and Saudi forces struck Iran-backed militants in Iraq.",
				"美沙联军空袭伊拉克境内受伊朗支持的恐怖分子。",
				nil,
			),
			digItem(
				"国际关系与地缘政治", "NYT", "https://example.com/b",
				"尼泊尔洪水。Survivors tell the stories of the people buried under the mud.",
				"尼泊尔喜马拉雅地区发生洪水，城镇被毁。",
				nil,
			),
		},
		Stats: types.DigestStats{TotalFetched: 120, TotalTagged: 120, TotalSelected: 2},
	}

	if err := writeDigestMD(dir, result); err != nil {
		t.Fatalf("writeDigestMD 返回错误: %v", err)
	}
	body := readOnlyDigestMD(t, dir)

	for _, want := range []string{
		"- 美沙联军空袭伊拉克境内受伊朗支持的恐怖分子。",
		"- 尼泊尔喜马拉雅地区发生洪水，城镇被毁。",
		"[联合早报](https://example.com/a)",
		"[NYT](https://example.com/b)",
		"## 国际关系与地缘政治",
		"统计: 抓取=120, 标注=120, 入选=2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("输出缺少 %q\n--- 实际输出 ---\n%s", want, body)
		}
	}

	for _, unwanted := range []string{
		"US and Saudi forces struck",
		"Survivors tell the stories",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("输出不应包含英文原文 %q（应已被 ItemSummary 覆盖）\n--- 实际输出 ---\n%s", unwanted, body)
		}
	}
}

// ItemSummary 为空时必须回退到 FactParagraph，且无链接时走「来源:」分支。
func TestWriteDigestMDFallsBackToFactParagraph(t *testing.T) {
	dir := t.TempDir()
	result := &types.NewsSummaryResult{
		DigestItems: []types.DigestItem{
			digItem(
				"AI与数码", "NPR", "",
				"回退用的原始事实段落。",
				"", // 摘要生成失败
				nil,
			),
		},
		Stats: types.DigestStats{TotalFetched: 5, TotalTagged: 5, TotalSelected: 1},
	}

	if err := writeDigestMD(dir, result); err != nil {
		t.Fatalf("writeDigestMD 返回错误: %v", err)
	}
	body := readOnlyDigestMD(t, dir)

	if !strings.Contains(body, "- 回退用的原始事实段落。") {
		t.Errorf("ItemSummary 为空时应回退到 FactParagraph\n--- 实际输出 ---\n%s", body)
	}
	if !strings.Contains(body, "来源: NPR") {
		t.Errorf("无链接时应输出「来源: NPR」\n--- 实际输出 ---\n%s", body)
	}
}

// 核心回归（对应 commit 294a24a 引入的副作用）：改用 ItemSummary 之后，
// LLM 摘要会把 FactParagraph 里的日期洗掉，导致 7 周前的旧闻看起来像当天的。
// DatePrefix 必须补回来；而回退到 FactParagraph 时不能重复加日期。
func TestWriteDigestMDPreservesPublishDate(t *testing.T) {
	dir := t.TempDir()

	withSummary := digItem(
		"经济与交通", "联合早报-中国", "https://example.com/quant",
		"中国7月28日，量化砸盘成A股最大隐忧？",
		"A股近期承压下行，晶片股遭抛售。",
		nil,
	)
	withSummary.DatePrefix = "7月28日，"

	fallback := digItem(
		"自然灾害与气候事件", "NYT", "https://example.com/flood",
		"南亚9月13日，喜马拉雅洪水过后，尼泊尔城镇被毁。",
		"", // 摘要生成失败，回退 FactParagraph（已自带日期）
		nil,
	)

	result := &types.NewsSummaryResult{
		DigestItems: []types.DigestItem{withSummary, fallback},
		Stats:       types.DigestStats{TotalFetched: 2, TotalTagged: 2, TotalSelected: 2},
	}

	if err := writeDigestMD(dir, result); err != nil {
		t.Fatalf("writeDigestMD 返回错误: %v", err)
	}
	body := readOnlyDigestMD(t, dir)

	if want := "- 7月28日，A股近期承压下行，晶片股遭抛售。"; !strings.Contains(body, want) {
		t.Errorf("ItemSummary 分支应补回发布日期，期望包含 %q\n--- 实际输出 ---\n%s", want, body)
	}
	if want := "- 南亚9月13日，喜马拉雅洪水过后，尼泊尔城镇被毁。"; !strings.Contains(body, want) {
		t.Errorf("回退分支应保持原样，期望包含 %q\n--- 实际输出 ---\n%s", want, body)
	}
	if strings.Contains(body, "7月28日，7月28日，") || strings.Contains(body, "南亚南亚") {
		t.Errorf("日期或地区被重复拼接\n--- 实际输出 ---\n%s", body)
	}
}

// Refs 仍按 markdown 链接渲染，且分类切换时会插入空行。
func TestWriteDigestMDRendersRefsAndCategories(t *testing.T) {
	dir := t.TempDir()
	result := &types.NewsSummaryResult{
		DigestItems: []types.DigestItem{
			digItem("战争与地缘", "BBC", "https://example.com/x", "fact", "摘要一",
				[]types.NewsReference{
					{DisplayTitle: "前情标题", Source: "FOX News", Link: "https://example.com/ref", RelationNote: "相关"},
					{DisplayTitle: "无链接参考", Source: "CNN", RelationNote: "前情回顾"},
				}),
			digItem("航空航天", "中新网", "https://example.com/y", "fact", "摘要二", nil),
		},
		Stats: types.DigestStats{TotalFetched: 2, TotalTagged: 2, TotalSelected: 2},
	}

	if err := writeDigestMD(dir, result); err != nil {
		t.Fatalf("writeDigestMD 返回错误: %v", err)
	}
	body := readOnlyDigestMD(t, dir)

	for _, want := range []string{
		"> 相关: [前情标题](https://example.com/ref)（FOX News）",
		"> 前情回顾: 无链接参考（CNN）",
		"## 战争与地缘",
		"## 航空航天",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("输出缺少 %q\n--- 实际输出 ---\n%s", want, body)
		}
	}
}

// ChatModel 必须带 120s 超时，否则上游卡住会拖死整条管线（标注批次之外的调用
// 没有别的兜底）。同时确认 JSON 模式只影响 response_format。
func TestBuildChatModelConfigWiresTimeoutAndJSONMode(t *testing.T) {
	plain := buildChatModelConfig("test-key", "https://example.com/v1", "test-model", false)

	if plain.Timeout != 120*time.Second {
		t.Errorf("ChatModel 超时应为 120s，实际 %v", plain.Timeout)
	}
	if plain.MaxTokens == nil || *plain.MaxTokens != 16384 {
		t.Errorf("MaxTokens 应为 16384，实际 %v", plain.MaxTokens)
	}
	if plain.ResponseFormat != nil {
		t.Errorf("非 JSON 模式不应设置 ResponseFormat，实际 %+v", plain.ResponseFormat)
	}
	if plain.APIKey != "test-key" || plain.BaseURL != "https://example.com/v1" || plain.Model != "test-model" {
		t.Errorf("基础字段未正确透传: %+v", plain)
	}

	jsonCfg := buildChatModelConfig("test-key", "", "test-model", true)
	if jsonCfg.ResponseFormat == nil || jsonCfg.ResponseFormat.Type != "json_object" {
		t.Errorf("JSON 模式应设置 response_format=json_object，实际 %+v", jsonCfg.ResponseFormat)
	}
	if jsonCfg.Timeout != 120*time.Second {
		t.Errorf("JSON 模式同样需要超时，实际 %v", jsonCfg.Timeout)
	}
}
