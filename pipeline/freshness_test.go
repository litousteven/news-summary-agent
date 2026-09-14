package pipeline

import (
	"testing"
	"time"

	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

// 固定的“当前时间”，让跨年/时效断言不随真实日期漂移。
func withFrozenNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := currentTime
	currentTime = func() time.Time { return now }
	t.Cleanup(func() { currentTime = prev })
}

func TestParsePublishTime_AcceptedFormats(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"RFC1123 GMT", "Tue, 28 Jul 2026 12:27:18 GMT", time.Date(2026, 7, 28, 12, 27, 18, 0, time.UTC)},
		{"RFC1123Z 数字时区", "Thu, 10 Sep 2026 12:45:54 -0400", time.Date(2026, 9, 10, 12, 45, 54, 0, time.FixedZone("", -4*3600))},
		{"RFC3339", "2026-09-13T06:02:39+00:00", time.Date(2026, 9, 13, 6, 2, 39, 0, time.UTC)},
		{"裸日期兜底", "2026-07-28", time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parsePublishTime(c.in)
			if !ok {
				t.Fatalf("parsePublishTime(%q) 应解析成功", c.in)
			}
			if !got.Equal(c.want) {
				t.Errorf("parsePublishTime(%q) = %v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

func TestParsePublishTime_RejectsUnusable(t *testing.T) {
	for _, in := range []string{"", "   ", "not a date", "昨天"} {
		if _, ok := parsePublishTime(in); ok {
			t.Errorf("parsePublishTime(%q) 应解析失败", in)
		}
	}
}

// 时效判定是这次修复的核心：三种分类必须互斥且稳定。
// 同时验证「过期条目也要返回解析出的时间」，否则完全停更的源无法触发告警。
func TestFreshnessOf(t *testing.T) {
	cutoff := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		pub      string
		want     freshness
		wantTime bool // 是否应返回非零时间
	}{
		{"窗口内（当天）", "Mon, 14 Sep 2026 06:00:00 GMT", freshnessFresh, true},
		{"窗口内（边界当天）", "Fri, 11 Sep 2026 09:00:00 GMT", freshnessFresh, true},
		{"窗口外（早一天）", "Thu, 10 Sep 2026 23:59:59 GMT", freshnessStale, true},
		// 本次事故的原型：停更源返回 47 天前的条目，必须被丢弃，但仍要能报出时间。
		{"窗口外（事故原型，47天前）", "Tue, 28 Jul 2026 12:27:18 GMT", freshnessStale, true},
		{"无日期：未知年龄，放行", "", freshnessUndated, false},
		{"无法解析：未知年龄，放行", "not a date", freshnessUndated, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, parsed := freshnessOf(c.pub, cutoff)
			if got != c.want {
				t.Errorf("freshnessOf(%q) 分类 = %v，期望 %v", c.pub, got, c.want)
			}
			if parsed.IsZero() == c.wantTime {
				t.Errorf("freshnessOf(%q) 返回时间 %v，期望非零=%v", c.pub, parsed, c.wantTime)
			}
		})
	}
}

// 跨年时必须带年份，否则 2025 年的旧闻看起来像今年的。
func TestFormatPublishTime_YearOnlyWhenDifferent(t *testing.T) {
	withFrozenNow(t, time.Date(2026, 9, 14, 6, 4, 0, 0, time.Local))

	if got, want := formatPublishTime("Tue, 28 Jul 2026 12:27:18 GMT"), "7月28日"; got != want {
		t.Errorf("同年应省略年份: got %q, want %q", got, want)
	}
	if got, want := formatPublishTime("Wed, 31 Dec 2025 10:00:00 GMT"), "2025年12月31日"; got != want {
		t.Errorf("跨年应带年份: got %q, want %q", got, want)
	}
	if got := formatPublishTime("看不懂的时间"); got != "" {
		t.Errorf("无法解析应返回空串，got %q", got)
	}
}

// DatePrefix 只带日期、不带 Region：LLM 摘要通常已经以地区开头，重复会很啰嗦。
func TestItemDatePrefix_DateOnly(t *testing.T) {
	withFrozenNow(t, time.Date(2026, 9, 14, 6, 4, 0, 0, time.Local))

	item := types.MergedNewsItem{
		TaggedNewsItem: types.TaggedNewsItem{
			RawNewsItem: types.RawNewsItem{PublishedAt: "Tue, 28 Jul 2026 12:27:18 GMT"},
			Region:      "中国",
		},
	}
	if got, want := itemDatePrefix(item), "7月28日，"; got != want {
		t.Errorf("itemDatePrefix = %q，期望 %q（不应包含 Region）", got, want)
	}

	noDate := types.MergedNewsItem{}
	if got := itemDatePrefix(noDate); got != "" {
		t.Errorf("无发布时间应返回空串，got %q", got)
	}
}

// buildFactParagraph 保留 Region+日期前缀（历史行为不变），供下游 prompt 与历史记录使用。
func TestBuildFactParagraph_KeepsRegionAndDate(t *testing.T) {
	withFrozenNow(t, time.Date(2026, 9, 14, 6, 4, 0, 0, time.Local))

	item := types.MergedNewsItem{
		TaggedNewsItem: types.TaggedNewsItem{
			RawNewsItem:   types.RawNewsItem{PublishedAt: "Tue, 28 Jul 2026 12:27:18 GMT", Title: "原始标题"},
			DisplayTitle:  "量化砸盘成A股最大隐忧？",
			Region:        "中国",
			Category:      "经济与交通",
			InterestScore: 8,
		},
	}
	if got, want := buildFactParagraph(item), "中国7月28日，量化砸盘成A股最大隐忧？"; got != want {
		t.Errorf("buildFactParagraph = %q，期望 %q", got, want)
	}
}
