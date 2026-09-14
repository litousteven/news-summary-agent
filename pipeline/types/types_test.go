package types

import "testing"

// RenderText 是展示层恢复日期的唯一入口：ItemSummary 由 LLM 生成、不含日期，
// 必须靠 DatePrefix 补回；FactParagraph 已自带 "{region}{date}，" 前缀，不能重复加。
func TestDigestItemRenderText(t *testing.T) {
	cases := []struct {
		name string
		item DigestItem
		want string
	}{
		{
			name: "有 ItemSummary：补日期前缀",
			item: DigestItem{ItemSummary: "A股近期承压下行。", DatePrefix: "7月28日，", FactParagraph: "中国7月28日，量化砸盘成A股最大隐忧？"},
			want: "7月28日，A股近期承压下行。",
		},
		{
			name: "无 ItemSummary：回退 FactParagraph，不重复加前缀",
			item: DigestItem{DatePrefix: "7月28日，", FactParagraph: "中国7月28日，量化砸盘成A股最大隐忧？"},
			want: "中国7月28日，量化砸盘成A股最大隐忧？",
		},
		{
			name: "日期不可解析：DatePrefix 为空，退化为纯摘要",
			item: DigestItem{ItemSummary: "某条新闻。", DatePrefix: ""},
			want: "某条新闻。",
		},
		{
			// 生产实例：18:24 那期的「芬兰加入前沿威慑核计划」曾渲染成
			// "9月14日，9月14日，芬兰与法国…"——LLM 摘要自己带了日期。
			name: "摘要已自带同日日期：不重复拼接",
			item: DigestItem{ItemSummary: "9月14日，芬兰与法国发布联合声明。", DatePrefix: "9月14日，"},
			want: "9月14日，芬兰与法国发布联合声明。",
		},
		{
			name: "摘要自带日期但缺逗号：同样不重复",
			item: DigestItem{ItemSummary: "9月14日芬兰与法国发布联合声明。", DatePrefix: "9月14日，"},
			want: "9月14日芬兰与法国发布联合声明。",
		},
		{
			// 边界：裸日期 "7月2日" 是 "7月28日…" 的子串，不能误判为已带日期。
			name: "前缀是另一个日期的子串：仍要补前缀",
			item: DigestItem{ItemSummary: "7月28日，量化砸盘引发争议。", DatePrefix: "7月2日，"},
			want: "7月2日，7月28日，量化砸盘引发争议。",
		},
		{
			name: "两者都为空：返回空串",
			item: DigestItem{},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.item.RenderText(); got != c.want {
				t.Errorf("RenderText() = %q，期望 %q", got, c.want)
			}
		})
	}
}
