package handlers

import (
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// Tests for PromptLoader cache operations

func TestSetCacheTTL(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())

	newTTL := 30 * time.Second
	loader.SetCacheTTL(newTTL)

	loader.cache.mu.RLock()
	if loader.cache.ttl != newTTL {
		t.Errorf("expected TTL %v, got %v", newTTL, loader.cache.ttl)
	}
	if loader.cache.valid {
		t.Error("expected cache to be invalidated after SetCacheTTL")
	}
	loader.cache.mu.RUnlock()
}

func TestSetCacheTTLZeroDisables(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())
	loader.SetCacheTTL(0)

	loader.cache.mu.RLock()
	if loader.cache.ttl != 0 {
		t.Errorf("expected TTL 0, got %v", loader.cache.ttl)
	}
	loader.cache.mu.RUnlock()
}

func TestInvalidateCache(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())

	// Mark cache as valid
	loader.cache.mu.Lock()
	loader.cache.valid = true
	loader.cache.prompts = []models.Prompt{{ID: "test"}}
	loader.cache.mu.Unlock()

	before := loader.cache.evictions.Load()
	loader.InvalidateCache()
	after := loader.cache.evictions.Load()

	if after != before+1 {
		t.Errorf("expected evictions to increment, before=%d after=%d", before, after)
	}

	loader.cache.mu.RLock()
	if loader.cache.valid {
		t.Error("expected cache to be invalid after InvalidateCache")
	}
	if len(loader.cache.prompts) != 0 {
		t.Error("expected cache prompts to be cleared")
	}
	loader.cache.mu.RUnlock()
}

func TestInvalidateCacheAlreadyInvalid(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())

	// Cache is already invalid by default
	before := loader.cache.evictions.Load()
	loader.InvalidateCache()
	after := loader.cache.evictions.Load()

	// Evictions counter should NOT increment for already-invalid cache
	if after != before {
		t.Errorf("expected no eviction for already-invalid cache, before=%d after=%d", before, after)
	}
}

func TestCacheStatsEmpty(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())

	stats := loader.CacheStats()

	if stats.Hits != 0 {
		t.Errorf("expected 0 hits, got %d", stats.Hits)
	}
	if stats.Misses != 0 {
		t.Errorf("expected 0 misses, got %d", stats.Misses)
	}
	if stats.Evictions != 0 {
		t.Errorf("expected 0 evictions, got %d", stats.Evictions)
	}
	if stats.HitRate != 0 {
		t.Errorf("expected 0 hit rate, got %f", stats.HitRate)
	}
}

func TestCacheStatsWithData(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())

	// Manually set stats
	loader.cache.hits.Store(100)
	loader.cache.misses.Store(50)
	loader.cache.evictions.Store(10)

	stats := loader.CacheStats()

	if stats.Hits != 100 {
		t.Errorf("expected 100 hits, got %d", stats.Hits)
	}
	if stats.Misses != 50 {
		t.Errorf("expected 50 misses, got %d", stats.Misses)
	}
	if stats.Evictions != 10 {
		t.Errorf("expected 10 evictions, got %d", stats.Evictions)
	}

	expectedHitRate := 100.0 / 150.0
	if stats.HitRate != expectedHitRate {
		t.Errorf("expected hit rate %f, got %f", expectedHitRate, stats.HitRate)
	}
}

func TestCachedPromptsFresh(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())
	loader.SetCacheTTL(10 * time.Second)

	prompts := []models.Prompt{
		{ID: "p1", Domain: "router-prompts"},
		{ID: "p2", Domain: "conversation-prompts"},
	}

	loader.cache.mu.Lock()
	loader.cache.prompts = prompts
	loader.cache.loadedAt = time.Now()
	loader.cache.valid = true
	loader.cache.mu.Unlock()

	cached, ok := loader.cachedPrompts()

	if !ok {
		t.Fatal("expected cache hit for fresh data")
	}
	if len(cached) != 2 {
		t.Errorf("expected 2 cached prompts, got %d", len(cached))
	}
	if cached[0].ID != "p1" {
		t.Errorf("expected first prompt ID p1, got %s", cached[0].ID)
	}
}

func TestCachedPromptsStale(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())
	loader.SetCacheTTL(1 * time.Millisecond) // Very short TTL

	prompts := []models.Prompt{{ID: "p1"}}

	loader.cache.mu.Lock()
	loader.cache.prompts = prompts
	loader.cache.loadedAt = time.Now().Add(-2 * time.Millisecond)
	loader.cache.valid = true
	loader.cache.mu.Unlock()

	time.Sleep(2 * time.Millisecond) // Ensure TTL expires

	cached, ok := loader.cachedPrompts()

	if ok {
		t.Fatal("expected cache miss for stale data")
	}
	if cached != nil {
		t.Errorf("expected nil for stale cache, got %v", cached)
	}
}

func TestCachedPromptsDisabled(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())
	loader.SetCacheTTL(0) // Disable caching

	prompts := []models.Prompt{{ID: "p1"}}

	loader.cache.mu.Lock()
	loader.cache.prompts = prompts
	loader.cache.loadedAt = time.Now()
	loader.cache.valid = true
	loader.cache.mu.Unlock()

	_, ok := loader.cachedPrompts()

	if ok {
		t.Fatal("expected cache miss when caching disabled")
	}
}
