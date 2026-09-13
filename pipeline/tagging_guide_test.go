package pipeline

import (
	"strings"
	"testing"

	tagpkg "github.com/litousteven/news-summary-agent/pipeline/tag"
)

const sportsRuleMarker = "特别规则：体育与文娱新闻"

// 验证 config/tagging_guide.md 真的被读进来、并且真的进了标注 prompt。
// 之前这条规则只存在于文件里，没人验证它会不会被注入。
func TestTaggingGuideSportsRuleReachesTagPrompt(t *testing.T) {
	p := &NewsPipeline{ConfigDir: repoConfigDir(t)}

	guide, err := p.loadTaggingGuide()
	if err != nil {
		t.Fatalf("loadTaggingGuide: %v", err)
	}
	if guide == tagpkg.DefaultTaggingGuide {
		t.Fatal("loadTaggingGuide 回退到了内置默认值，说明 config/tagging_guide.md 没被读到")
	}
	if !strings.Contains(guide, sportsRuleMarker) {
		t.Fatalf("tagging_guide.md 里缺少体育文娱规则 %q", sportsRuleMarker)
	}

	vars := tagpkg.FormatBatchTagPromptVars(nil, "categories", guide, "examples")
	injected, _ := vars["tagging_guide"].(string)
	if !strings.Contains(injected, sportsRuleMarker) {
		t.Fatal("FormatBatchTagPromptVars 未把 guide 注入 tagging_guide 变量")
	}

	// 最终 prompt 文本里必须能看到这条规则
	rendered := strings.ReplaceAll(tagpkg.SystemPrompt, "{tagging_guide}", injected)
	if !strings.Contains(rendered, sportsRuleMarker) {
		t.Errorf("SystemPrompt 渲染后仍不含体育文娱规则\n--- prompt ---\n%s", rendered)
	}
}
