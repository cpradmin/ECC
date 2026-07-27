package handlers

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// DefaultLoadTimeout bounds every filesystem traversal and every write-lock
// acquisition. (B3 fix)
//
// Without it a degraded mount (NFS stall, dying disk, fuse daemon hang) turns
// every list/get/scrape into an indefinitely blocked goroutine; with a 15s
// server WriteTimeout upstream, those goroutines pile up faster than they
// drain and the process wedges. 5s is comfortably above the p99 for a corpus
// of this size (sub-millisecond) while still firing long before the HTTP
// layer gives up.
const DefaultLoadTimeout = 5 * time.Second

// DefaultCacheTTL is how long a LoadAll() result stays valid.
//
// PERF FIX: LoadAll walks 12 directories and parses every YAML file on disk.
// It was called on the hot path of every list/search/get/metrics request —
// GetPrompt(id) alone re-parsed the entire corpus to find one record.
//
// Invalidation strategy is TTL + explicit bust on write:
//   - writes that go through SavePrompt call InvalidateCache() synchronously, so
//     a caller never reads back its own stale write (read-your-writes);
//   - the 60s TTL bounds staleness from out-of-band edits (the daily pipeline
//     and manual YAML edits write files directly, without touching this
//     process), which is the only source of drift left.
//
// A filesystem watcher would be tighter, but it adds an fsnotify dependency and
// a goroutine per directory for a corpus that changes a few times per day.
const DefaultCacheTTL = 60 * time.Second

// LoaderConfig holds configuration for loading prompts
type LoaderConfig struct {
	DataHome string
}

// PromptLoader handles loading prompts from the filesystem
type PromptLoader struct {
	dataHome string
	baseDir  string
	cache    *promptCache

	// ioDelay injects an artificial per-file delay (nanoseconds) so tests can
	// simulate a slow/hung filesystem and prove the B3 timeouts fire. Always
	// zero in production; atomic so the race detector stays quiet.
	ioDelay atomic.Int64
}

// setIODelay simulates slow file I/O. Test-only.
func (pl *PromptLoader) setIODelay(d time.Duration) { pl.ioDelay.Store(int64(d)) }

// NewPromptLoader creates a new PromptLoader
func NewPromptLoader(dataHome string) *PromptLoader {
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local/share")
	}
	return &PromptLoader{
		dataHome: dataHome,
		baseDir:  filepath.Join(dataHome, "ecc-prompts", "instincts"),
		cache:    &promptCache{ttl: DefaultCacheTTL},
	}
}
