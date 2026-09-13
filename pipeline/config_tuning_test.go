package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// repoConfigDir 返回仓库内的 config/ 目录。
// `go test ./pipeline` 的工作目录是 pipeline/，所以用 ".."。
func repoConfigDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "config")
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Skipf("找不到仓库 config 目录（%s），跳过: %v", dir, err)
	}
	return dir
}

// 防止 config.yaml 的 key 拼错导致调优静默失效：
// LoadConfig 解析失败或字段名不匹配都会悄悄留下一堆零值，再由 getter 退回默认值。
func TestRepoConfigTuningValuesAreLoaded(t *testing.T) {
	cfg := LoadConfig(repoConfigDir(t))

	if cfg.TagMaxConcurrentBatches != 1 {
		t.Errorf("tag_max_concurrent_batches 应为 1，实际 %d", cfg.TagMaxConcurrentBatches)
	}
	if cfg.TagMaxRetries != 3 {
		t.Errorf("tag_max_retries 应为 3，实际 %d", cfg.TagMaxRetries)
	}
	if cfg.TagBatchTimeoutSeconds != 300 {
		t.Errorf("tag_batch_timeout_seconds 应为 300，实际 %d", cfg.TagBatchTimeoutSeconds)
	}

	// getter 必须真的用上配置值，而不是因为 <=0 退回默认值
	p := &NewsPipeline{Config: cfg}
	if got := p.GetTagMaxConcurrentBatches(); got != 1 {
		t.Errorf("GetTagMaxConcurrentBatches() = %d，期望 1（可能退回了默认值）", got)
	}
	if got := p.GetTagMaxRetries(); got != 3 {
		t.Errorf("GetTagMaxRetries() = %d，期望 3（可能退回了默认值）", got)
	}
	if got := p.GetTagBatchTimeoutSeconds(); got != 300 {
		t.Errorf("GetTagBatchTimeoutSeconds() = %d，期望 300（可能退回了默认值）", got)
	}
}
