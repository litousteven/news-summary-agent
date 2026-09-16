package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

// fakeChatModel 按固定回复返回，用来测「同一事件」核查这条路径。
type fakeChatModel struct {
	reply string
	err   error
	calls int
	last  string
}

func (f *fakeChatModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.calls++
	if len(in) > 0 {
		f.last = in[len(in)-1].Content
	}
	if f.err != nil {
		return nil, f.err
	}
	return schema.AssistantMessage(f.reply, nil), nil
}

func (f *fakeChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not implemented")
}

func midBandItems() []types.MergedNewsItem {
	return []types.MergedNewsItem{
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem:   types.RawNewsItem{Source: "中新网-中国", Link: "https://e.com/a", Summary: "1至8月行政复议案件77.2万件"},
				DisplayTitle:  "1至8月全国各级行政复议机关办理案件77.2万件",
				InterestScore: 7,
			},
		},
		{
			TaggedNewsItem: types.TaggedNewsItem{
				RawNewsItem:   types.RawNewsItem{Source: "中新网-中国", Link: "https://e.com/b", Summary: "司法部表示持续纠治乱执法"},
				DisplayTitle:  "司法部：持续纠治乱执法、滥执法问题",
				InterestScore: 7,
			},
		},
	}
}

// 重复对只应单向挂引用：胜出方指向落败方。
//
// 早期版本互挂，而编排第二遍的规则是「被任一入选条目的 Refs 引用到就移除」，
// 两条都入选时会被一起删掉——整个事件从简报里消失。
func TestLinkDuplicate_OnlyWinnerReferencesLoser(t *testing.T) {
	items := midBandItems()
	linkDuplicate(items, 0, 1, 0.46)

	if len(items[0].Refs) != 1 || len(items[1].Refs) != 0 {
		t.Fatalf("应只有一方持有引用: refs[0]=%d refs[1]=%d", len(items[0].Refs), len(items[1].Refs))
	}
	if items[0].Refs[0].Link != items[1].Link {
		t.Errorf("引用应指向落败方，实际 %q", items[0].Refs[0].Link)
	}
	if items[0].Refs[0].Similarity != 0.46 {
		t.Errorf("相似度应保留，实际 %v", items[0].Refs[0].Similarity)
	}
}

// 分数相同时看来源档位；中新网-中国(5) 优于 FOX News(8)。
func TestLinkDuplicate_WinnerBySourceRankWhenScoreTies(t *testing.T) {
	items := midBandItems()
	items[1].Source = "FOX News"
	linkDuplicate(items, 1, 0, 0.5) // 故意把低档位的放在第一个参数

	if len(items[0].Refs) != 1 {
		t.Fatalf("档位更高的 中新网-中国 应胜出并持有引用，实际 refs[0]=%d refs[1]=%d",
			len(items[0].Refs), len(items[1].Refs))
	}
}

// 分数不同时以分数为准。
func TestLinkDuplicate_WinnerByScore(t *testing.T) {
	items := midBandItems()
	items[1].InterestScore = 9
	linkDuplicate(items, 0, 1, 0.5)

	if len(items[1].Refs) != 1 {
		t.Fatalf("分数更高的应胜出，实际 refs[0]=%d refs[1]=%d", len(items[0].Refs), len(items[1].Refs))
	}
}

// 模型判为同一事件 → 挂引用（随后由编排第二遍去重）。
func TestVerifyMidBandDuplicates_MergesConfirmedPairs(t *testing.T) {
	items := midBandItems()
	p := &NewsPipeline{ChatModel: &fakeChatModel{reply: "同一事件"}}

	p.verifyMidBandDuplicates(context.Background(), items, []candidatePair{{i: 0, j: 1, sim: 0.46}})

	if len(items[0].Refs) != 1 {
		t.Errorf("判为同一事件后应有引用，实际 %d", len(items[0].Refs))
	}
}

// 模型判为不同事件 → 不挂引用。
func TestVerifyMidBandDuplicates_KeepsDistinctEvents(t *testing.T) {
	items := midBandItems()
	p := &NewsPipeline{ChatModel: &fakeChatModel{reply: "不同事件"}}

	p.verifyMidBandDuplicates(context.Background(), items, []candidatePair{{i: 0, j: 1, sim: 0.42}})

	if len(items[0].Refs) != 0 || len(items[1].Refs) != 0 {
		t.Errorf("判为不同事件不应挂引用，实际 %d/%d", len(items[0].Refs), len(items[1].Refs))
	}
}

// 核查失败不能影响管线其余部分。
func TestVerifyMidBandDuplicates_LLMFailureIsNonFatal(t *testing.T) {
	items := midBandItems()
	p := &NewsPipeline{ChatModel: &fakeChatModel{err: errors.New("模型超时")}}

	p.verifyMidBandDuplicates(context.Background(), items, []candidatePair{{i: 0, j: 1, sim: 0.5}})

	if len(items[0].Refs) != 0 || len(items[1].Refs) != 0 {
		t.Error("核查失败时不应挂引用")
	}
}

// 没有模型时直接跳过，不 panic。
func TestVerifyMidBandDuplicates_NoChatModel(t *testing.T) {
	items := midBandItems()
	p := &NewsPipeline{}
	p.verifyMidBandDuplicates(context.Background(), items, []candidatePair{{i: 0, j: 1, sim: 0.5}})
}

// 回答解析：按顺序对应，「不同事件」不能被「同一事件」误判。
func TestVerifySameEvent_ParsesAnswersInOrder(t *testing.T) {
	items := midBandItems()
	p := &NewsPipeline{ChatModel: &fakeChatModel{reply: "同一事件\n不同事件\n"}}

	verdicts, err := p.verifySameEvent(context.Background(), items, []candidatePair{
		{i: 0, j: 1, sim: 0.5}, {i: 1, j: 0, sim: 0.45},
	})
	if err != nil {
		t.Fatalf("verifySameEvent: %v", err)
	}
	if len(verdicts) != 2 || !verdicts[0] || verdicts[1] {
		t.Errorf("回答解析错误: %v（「不同事件」含「同事件」，顺序判错会误判为同一事件）", verdicts)
	}
}

// 回答数量不足时，未解析的按「不同事件」处理，不做臆测。
func TestVerifySameEvent_MissingAnswersDefaultToFalse(t *testing.T) {
	items := midBandItems()
	p := &NewsPipeline{ChatModel: &fakeChatModel{reply: "同一事件\n"}}

	verdicts, err := p.verifySameEvent(context.Background(), items, []candidatePair{
		{i: 0, j: 1}, {i: 1, j: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verdicts[0] {
		t.Error("第 1 个回答应为 true")
	}
	if verdicts[1] {
		t.Error("未解析的回答应为 false（宁可不合并）")
	}
}

// 提示词里应带上双方标题，供模型判断是否同一事件。
func TestVerifySameEvent_PromptCarriesBothTitles(t *testing.T) {
	items := midBandItems()
	fake := &fakeChatModel{reply: "不同事件"}
	p := &NewsPipeline{ChatModel: fake}
	_, _ = p.verifySameEvent(context.Background(), items, []candidatePair{{i: 0, j: 1, sim: 0.5}})

	for _, want := range []string{"行政复议", "纠治乱执法", "同一事件"} {
		if !strings.Contains(fake.last, want) {
			t.Errorf("提示词应包含 %q", want)
		}
	}
}

// 端到端：一对被判为同一事件的条目，经编排后只保留一条（胜出方）。
func TestBuildDigest_KeepsSingleItemForConfirmedDuplicate(t *testing.T) {
	items := midBandItems()
	items[0].Category = "国内事务"
	items[1].Category = "国内事务"
	linkDuplicate(items, 0, 1, 0.46)

	p := &NewsPipeline{}
	digest, err := p.buildDigest(context.Background(), items)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.Items) != 1 {
		t.Fatalf("同一事件的两条应只剩 1 条，实际 %d 条", len(digest.Items))
	}
	if digest.Items[0].Link != "https://e.com/a" {
		t.Errorf("应保留胜出方 https://e.com/a，实际 %q", digest.Items[0].Link)
	}
}
