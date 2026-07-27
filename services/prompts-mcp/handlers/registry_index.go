package handlers

import (
	"fmt"

	"github.com/cpradmin/prompts-mcp/models"
)

// RebuildIndex rebuilds the registry index from current prompts
// Called after import or manually via endpoint
// Acquires write lock internally
func (rh *RegistryHandler) RebuildIndex() error {
	// Load all prompts
	prompts, err := rh.loader.LoadAll()
	if err != nil {
		return fmt.Errorf("error loading prompts: %w", err)
	}

	// Acquire write lock and rebuild
	rh.mu.Lock()
	err = rh.rebuildIndexLocked(prompts)
	rh.mu.Unlock()

	// Notify outside the lock: callbacks are foreign code and must never be
	// able to deadlock the registry by re-entering it.
	if err == nil {
		rh.notifyChange()
	}
	return err
}

// rebuildIndexLocked builds and saves the registry index
// Caller must hold rh.mu write lock
func (rh *RegistryHandler) rebuildIndexLocked(prompts []models.Prompt) error {
	// Build index (include prompts with 0.7+ confidence to be inclusive)
	index, err := rh.builder.BuildIndex(prompts, 0.7)
	if err != nil {
		return fmt.Errorf("error building index: %w", err)
	}

	// Save index to disk
	if err := rh.builder.SaveIndex(index); err != nil {
		return fmt.Errorf("error saving index: %w", err)
	}

	// Cache in memory (caller holds write lock)
	rh.index = index
	return nil
}

// cachedCopy returns a deep copy of the cached index, or nil if the cache is cold.
func (rh *RegistryHandler) cachedCopy() *models.RegistryIndex {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	if rh.index == nil {
		return nil
	}
	return rh.deepCopyIndex(rh.index)
}

// GetIndex retrieves the current registry index (cached or rebuilt).
//
// B2 FIX: the previous implementation released the read lock, loaded from disk,
// then unconditionally reacquired the write lock and assigned rh.index. Any
// RebuildIndex that completed during the unlocked window was silently clobbered
// by the stale on-disk copy, and N concurrent misses each performed a full load.
//
// The fix is a two-tier double-checked load:
//  1. fast path  — read lock, return a deep copy if warm;
//  2. loadMu     — serialize cold-start so exactly one goroutine touches disk;
//  3. re-check   — the winner may have already published while we waited;
//  4. publish    — write lock, and only assign if the cache is STILL cold, so a
//     concurrent RebuildIndex result always wins over a disk read.
//
// C3 FIX (retained): returns a deep copy to prevent data races on interior pointers.
func (rh *RegistryHandler) GetIndex() (*models.RegistryIndex, error) {
	// 1. Fast path: warm cache.
	if c := rh.cachedCopy(); c != nil {
		return c, nil
	}

	// 2. Only one goroutine performs the cold-start load.
	rh.loadMu.Lock()
	defer rh.loadMu.Unlock()

	// 3. Double-check: another goroutine may have populated the cache while we
	//    were blocked on loadMu.
	if c := rh.cachedCopy(); c != nil {
		return c, nil
	}

	// Disk I/O happens outside every critical section on rh.mu.
	index, err := rh.builder.LoadIndex()
	if err != nil {
		return nil, fmt.Errorf("error loading index: %w", err)
	}

	// If no index exists (or it was rejected as stale/corrupt), build one.
	if index == nil {
		prompts, err := rh.loader.LoadAll()
		if err != nil {
			return nil, fmt.Errorf("error loading prompts: %w", err)
		}
		rh.mu.Lock()
		err = rh.rebuildIndexLocked(prompts)
		if err == nil {
			index = rh.deepCopyIndex(rh.index)
		}
		rh.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("error building initial index: %w", err)
		}
		return index, nil
	}

	// 4. Publish, but never overwrite a fresher in-memory index produced by a
	//    concurrent RebuildIndex.
	rh.mu.Lock()
	if rh.index == nil {
		rh.index = index
	}
	result := rh.deepCopyIndex(rh.index)
	rh.mu.Unlock()
	return result, nil
}

// getIndexLocked returns the live cached index, loading or building it if the
// cache is cold. The caller MUST already hold rh.mu for writing, and must not
// retain the returned pointer past the critical section.
//
// This exists because GetIndex acquires rh.mu itself; calling it from a
// write-locked section self-deadlocks (sync.RWMutex is not reentrant).
func (rh *RegistryHandler) getIndexLocked() (*models.RegistryIndex, error) {
	if rh.index != nil {
		return rh.index, nil
	}

	index, err := rh.builder.LoadIndex()
	if err != nil {
		return nil, fmt.Errorf("error loading index: %w", err)
	}
	if index != nil {
		rh.index = index
		return rh.index, nil
	}

	prompts, err := rh.loader.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("error loading prompts: %w", err)
	}
	if err := rh.rebuildIndexLocked(prompts); err != nil {
		return nil, err
	}
	return rh.index, nil
}
