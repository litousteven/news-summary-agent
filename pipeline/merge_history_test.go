package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	types "github.com/litousteven/news-summary-agent/pipeline/types"
)

// 回归：去重窗口必须覆盖整个新闻时效窗口。
//
// 事故原型：同一篇 7月28日的文章在 9/12 推送、9/13 被去重拦住（窗口含 9/12）、
// 9/14 又推送一次（窗口只剩 9/14+9/13，9/12 已滚出）——旧闻因此每两天复活一次。
func TestLoadPushHistory_WindowCoversFreshnessWindow(t *testing.T) {
	dir := t.TempDir()
	p := &NewsPipeline{DataDir: dir, Config: PipelineConfig{MaxNewsAgeDays: 3}}

	now := time.Now()
	writeDay := func(offsetDays int, link string) {
		path := filepath.Join(dir, "push_history_"+now.AddDate(0, 0, offsetDays).Format("20060102")+".jsonl")
		line, err := json.Marshal(types.PushHistoryRecord{Link: link, DisplayTitle: link})
		if err != nil {
			t.Fatalf("序列化记录失败: %v", err)
		}
		if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", path, err)
		}
	}

	writeDay(0, "today")
	writeDay(-2, "two-days-ago")   // 事故原型所在的那一天
	writeDay(-3, "three-days-ago") // 窗口最后一天（maxAge+1=4 天，即 -3 仍在窗口内）
	writeDay(-5, "five-days-ago")  // 窗口外，不应加载

	records, err := p.loadPushHistory()
	if err != nil {
		t.Fatalf("loadPushHistory 返回错误: %v", err)
	}

	got := make(map[string]bool)
	for _, r := range records {
		got[r.Link] = true
	}

	for _, want := range []string{"today", "two-days-ago", "three-days-ago"} {
		if !got[want] {
			t.Errorf("窗口内应加载 %q，实际已加载 %v", want, keys(got))
		}
	}
	if got["five-days-ago"] {
		t.Errorf("窗口外不应加载 %q（窗口=%d 天）", "five-days-ago", p.GetHistoryWindowDays())
	}
}

// 窗口宽度必须严格大于时效窗口，否则「还够新、能被选中」的条目会掉出历史。
func TestGetHistoryWindowDays_ExceedsFreshnessWindow(t *testing.T) {
	for _, maxAge := range []int{1, 3, 7} {
		p := &NewsPipeline{Config: PipelineConfig{MaxNewsAgeDays: maxAge}}
		if got, want := p.GetHistoryWindowDays(), maxAge+1; got != want {
			t.Errorf("MaxNewsAgeDays=%d 时窗口 = %d，期望 %d", maxAge, got, want)
		}
		if p.GetHistoryWindowDays() <= p.GetMaxNewsAgeDays() {
			t.Errorf("MaxNewsAgeDays=%d 时窗口 %d 必须大于时效窗口", maxAge, p.GetHistoryWindowDays())
		}
	}
}

// 未配置时回落到默认值，避免 max_news_age_days 缺失导致窗口退化成 0 天。
func TestGetMaxNewsAgeDays_Default(t *testing.T) {
	p := &NewsPipeline{}
	if got, want := p.GetMaxNewsAgeDays(), types.DefaultMaxNewsAgeDays; got != want {
		t.Errorf("GetMaxNewsAgeDays() = %d，期望默认值 %d", got, want)
	}
	if got := p.GetHistoryWindowDays(); got <= 0 {
		t.Errorf("默认窗口应大于 0，实际 %d", got)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
