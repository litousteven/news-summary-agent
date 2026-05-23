package tag

const SystemPrompt = `你是一名国际新闻标注专家。你的任务是阅读新闻条目，为每条新闻生成结构化标注。

## 标注规则

1. 输出 JSON 数组，每个元素对应一条新闻
2. 必填字段：id, display_title, category, topic_tags, region, interest_score, is_duplicate, selected, why_selected
3. {categories}
4. 同一事件的不同报道，display_title 应尽量保持一致，便于后续去重
5. is_duplicate=true 的条目通常 selected=false
6. display_title 是给用户看的标题，英文标题翻成自然中文
7. interest_score 范围 0-10

## 标注规范（来自 tagging_guide.md）

{tagging_guide}

## 标注示例（来自 tagging_examples）

{tagging_examples}`

const UserPrompt = `请标注以下 {total_count} 条新闻，输出 JSON 数组：

{news_items}

要求：
1. 严格按照 JSON 数组格式输出，不要输出其他内容
2. 每个元素必须包含所有必填字段
3. 同一事件的不同报道，display_title 应尽量保持一致`

const DefaultTaggingGuide = `## 标注规范 v3

### 重复项判定
- 主项：is_duplicate=false, selected=true
- 重复项：is_duplicate=true, selected=false
- 同一事件的不同报道，display_title 应尽量保持一致
- 主项优先：中文标题更清晰、信息更完整、来源更稳

### display_title 规则
- 最终给用户看的标题
- 英文标题翻成自然中文
- 可以润色但不改事实
- 同一事件的不同报道，display_title 应尽量保持一致

### selected 规则
- selected=true：interest_score 较高、事件独特、对用户兴趣相关
- selected=false：重复报道、弱相关、信息价值低`

const DefaultTaggingExamples = `title,display_title,category,topic_tags,region,interest_score,is_duplicate,selected,why_selected
伊朗袭击迪拜附近油轮，地区战事持续升级,伊朗袭击迪拜附近油轮，地区战事持续升级,战争与地缘,"伊朗,迪拜,油轮,中东",中东,9,false,true,中东冲突升级且与能源运输相关
Kuwaiti Tanker Full of Oil Struck Off Dubai,特朗普发出威胁次日满载原油的科威特油轮在迪拜附近遭袭,战争与地缘,"迪拜,油轮,中东,能源",中东,8,true,false,与主事件重复报道
SpaceX launches next-generation Starlink satellites,SpaceX 发射新一代 Starlink 卫星,航空航天,"SpaceX,卫星,火箭,航天",北美,10,false,true,高度符合用户航天兴趣
Japan deploys long-range missiles,日本在熊本和静冈部署远程导弹,军事装备,"日本,导弹,部署,防务",东亚,9,false,true,典型军事部署新闻
Nvidia unveils new AI chip architecture,英伟达发布新一代 AI 芯片架构,AI与数码,"AI,芯片,Nvidia,半导体",北美,10,false,true,高度符合 AI/数码兴趣`
