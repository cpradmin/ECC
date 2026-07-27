package handlers

import (
	"sync"

	"github.com/cpradmin/prompts-mcp/models"
)

// RegistryHandler handles registry API endpoints
type RegistryHandler struct {
	loader  *PromptLoader
	builder *models.RegistryBuilder
	index   *models.RegistryIndex
	// mu guards index. Held for: cache publish/copy, index rebuild (with rebuildIndexLocked),
	// and promotion validation. Disk I/O (LoadAll, SavePrompt) happens outside lock where safe.
	mu sync.RWMutex
	// loadMu serializes the expensive cold-start path (disk read / full rebuild)
	// so N concurrent cache misses perform one load, not N. It is always
	// acquired BEFORE mu; never the reverse.
	loadMu sync.Mutex
	// onChange callbacks fire after a rebuild publishes a new index. Set once
	// at construction, so no lock is needed to read them.
	onChange []func()
}

