package pipeline

import (
	"context"
	"os"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/joho/godotenv"

	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

// 真实模型的端到端核查，默认跳过：NEWS_E2E=1 才跑（需要 .env 里的模型配置与网络）。
//
// 用 2026-09-16 那期真实出现的两组重复做真阳性检验——它们是同一场发布会的
// 不同稿件，标题各异，向量相似度只有 0.46–0.60，够不着 cluster_threshold，
// 只能靠这里的 LLM 判断才能合并。
func TestE2E_SameEventVerification(t *testing.T) {
	if os.Getenv("NEWS_E2E") != "1" {
		t.Skip("设置 NEWS_E2E=1 才跑真实模型（会消耗额度）")
	}
	_ = godotenv.Load("../.env")

	modelName := os.Getenv("CHAT_MODEL_NAME")
	if modelName == "" {
		t.Skip("未配置 CHAT_MODEL_NAME，跳过")
	}
	m, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: os.Getenv("CHAT_MODEL_BASE_URL"),
		APIKey:  os.Getenv("CHAT_MODEL_API_KEY"),
		Model:   modelName,
	})
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}
	p := &NewsPipeline{ChatModel: m}

	item := func(title, summary string) types.MergedNewsItem {
		return types.MergedNewsItem{TaggedNewsItem: types.TaggedNewsItem{
			RawNewsItem:  types.RawNewsItem{Source: "中新网-中国", Summary: summary},
			DisplayTitle: title,
		}}
	}

	// 真阳性：同一场国新办发布会的两篇稿件（一个讲数据、一个讲表态）
	truePair := []types.MergedNewsItem{
		item("1至8月全国各级行政复议机关办理案件77.2万件",
			"国务院新闻办公室举行“开局起步‘十五五’”系列主题新闻发布会，介绍全面依法治国和司法行政工作情况。今年1至8月全国各级行政复议机关共办理案件77.2万件。"),
		item("司法部：持续纠治乱执法、滥执法问题",
			"国新办举行“开局起步‘十五五’”系列发布会，司法部表示将持续纠治乱执法、滥执法问题，介绍“十五五”时期推进全面依法治国和司法行政工作。"),
	}
	// 真阴性：同属反腐但完全不同的两个人
	falsePair := []types.MergedNewsItem{
		item("贵州省人大教科文卫委原副主任委员孔德明被开除党籍", "贵州省纪委监委对孔德明立案审查调查。"),
		item("贵州省政协原副秘书长王明亮被开除公职", "贵州省监委对王明亮严重违法问题立案调查。"),
	}

	verdicts, err := p.verifySameEvent(context.Background(), append(truePair, falsePair...),
		[]candidatePair{{i: 0, j: 1, sim: 0.459}, {i: 2, j: 3, sim: 0.718}})
	if err != nil {
		t.Fatalf("verifySameEvent: %v", err)
	}
	if !verdicts[0] {
		t.Errorf("同一场发布会的两篇稿件应判为同一事件（实际判为不同事件）")
	}
	if verdicts[1] {
		t.Errorf("两个不同官员的处分通报不应判为同一事件（实际判为同一事件）")
	}
}
