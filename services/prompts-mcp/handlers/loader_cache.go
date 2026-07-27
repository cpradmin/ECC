package handlers

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// promptCache is the TTL cache guarding LoadAll.
type promptCache struct {
	mu        sync.RWMutex
	prompts   []models.Prompt
	loadedAt  time.Time
	valid     bool
	ttl       time.Duration
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

// CacheStats reports cache effectiveness.
type CacheStats struct {
	Hits      int64   `json:"hits"`
	Misses    int64   `json:"misses"`
	Evictions int64   `json:"evictions"`
	HitRate   float64 `json:"hit_rate"`
	Entries   int     `json:"entries"`
	AgeMs     int64   `json:"age_ms"`
}

// SetCacheTTL overrides the cache lifetime (0 disables caching). Intended for
// tests and for operators who need stricter freshness.
func (pl *PromptLoader) SetCacheTTL(ttl time.Duration) {
	pl.cache.mu.Lock()
	pl.cache.ttl = ttl
	pl.cache.valid = false
	pl.cache.mu.Unlock()
}

// InvalidateCache drops the cached prompt set. Called after any write so a
// reader never observes its own stale write.
func (pl *PromptLoader) InvalidateCache() {
	pl.cache.mu.Lock()
	if pl.cache.valid {
		pl.cache.evictions.Add(1)
	}
	pl.cache.valid = false
	pl.cache.prompts = nil
	pl.cache.mu.Unlock()
}

// CacheStats returns hit/miss counters for the LoadAll cache.
func (pl *PromptLoader) CacheStats() CacheStats {
	pl.cache.mu.RLock()
	entries := len(pl.cache.prompts)
	var ageMs int64
	if pl.cache.valid {
		ageMs = time.Since(pl.cache.loadedAt).Milliseconds()
	}
	pl.cache.mu.RUnlock()

	hits := pl.cache.hits.Load()
	misses := pl.cache.misses.Load()
	rate := 0.0
	if total := hits + misses; total > 0 {
		rate = float64(hits) / float64(total)
	}
	return CacheStats{
		Hits:      hits,
		Misses:    misses,
		Evictions: pl.cache.evictions.Load(),
		HitRate:   rate,
		Entries:   entries,
		AgeMs:     ageMs,
	}
}

// cachedPrompts returns a copy of the cache entry if it is present and fresh.
func (pl *PromptLoader) cachedPrompts() ([]models.Prompt, bool) {
	pl.cache.mu.RLock()
	defer pl.cache.mu.RUnlock()

	if !pl.cache.valid || pl.cache.ttl <= 0 {
		return nil, false
	}
	if time.Since(pl.cache.loadedAt) > pl.cache.ttl {
		return nil, false
	}
	return copyPrompts(pl.cache.prompts), true
}

// copyPrompts returns a shallow copy of the slice so cache entries stay immutable.
func copyPrompts(src []models.Prompt) []models.Prompt {
	if src == nil {
		return nil
	}
	out := make([]models.Prompt, len(src))
	copy(out, src)
	return out
}
