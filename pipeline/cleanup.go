package pipeline

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// CleanupExpiredFiles removes files in data/ and log/ older than the configured expiry days.
// Only cleans known runtime file patterns: push_history_*.jsonl, tagged_cache_*.jsonl, embedding_cache_*.jsonl, digest_*.md, pipeline_*.log.
// Errors are logged but not fatal — cleanup is best-effort.
func (p *NewsPipeline) CleanupExpiredFiles() {
	expiryDays := p.GetFileExpiryDays()
	if expiryDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -expiryDays)

	patterns := []string{
		"push_history_*.jsonl",
		"tagged_cache_*.jsonl",
		"embedding_cache_*.jsonl",
		"digest_*.md",
	}

	var totalRemoved int
	var totalBytes int64

	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(p.DataDir, pattern))
		if err != nil {
			log.Printf("[Cleanup] glob %s 失败: %v", pattern, err)
			continue
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				log.Printf("[Cleanup] stat %s 失败: %v", path, err)
				continue
			}
			if info.ModTime().Before(cutoff) {
				if err := os.Remove(path); err != nil {
					log.Printf("[Cleanup] 删除 %s 失败: %v", path, err)
				} else {
					log.Printf("[Cleanup] 删除过期文件: %s (修改时间: %s, 大小: %d bytes)",
						filepath.Base(path), info.ModTime().Format("2006-01-02 15:04:05"), info.Size())
					totalRemoved++
					totalBytes += info.Size()
				}
			}
		}
	}

	if totalRemoved > 0 {
		log.Printf("[Cleanup] 共清理 %d 个过期文件 (>= %d 天), 释放 %d bytes", totalRemoved, expiryDays, totalBytes)
	} else {
		log.Printf("[Cleanup] 无过期文件需要清理 (过期阈值: %d 天)", expiryDays)
	}

	// Clean log files (log/pipeline_*.log) — use same expiryDays
	logDir := filepath.Join(filepath.Dir(p.DataDir), "log")
	logPattern := filepath.Join(logDir, "pipeline_*.log")
	logMatches, err := filepath.Glob(logPattern)
	if err != nil {
		log.Printf("[Cleanup] glob log 失败: %v", err)
		return
	}
	var logRemoved int
	for _, path := range logMatches {
		info, err := os.Stat(path)
		if err != nil {
			log.Printf("[Cleanup] stat log %s 失败: %v", path, err)
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				log.Printf("[Cleanup] 删除 log %s 失败: %v", path, err)
			} else {
				log.Printf("[Cleanup] 删除过期日志: %s (修改时间: %s, 大小: %d bytes)",
					filepath.Base(path), info.ModTime().Format("2006-01-02 15:04:05"), info.Size())
				logRemoved++
			}
		}
	}
	if logRemoved > 0 {
		log.Printf("[Cleanup] 共清理 %d 个过期日志文件", logRemoved)
	}
}
