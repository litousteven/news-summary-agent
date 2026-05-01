package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// EmbeddingCache provides per-day persistent caching for embedding vectors.
type EmbeddingCache struct {
	dataDir string
	cache   map[string][]float64 // hash -> vector
	dirty   bool
}

func NewEmbeddingCache(dataDir string) *EmbeddingCache {
	return &EmbeddingCache{
		dataDir: dataDir,
		cache:   make(map[string][]float64),
	}
}

func (ec *EmbeddingCache) Load() {
	today := time.Now().Format("20060102")
	path := ec.dataDir + "/embedding_cache_" + today + ".jsonl"
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry struct {
			Hash string    `json:"hash"`
			Vec  []float64 `json:"vec"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			ec.cache[entry.Hash] = entry.Vec
		}
	}
	if len(ec.cache) > 0 {
		log.Printf("[EmbeddingCache] 加载 %d 条缓存", len(ec.cache))
	}
}

func (ec *EmbeddingCache) Save() {
	if !ec.dirty {
		return
	}
	today := time.Now().Format("20060102")
	path := ec.dataDir + "/embedding_cache_" + today + ".jsonl"
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("[EmbeddingCache] 写入失败: %v", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for hash, vec := range ec.cache {
		if err := enc.Encode(map[string]any{
			"hash": hash,
			"vec":  vec,
		}); err != nil {
			log.Printf("[EmbeddingCache] 编码失败: %v", err)
		}
	}
	log.Printf("[EmbeddingCache] 保存 %d 条缓存", len(ec.cache))
}

func (ec *EmbeddingCache) Get(key string) ([]float64, bool) {
	vec, ok := ec.cache[key]
	return vec, ok
}

func (ec *EmbeddingCache) Set(key string, vec []float64) {
	ec.cache[key] = vec
	ec.dirty = true
}

func embedHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h[:8])
}

// CachedEmbedStrings wraps the Embedding model with per-day persistent caching.
// For each text, it checks the cache first and only calls the model for cache misses.
func CachedEmbedStrings(ctx context.Context, p *NewsPipeline, texts []string) ([][]float64, error) {
	if p.Embedding == nil {
		return nil, nil
	}
	hashes := make([]string, len(texts))
	for i, t := range texts {
		hashes[i] = embedHash(t)
	}

	// Collect cache hits and misses
	results := make([][]float64, len(texts))
	var missTexts []string
	var missIdx []int
	for i, hash := range hashes {
		if vec, ok := p.EmbedCache.Get(hash); ok {
			results[i] = vec
		} else {
			missTexts = append(missTexts, texts[i])
			missIdx = append(missIdx, i)
		}
	}

	// Embed only the misses
	if len(missTexts) > 0 {
		vecs, err := p.Embedding.EmbedStrings(ctx, missTexts)
		if err != nil || len(vecs) != len(missTexts) {
			if len(missIdx) == len(texts) {
				return nil, err // all were misses, return error
			}
			// partial: fill misses with nil, return what we have
			return results, nil
		}
		for j, idx := range missIdx {
			results[idx] = vecs[j]
			p.EmbedCache.Set(hashes[idx], vecs[j])
		}
	}

	return results, nil
}
