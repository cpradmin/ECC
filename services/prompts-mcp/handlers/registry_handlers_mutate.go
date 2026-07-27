package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// RebuildRegistryEndpoint triggers a registry rebuild
// POST /mcp/prompts/registry/rebuild
func (rh *RegistryHandler) RebuildRegistryEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check context before starting long operation
	ctx := r.Context()
	select {
	case <-ctx.Done():
		http.Error(w, "Request cancelled", http.StatusRequestTimeout)
		return
	default:
	}

	start := time.Now()
	err := rh.RebuildIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error rebuilding registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Check context again before responding
	select {
	case <-ctx.Done():
		http.Error(w, "Request cancelled before response", http.StatusRequestTimeout)
		return
	default:
	}

	elapsed := time.Since(start).Milliseconds()

	// Hold read lock while reading from index (I9 fix)
	rh.mu.RLock()
	totalPrompts := rh.index.TotalPrompts
	rh.mu.RUnlock()

	WriteJSONOK(w, map[string]any{
		"status":        "rebuilt",
		"total_prompts": totalPrompts,
		"elapsed_ms":    elapsed,
		"timestamp":     time.Now().UTC(),
	})
}

// PromotePrompt promotes a prompt (≥0.8 confidence) to registry
// POST /mcp/prompts/registry/promote
//
// I11 FIX: previously held the write lock across entire promote sequence,
// blocking all readers while loading/saving disk. Now:
//  1. Acquire write lock, get entry + snapshot fields, release lock
//  2. Load and save prompt OUTSIDE lock (disk I/O)
//  3. Re-acquire write lock, rebuild index, release lock
// This allows concurrent reads while disk operations happen.
func (rh *RegistryHandler) PromotePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PromptID     string `json:"prompt_id"`
		Version      string `json:"version"`
		ReleaseNotes string `json:"release_notes"`
	}

	// B3: bounded read; oversize bodies get 413, malformed JSON gets 400.
	if !DecodeJSONBody(w, r, &req) {
		return
	}

	if req.PromptID == "" {
		http.Error(w, "Missing required field: prompt_id", http.StatusBadRequest)
		return
	}

	// I11 FIX: Step 1 — Hold write lock only for index query
	var entryDomain string
	var entryVersion string
	var entryConfidence float32

	rh.mu.Lock()
	// DEADLOCK FIX: this used to call rh.GetIndex(), which takes rh.mu.RLock().
	// sync.RWMutex is not reentrant, so the write lock above made every call to
	// this endpoint hang forever. Use the lock-aware variant instead.
	index, err := rh.getIndexLocked()
	if err != nil {
		rh.mu.Unlock()
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	entry := rh.builder.GetEntryByID(index, req.PromptID)
	if entry == nil {
		rh.mu.Unlock()
		http.Error(w, fmt.Sprintf("Prompt not found in registry: %s", req.PromptID), http.StatusNotFound)
		return
	}

	// Snapshot the fields we report after the rebuild invalidates `entry`.
	entryDomain = entry.Domain
	entryVersion = entry.CurrentVersion
	entryConfidence = entry.Confidence

	// Check confidence threshold while holding lock
	if entryConfidence < 0.8 {
		rh.mu.Unlock()
		http.Error(w, fmt.Sprintf("Prompt confidence (%.2f) is below 0.8 threshold for promotion", entryConfidence), http.StatusForbidden)
		return
	}

	rh.mu.Unlock()
	// I11 FIX: lock released — disk I/O now happens concurrently with reads

	// Step 2 — Load and save OUTSIDE lock (no contention on readers)
	prompt, err := rh.loader.LoadByID(req.PromptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error loading prompt: %v", err), http.StatusInternalServerError)
		return
	}

	// Mark as promoted and save
	prompt.Promoted = true
	prompt.UpdatedAt = time.Now().UTC()
	if err := rh.loader.SavePrompt(prompt, prompt.Scope); err != nil {
		if errors.Is(err, ErrInvalidPathSegment) {
			http.Error(w, fmt.Sprintf("Invalid prompt: %v", err), http.StatusBadRequest)
			return
		}
		http.Error(w, fmt.Sprintf("Error saving promoted prompt: %v", err), http.StatusInternalServerError)
		return
	}

	// Step 2.5 — Load all prompts OUTSIDE lock (most expensive I/O)
	// This mirrors RebuildIndex pattern: disk work happens concurrently with readers
	promptsToRebuild, err := rh.loader.LoadAll()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error loading prompts for rebuild: %v", err), http.StatusInternalServerError)
		return
	}

	// Step 3 — Re-acquire write lock ONLY for index rebuild + notify
	rh.mu.Lock()
	defer rh.mu.Unlock()

	// I6 FIX (TOCTOU): re-validate confidence; feedback may have dropped it during step 2
	currentIndex, err := rh.getIndexLocked()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error verifying registry: %v", err), http.StatusInternalServerError)
		return
	}
	currentEntry := rh.builder.GetEntryByID(currentIndex, req.PromptID)
	if currentEntry != nil && currentEntry.Confidence < 0.8 {
		http.Error(w, fmt.Sprintf("Prompt confidence (%.2f) dropped below 0.8 threshold during processing", currentEntry.Confidence), http.StatusForbidden)
		return
	}

	// Rebuild index to persist registry changes
	if err := rh.rebuildIndexLocked(promptsToRebuild); err != nil {
		http.Error(w, fmt.Sprintf("Error updating registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Unlock before notifying (callbacks are foreign code per registry_index.go:24-25)
	rh.mu.Unlock()
	rh.notifyChange()
	rh.mu.Lock() // re-acquire for defer unlock

	WriteJSONOK(w, map[string]any{
		"status":        "promoted",
		"prompt_id":     req.PromptID,
		"version":       entryVersion,
		"confidence":    entryConfidence,
		"registry_url":  fmt.Sprintf("/mcp/prompts/registry/%s/%s", entryDomain, entryVersion),
		"release_notes": req.ReleaseNotes,
		"timestamp":     time.Now().UTC(),
	})
}
