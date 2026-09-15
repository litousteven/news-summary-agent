package tag

import (
	"testing"

	"github.com/litousteven/news-summary-agent/pipeline/types"
)

// 造一条原始条目；ID 形如 "中新网-7f87b45d"（GenerateItemID 的格式）。
func rawItem(id, title string) types.RawNewsItem {
	return types.RawNewsItem{
		ID:      id,
		Source:  "中新网",
		Title:   title,
		Summary: "摘要内容",
		Link:    "https://example.com/" + title,
	}
}

// 造一条模型返回的标注结果。
func tagResultJSON(id, displayTitle string) string {
	return `[{"id":"` + id + `","display_title":"` + displayTitle + `","category":"经济与交通",` +
		`"topic_tags":["测试"],"region":"中国","interest_score":8,"selected":true,"why_selected":"测试"}]`
}

// 核心回归：模型返回了无法对应任何原始条目的结果时，必须丢弃。
//
// 原实现在 ID / display_title / title 三级匹配全失败后仍然 append，产出一条
// source、title、summary、link 全为空、只有模型生成内容的条目，并一路推进简报
// ——等于凭空发布一条无法回溯到任何原文的「新闻」。2026-09-14 与 09-15 各发生一次，
// 后者被推送成「中国车企加快自研电池布局，导致宁德时代股价大幅下跌超过6%」。
func TestParseTaggedItems_DropsUnmatchedResult(t *testing.T) {
	raw := []types.RawNewsItem{
		rawItem("中新网-aaaaaaaa", "真实存在的新闻一"),
		rawItem("中新网-bbbbbbbb", "真实存在的新闻二"),
	}
	// 模型编了一条谁也对不上的结果
	msg := tagResultJSON("中新网-ffffffff", "中国车企加快自研电池布局，宁德时代股价大幅下跌超过6%")

	tagged, err := ParseTaggedItems(msg, raw, types.ValidCategories, ConvertCategoryDefs(types.CategoryDefs))
	if err != nil {
		t.Fatalf("ParseTaggedItems 返回错误: %v", err)
	}
	if len(tagged) != 0 {
		t.Fatalf("无法回溯的结果必须被丢弃，实际留下 %d 条: %+v", len(tagged), tagged)
	}
}

// 模型丢掉 ID 的「来源-」前缀时（来源名是中文时常见），按哈希后缀救回，
// 而不是丢弃——这类结果对应的往往是真实新闻。
func TestParseTaggedItems_RecoversByIDHashWhenPrefixLost(t *testing.T) {
	raw := []types.RawNewsItem{rawItem("中新网-40fa7427", "某条真实新闻")}
	// 模型只回了裸哈希
	msg := tagResultJSON("40fa7427", "改写后的展示标题")

	tagged, err := ParseTaggedItems(msg, raw, types.ValidCategories, ConvertCategoryDefs(types.CategoryDefs))
	if err != nil {
		t.Fatalf("ParseTaggedItems 返回错误: %v", err)
	}
	if len(tagged) != 1 {
		t.Fatalf("应救回 1 条，实际 %d 条", len(tagged))
	}
	// 关键：原始字段必须被绑回来
	if tagged[0].Source != "中新网" || tagged[0].Link == "" || tagged[0].Title != "某条真实新闻" {
		t.Errorf("原始字段未绑定: source=%q title=%q link=%q",
			tagged[0].Source, tagged[0].Title, tagged[0].Link)
	}
	// 展示标题仍用模型改写后的
	if tagged[0].DisplayTitle != "改写后的展示标题" {
		t.Errorf("DisplayTitle = %q", tagged[0].DisplayTitle)
	}
}

// 精确 ID 匹配仍是首选路径，且原始字段完整保留。
func TestParseTaggedItems_ExactIDMatch(t *testing.T) {
	raw := []types.RawNewsItem{rawItem("中新网-12345678", "原始标题")}
	msg := tagResultJSON("中新网-12345678", "改写标题")

	tagged, err := ParseTaggedItems(msg, raw, types.ValidCategories, ConvertCategoryDefs(types.CategoryDefs))
	if err != nil {
		t.Fatal(err)
	}
	if len(tagged) != 1 {
		t.Fatalf("应得 1 条，实际 %d", len(tagged))
	}
	if tagged[0].ID != "中新网-12345678" || tagged[0].Source != "中新网" || tagged[0].Summary == "" {
		t.Errorf("原始字段未完整保留: %+v", tagged[0].RawNewsItem)
	}
}

// 正常情况下条目数不应被改变（防止修复误伤）。
func TestParseTaggedItems_KeepsAllMatched(t *testing.T) {
	raw := []types.RawNewsItem{
		rawItem("中新网-aaaaaaaa", "新闻一"),
		rawItem("中新网-bbbbbbbb", "新闻二"),
		rawItem("中新网-cccccccc", "新闻三"),
	}
	msg := `[
	  {"id":"中新网-aaaaaaaa","display_title":"标题一","category":"经济与交通","topic_tags":["a"],"region":"中国","interest_score":7,"selected":true,"why_selected":"x"},
	  {"id":"中新网-bbbbbbbb","display_title":"标题二","category":"经济与交通","topic_tags":["b"],"region":"中国","interest_score":7,"selected":true,"why_selected":"x"},
	  {"id":"中新网-cccccccc","display_title":"标题三","category":"经济与交通","topic_tags":["c"],"region":"中国","interest_score":7,"selected":true,"why_selected":"x"}
	]`

	tagged, err := ParseTaggedItems(msg, raw, types.ValidCategories, ConvertCategoryDefs(types.CategoryDefs))
	if err != nil {
		t.Fatal(err)
	}
	if len(tagged) != 3 {
		t.Fatalf("应保留 3 条，实际 %d 条", len(tagged))
	}
	for _, item := range tagged {
		if item.Source == "" || item.Link == "" {
			t.Errorf("条目原始字段为空: %+v", item.RawNewsItem)
		}
	}
}

// idHash 的边界：带分隔符取后缀，无分隔符原样返回。
func TestIDHash(t *testing.T) {
	cases := map[string]string{
		"中新网-7f87b45d": "7f87b45d",
		"40fa7427":     "40fa7427",
		"a-b-c":        "c",
		"":             "",
		"tail-":        "tail-", // 末尾是分隔符时原样返回，不产生空哈希
	}
	for in, want := range cases {
		if got := idHash(in); got != want {
			t.Errorf("idHash(%q) = %q，期望 %q", in, got, want)
		}
	}
}
