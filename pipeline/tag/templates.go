package tag

const SystemPrompt = `你是一名国际新闻标注专家。你的任务是阅读新闻条目，为每条新闻生成结构化标注。

## 标注规则

1. 输出 JSON 对象，格式为 {{"items": [...]}}，items 数组中每个元素对应一条新闻
2. 必填字段：id, display_title, category, topic_tags, region, interest_score, why_selected
3. {categories}
4. display_title 是给用户看的标题，英文标题翻成自然中文
5. interest_score 范围 0-10

## 标注规范（来自 tagging_guide.md）

{tagging_guide}

## 标注示例（来自 tagging_examples）

{tagging_examples}`

const UserPrompt = `请标注以下 {total_count} 条新闻：

{news_items}

要求：
1. 严格按照 {{"items": [...]}} 格式输出，不要输出其他内容
2. 每个元素必须包含所有必填字段`

const DefaultTaggingGuide = `## 标注规范 v3

### display_title 规则
- 最终给用户看的标题
- 英文标题翻成自然中文
- 可以润色但不改事实

### interest_score 规则
- 0-10，越高越值得入选简报
- 重复报道、弱相关、信息价值低的条目给低分`

const DefaultTaggingExamples = `title,display_title,category,topic_tags,region,interest_score,why_selected
伊朗袭击迪拜附近油轮，地区战事持续升级,伊朗袭击迪拜附近油轮，地区战事持续升级,战争与地缘,"伊朗,迪拜,油轮,中东",中东,9,中东冲突升级且与能源运输相关
Kuwaiti Tanker Full of Oil Struck Off Dubai,特朗普发出威胁次日满载原油的科威特油轮在迪拜附近遭袭,战争与地缘,"迪拜,油轮,中东,能源",中东,8,同一事件另一来源的报道
SpaceX launches next-generation Starlink satellites,SpaceX 发射新一代 Starlink 卫星,航空航天,"SpaceX,卫星,火箭,航天",北美,10,高度符合用户航天兴趣
Japan deploys long-range missiles,日本在熊本和静冈部署远程导弹,军事装备,"日本,导弹,部署,防务",东亚,9,典型军事部署新闻
Nvidia unveils new AI chip architecture,英伟达发布新一代 AI 芯片架构,AI与数码,"AI,芯片,Nvidia,半导体",北美,10,高度符合 AI/数码兴趣`
