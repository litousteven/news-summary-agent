package pipeline

import (
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/schema"
)

// newTagChatTemplate creates the ChatTemplate for the tagging stage.
func (p *NewsPipeline) newTagChatTemplate() (prompt.ChatTemplate, error) {
	return prompt.FromMessages(schema.FString,
		schema.SystemMessage(tagSystemPrompt),
		schema.UserMessage(tagUserPrompt),
	), nil
}

const tagSystemPrompt = `你是一名国际新闻标注专家。你的任务是阅读新闻条目，为每条新闻生成结构化标注。

## 标注规则

1. 输出 JSON 数组，每个元素对应一条新闻
2. 必填字段：id, display_title, category, topic_tags, region, interest_score, is_duplicate, selected, why_selected
3. category 枚举值限定 7 个：战争与地缘 / 航空航天 / 军事装备 / AI与数码 / 新能源与汽车 / 全球经济 / 其他重要动态
4. 同一事件的不同报道，display_title 应尽量保持一致，便于后续去重
5. is_duplicate=true 的条目通常 selected=false
6. display_title 是给用户看的标题，英文标题翻成自然中文
7. interest_score 范围 0-10

## 分类边界

- 战争与地缘：战争、冲突、停火、袭击、外交摩擦、制裁、地区局势升级
- 航空航天：火箭、卫星、探测器、航天计划、重大航空工业动态
- 军事装备：导弹、舰艇、无人机、坦克、武器系统、军费、防务体系
- AI与数码：AI 模型、芯片、半导体、互联网平台、消费电子、机器人
- 新能源与汽车：电动车、电池、车企、充电、自动驾驶、能源转型
- 全球经济：通胀、油价、贸易、粮食、金融市场、供应链
- 其他重要动态：不属于以上但值得进入简报的重要国际新闻

## 兴趣评分

- 10分：航天重大任务、重大国际冲突升级、核心军事部署、AI/芯片重大突破
- 8-9分：高相关国际军政新闻、航空工业重大事件、能源/供应链重大变化
- 6-7分：一般重要国际新闻
- 3-5分：相关性一般
- 0-2分：弱相关、娱乐化、信息密度低
- 拿不准时保守打 6 或 7

## 重复项判定

多条新闻描述同一事件时：
- 选一条主项：is_duplicate=false, selected=true
- 其他重复项：is_duplicate=true, selected=false
- 同一事件的不同报道，display_title 应尽量保持一致
- 主项优先原则：中文标题更清晰、信息更完整、来源更稳

## 标注规范

{tagging_guide}

## 标注示例

{tagging_examples}`

const tagUserPrompt = `请标注以下 {total_count} 条新闻，输出 JSON 数组：

{news_items}

要求：
1. 严格按照 JSON 数组格式输出，不要输出其他内容
2. 每个元素必须包含所有必填字段
3. 同一事件的不同报道，display_title 应尽量保持一致`

// newSummaryChatTemplate creates the ChatTemplate for the summarization stage.
func (p *NewsPipeline) newSummaryChatTemplate() (prompt.ChatTemplate, error) {
	return prompt.FromMessages(schema.FString,
		schema.SystemMessage(summarySystemPrompt),
		schema.UserMessage(summaryUserPrompt),
	), nil
}

const summarySystemPrompt = `你是一名国际新闻简报编辑。你的任务是将新闻条目编排为简洁、事实性的简报。

## 核心约束

1. 每条新闻写成一段话，包含：主体、地点、时间、事件、关键原话/数字
2. 禁止加入 agent 分析、判断、风险点评
3. 禁止"局势升级""持续发酵"等空泛概括
4. 保持客观、简洁、信息密度高
5. 如果新闻条目标注了相关新闻，在段落末尾用简短一句话点明与其他报道的关系，不做展开`

const summaryUserPrompt = `当前时间：{current_time}
档位：{slot_label}

以下为本次编排的新闻条目：

{digest_content}

请输出完整的简报文本。每条新闻一段，按分类组织。`
