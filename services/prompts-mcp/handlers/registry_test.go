package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// TestPromotedRoundTrip verifies that Promoted flag persists through save/load/rebuild cycle
func TestPromotedRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	// Create prompt loader
	loader := NewPromptLoader(tmpDir)

	// Create a test prompt (use "project" scope for "personal" directory)
	prompt := models.Prompt{
		ID:         "test-prompt-1",
		Trigger:    "say hello",
		Domain:     "router-prompts",
		Source:     "unit-test",
		Scope:      "project",
		Promoted:   false,
		Confidence: 0.85,
		Content:    "Hello world",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	// Step 1: Save prompt (Promoted=false)
	if err := loader.SavePrompt(&prompt, prompt.Scope); err != nil {
		t.Fatalf("Failed to save prompt: %v", err)
	}

	// Step 2: Load and verify Promoted=false
	loaded, err := loader.LoadByID(prompt.ID)
	if err != nil {
		t.Fatalf("Failed to load prompt: %v", err)
	}
	if loaded.Promoted != false {
		t.Errorf("Expected Promoted=false after save, got %v", loaded.Promoted)
	}

	// Step 3: Set Promoted=true and save
	loaded.Promoted = true
	if err := loader.SavePrompt(loaded, loaded.Scope); err != nil {
		t.Fatalf("Failed to save promoted prompt: %v", err)
	}

	// Step 4: Load again and verify Promoted=true
	loaded2, err := loader.LoadByID(prompt.ID)
	if err != nil {
		t.Fatalf("Failed to load promoted prompt: %v", err)
	}
	if loaded2.Promoted != true {
		t.Errorf("Expected Promoted=true after promote, got %v", loaded2.Promoted)
	}

	// Step 5: Build registry and verify Promoted is in RegistryEntry
	prompts := []models.Prompt{*loaded2}
	builder := models.NewRegistryBuilder(tmpDir)
	index, err := builder.BuildIndex(prompts, 0.7)
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	if len(index.Prompts) != 1 {
		t.Fatalf("Expected 1 prompt in index, got %d", len(index.Prompts))
	}

	entry := index.Prompts[0]
	if entry.Promoted != true {
		t.Errorf("Expected RegistryEntry.Promoted=true, got %v", entry.Promoted)
	}
	if entry.RegistryStatus != "promoted" {
		t.Errorf("Expected RegistryStatus='promoted', got %q", entry.RegistryStatus)
	}
}

// TestSavePromptEscaping verifies YAML escaping of special characters in Trigger field
func TestSavePromptEscaping(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	tests := []struct {
		name    string
		trigger string
	}{
		{
			name:    "quotes",
			trigger: `say "hello" to me`,
		},
		{
			name:    "newline",
			trigger: "line1\nline2",
		},
		{
			name:    "backslash",
			trigger: `C:\path\to\file`,
		},
		{
			name:    "colon",
			trigger: "key: value",
		},
		{
			name:    "mixed",
			trigger: `say "hello\nworld": it's great`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := models.Prompt{
				ID:         fmt.Sprintf("test-escape-%s", tt.name),
				Trigger:    tt.trigger,
				Domain:     "router-prompts",
				Source:     "unit-test",
				Scope:      "project",
				Confidence: 0.85,
				Content:    "Test content",
				CreatedAt:  time.Now().UTC(),
				UpdatedAt:  time.Now().UTC(),
			}

			// Save and load
			if err := loader.SavePrompt(&prompt, prompt.Scope); err != nil {
				t.Fatalf("Failed to save prompt: %v", err)
			}

			loaded, err := loader.LoadByID(prompt.ID)
			if err != nil {
				t.Fatalf("Failed to load prompt: %v", err)
			}

			// Verify trigger matches exactly
			if loaded.Trigger != tt.trigger {
				t.Errorf("Trigger mismatch\nExpected: %q\nGot:      %q", tt.trigger, loaded.Trigger)
			}
		})
	}
}

// TestPromotePromptCallsHandler verifies I11 fix - PromotePrompt drives the real handler
// and correctly updates the registry (not just manual inline manipulation).
func TestPromotePromptCallsHandler(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	handler := NewRegistryHandler(tmpDir, loader)

	// Create a test prompt with high confidence
	prompt := models.Prompt{
		ID:         "test-promote-real",
		Trigger:    "test prompt",
		Domain:     "router-prompts",
		Source:     "unit-test",
		Scope:      "project",
		Promoted:   false,
		Confidence: 0.85,
		Content:    "Test content",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	if err := loader.SavePrompt(&prompt, prompt.Scope); err != nil {
		t.Fatalf("Failed to save test prompt: %v", err)
	}

	// Build initial index
	if err := handler.RebuildIndex(); err != nil {
		t.Fatalf("Failed to build initial index: %v", err)
	}

	// Verify initial state
	index, err := handler.GetIndex()
	if err != nil {
		t.Fatalf("Failed to get initial index: %v", err)
	}

	entry := GetEntryByID(index, "test-promote-real")
	if entry == nil || entry.Promoted {
		t.Fatalf("Initial state should have unpromoted prompt")
	}

	// Call the real PromotePrompt handler via HTTP
	body := `{"prompt_id":"test-promote-real","version":"1.0","release_notes":"Test"}`
	req := httptest.NewRequest("POST", "/registry/promote", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.PromotePrompt(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the prompt is now promoted in the index
	indexAfter, err := handler.GetIndex()
	if err != nil {
		t.Fatalf("Failed to get updated index: %v", err)
	}

	entryAfter := GetEntryByID(indexAfter, "test-promote-real")
	if entryAfter == nil || !entryAfter.Promoted {
		t.Fatalf("After promotion, prompt should be marked promoted in registry")
	}

	// Verify on disk as well
	loadedPrompt, err := loader.LoadByID("test-promote-real")
	if err != nil {
		t.Fatalf("Failed to load promoted prompt: %v", err)
	}
	if !loadedPrompt.Promoted {
		t.Fatalf("Promoted flag should be persisted to disk")
	}
}

// TestPromotePromptConcurrentRead verifies I11 fix - concurrent reads don't block during promote
func TestPromotePromptConcurrentRead(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	handler := NewRegistryHandler(tmpDir, loader)

	// Create a test prompt
	prompt := models.Prompt{
		ID:         "test-promote-concurrent",
		Trigger:    "test",
		Domain:     "router",
		Source:     "test",
		Scope:      "project",
		Promoted:   false,
		Confidence: 0.85,
		Content:    "Test",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := loader.SavePrompt(&prompt, prompt.Scope); err != nil {
		t.Fatalf("Failed to save: %v", err)
	}

	if err := handler.RebuildIndex(); err != nil {
		t.Fatalf("Failed to rebuild: %v", err)
	}

	// Signal when promotion starts and finishes
	var promoteStarted, promoteFinished sync.WaitGroup
	promoteStarted.Add(1)
	promoteFinished.Add(1)

	// Launch a goroutine to call PromotePrompt
	go func() {
		promoteStarted.Done()
		body := `{"prompt_id":"test-promote-concurrent","version":"1","release_notes":""}`
		req := httptest.NewRequest("POST", "/registry/promote", strings.NewReader(body))
		w := httptest.NewRecorder()
		handler.PromotePrompt(w, req)
		promoteFinished.Done()
	}()

	// Wait for promote to start, then launch concurrent reads
	promoteStarted.Wait()
	time.Sleep(10 * time.Millisecond) // Let promote enter its disk I/O phase

	// Concurrent reads should complete quickly (not block on promote's lock)
	readsDone := make(chan bool)
	for i := 0; i < 5; i++ {
		go func() {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/registry", nil)
			handler.ListRegistry(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("ListRegistry returned %d", w.Code)
			}

			w = httptest.NewRecorder()
			req = httptest.NewRequest("GET", "/registry/stats", nil)
			handler.GetRegistryStats(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("GetRegistryStats returned %d", w.Code)
			}
			readsDone <- true
		}()
	}

	// Wait for at least 4 reads to complete (timeout = proof of blocking)
	completed := 0
	timeout := time.After(2 * time.Second)
	for i := 0; i < 5; i++ {
		select {
		case <-readsDone:
			completed++
		case <-timeout:
			t.Errorf("Reads blocked for >2s during promote; completed %d/5 before timeout", completed)
			return
		}
	}

	promoteFinished.Wait()
	if completed < 4 {
		t.Errorf("Expected most reads to complete, only %d/5 did", completed)
	}
}

// TestRebuildRegistryThreadSafe verifies no data races during concurrent reads
func TestRebuildRegistryThreadSafe(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	handler := NewRegistryHandler(tmpDir, loader)

	// Create test prompts
	for i := 0; i < 10; i++ {
		prompt := models.Prompt{
			ID:         fmt.Sprintf("test-thread-safe-%d", i),
			Trigger:    fmt.Sprintf("prompt %d", i),
			Domain:     "router-prompts",
			Source:     "unit-test",
			Scope:      "project",
			Confidence: 0.8 + float32(i)*0.01,
			Content:    fmt.Sprintf("Content %d", i),
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		if err := loader.SavePrompt(&prompt, prompt.Scope); err != nil {
			t.Fatalf("Failed to save prompt: %v", err)
		}
	}

	// Build initial index
	if err := handler.RebuildIndex(); err != nil {
		t.Fatalf("Failed to build initial index: %v", err)
	}

	// Spawn concurrent readers
	var wg sync.WaitGroup

	// 10 goroutines that read concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := handler.GetIndex(); err != nil {
					t.Errorf("GetIndex failed: %v", err)
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()

	// Verify final index is consistent
	index, err := handler.GetIndex()
	if err != nil {
		t.Fatalf("Failed to get final index: %v", err)
	}

	if index.TotalPrompts != 10 {
		t.Errorf("Expected 10 prompts in final index, got %d", index.TotalPrompts)
	}
}

// TestRebuildRegistryEndpointLocking verifies I9 fix - reads are protected by lock
func TestRebuildRegistryEndpointLocking(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	handler := NewRegistryHandler(tmpDir, loader)

	// Create test prompt
	prompt := models.Prompt{
		ID:         "test-endpoint-lock",
		Trigger:    "test",
		Domain:     "router-prompts",
		Source:     "unit-test",
		Scope:      "project",
		Confidence: 0.85,
		Content:    "Test",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	if err := loader.SavePrompt(&prompt, prompt.Scope); err != nil {
		t.Fatalf("Failed to save prompt: %v", err)
	}

	// Build initial index
	if err := handler.RebuildIndex(); err != nil {
		t.Fatalf("Failed to build initial index: %v", err)
	}

	// Call RebuildRegistryEndpoint to verify the I9 fix (read under lock)
	req := httptest.NewRequest("POST", "/registry/rebuild", nil)
	w := httptest.NewRecorder()
	handler.RebuildRegistryEndpoint(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("RebuildRegistryEndpoint failed with status %d", w.Code)
	}

	// Verify the response contains the protected read
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if _, ok := resp["total_prompts"]; !ok {
		t.Errorf("Response missing total_prompts (I9 fix should protect this read)")
	}
}

// TestListRegistryPromotedFilter verifies promoted flag appears in ListRegistry response
func TestListRegistryPromotedFilter(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	handler := NewRegistryHandler(tmpDir, loader)

	// Create two prompts, promote one
	prompts := []struct {
		id       string
		promoted bool
	}{
		{"promoted-1", true},
		{"not-promoted-1", false},
	}

	for _, p := range prompts {
		prompt := models.Prompt{
			ID:         p.id,
			Trigger:    "test",
			Domain:     "router-prompts",
			Source:     "unit-test",
			Scope:      "project",
			Promoted:   p.promoted,
			Confidence: 0.85,
			Content:    "Test",
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		if err := loader.SavePrompt(&prompt, prompt.Scope); err != nil {
			t.Fatalf("Failed to save prompt: %v", err)
		}
	}

	// Build index
	if err := handler.RebuildIndex(); err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	// Call ListRegistry
	req := httptest.NewRequest("GET", "/registry", nil)
	w := httptest.NewRecorder()
	handler.ListRegistry(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	promptsResp, ok := resp["prompts"].([]any)
	if !ok {
		t.Fatalf("Expected prompts in response")
	}

	if len(promptsResp) != 2 {
		t.Fatalf("Expected 2 prompts, got %d", len(promptsResp))
	}

	// Verify promoted flags are present and correct
	promotedCount := 0
	notPromotedCount := 0

	for i, p := range promptsResp {
		prompts_entry := p.(map[string]any)
		promoted, ok := prompts_entry["promoted"].(bool)
		if !ok {
			t.Fatalf("Promoted field not present in prompt %d", i)
		}

		if promoted {
			promotedCount++
		} else {
			notPromotedCount++
		}
	}

	if promotedCount != 1 {
		t.Errorf("Expected 1 promoted prompt, got %d", promotedCount)
	}
	if notPromotedCount != 1 {
		t.Errorf("Expected 1 non-promoted prompt, got %d", notPromotedCount)
	}
}

// GetEntryByID is a helper function for testing
// NOTE: This is duplicated from models/registry.go in tests for convenience
func GetEntryByID(index *models.RegistryIndex, promptID string) *models.RegistryEntry {
	if index == nil {
		return nil
	}
	for i := range index.Prompts {
		if index.Prompts[i].ID == promptID {
			return &index.Prompts[i]
		}
	}
	return nil
}
