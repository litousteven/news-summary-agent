package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupExpiredFiles_RemovesOld(t *testing.T) {
	tmpDir := t.TempDir()

	// Create "old" files by backdating modification time
	oldFiles := []string{
		"push_history_20260427.jsonl",
		"tagged_cache_20260427.jsonl",
		"digest_20260427_120000.md",
	}
	cutoff := time.Now().AddDate(0, 0, -3) // 3 days ago
	for _, name := range oldFiles {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte("old data"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, cutoff, cutoff); err != nil {
			t.Fatal(err)
		}
	}

	// Create "recent" files that should NOT be deleted
	recentFiles := []string{
		"push_history_20260429.jsonl",
		"tagged_cache_20260429.jsonl",
		"digest_20260429_120000.md",
	}
	for _, name := range recentFiles {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte("recent data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	p := &NewsPipeline{
		DataDir: tmpDir,
		Config:  PipelineConfig{FileExpiryDays: 2},
	}
	p.CleanupExpiredFiles()

	// Old files should be deleted
	for _, name := range oldFiles {
		if _, err := os.Stat(filepath.Join(tmpDir, name)); !os.IsNotExist(err) {
			t.Errorf("old file %s should have been deleted", name)
		}
	}

	// Recent files should still exist
	for _, name := range recentFiles {
		if _, err := os.Stat(filepath.Join(tmpDir, name)); os.IsNotExist(err) {
			t.Errorf("recent file %s should NOT have been deleted", name)
		}
	}
}

func TestCleanupExpiredFiles_DefaultExpiry(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an old file (3 days old)
	path := filepath.Join(tmpDir, "push_history_20260426.jsonl")
	if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().AddDate(0, 0, -3)
	if err := os.Chtimes(path, cutoff, cutoff); err != nil {
		t.Fatal(err)
	}

	// Default config (FileExpiryDays=0 → uses DefaultFileExpiryDays=2)
	p := &NewsPipeline{
		DataDir: tmpDir,
		Config:  PipelineConfig{},
	}
	p.CleanupExpiredFiles()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("old file should be deleted with default expiry of 2 days")
	}
}

func TestCleanupExpiredFiles_NoMatchPattern(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file that doesn't match any cleanup pattern
	path := filepath.Join(tmpDir, "important_config.yaml")
	oldTime := time.Now().AddDate(0, 0, -10)
	if err := os.WriteFile(path, []byte("config"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	p := &NewsPipeline{
		DataDir: tmpDir,
		Config:  PipelineConfig{FileExpiryDays: 2},
	}
	p.CleanupExpiredFiles()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("non-matching file should NOT be deleted")
	}
}
