package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// Tests for PromptLoader read operations

func TestLoadAllUncachedViaLoadAll(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	loader.SetCacheTTL(10 * time.Second)

	// Create test data
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: test-prompt
domain: router-prompts
confidence: 0.85
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	prompts, err := loader.LoadAll()

	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(prompts) == 0 {
		t.Fatal("expected prompts")
	}

	// Check that cache miss was recorded
	stats := loader.CacheStats()
	if stats.Misses < 1 {
		t.Error("expected cache miss to be recorded")
	}
}

func TestLoadAllCached(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	loader.SetCacheTTL(10 * time.Second)

	// Create test data
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: test-prompt
domain: router-prompts
confidence: 0.85
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	// First load
	loader.LoadAll()

	// Second load should hit cache
	before := loader.cache.hits.Load()
	loader.LoadAll()
	after := loader.cache.hits.Load()

	if after <= before {
		t.Errorf("expected cache hit to be recorded, before=%d after=%d", before, after)
	}
}

func TestLoadByIDFound(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test data
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: test-prompt-123
domain: router-prompts
confidence: 0.85
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	prompt, err := loader.LoadByID("test-prompt-123")

	if err != nil {
		t.Fatalf("LoadByID failed: %v", err)
	}
	if prompt == nil {
		t.Fatal("expected prompt, got nil")
	}
	if prompt.ID != "test-prompt-123" {
		t.Errorf("expected ID test-prompt-123, got %s", prompt.ID)
	}
}

func TestLoadByIDNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create empty structure
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	prompt, err := loader.LoadByID("nonexistent")

	if err == nil {
		t.Fatal("expected error for nonexistent prompt")
	}
	if prompt != nil {
		t.Errorf("expected nil prompt, got %v", prompt)
	}

	// B5 fix: should be wrapped so errors.Is() works
	if !strings.Contains(err.Error(), "prompt not found") {
		t.Errorf("expected 'prompt not found' in error, got: %v", err)
	}
}

func TestLoadByIDContext_Timeout(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test data
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: test-prompt
domain: router-prompts
confidence: 0.85
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	// Simulate slow filesystem
	loader.setIODelay(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	prompt, err := loader.LoadByIDContext(ctx, "test-prompt")

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if prompt != nil {
		t.Errorf("expected nil prompt on timeout, got %v", prompt)
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected timeout/context error, got: %v", err)
	}
}

func TestLoadByDomain(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test data with multiple domains
	for i, domain := range []string{"router-prompts", "conversation-prompts"} {
		domainDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", domain)
		if err := os.MkdirAll(domainDir, 0755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}

		promptYAML := fmt.Sprintf(`---
id: prompt-%d
domain: %s
confidence: 0.85
---
Content`, i, domain)

		if err := os.WriteFile(filepath.Join(domainDir, fmt.Sprintf("test%d.yaml", i)), []byte(promptYAML), 0644); err != nil {
			t.Fatalf("failed to write prompt: %v", err)
		}
	}

	prompts, err := loader.LoadByDomain("router-prompts")

	if err != nil {
		t.Fatalf("LoadByDomain failed: %v", err)
	}
	if len(prompts) != 1 {
		t.Errorf("expected 1 prompt with router-prompts domain, got %d", len(prompts))
	}
	if prompts[0].Domain != "router-prompts" {
		t.Errorf("expected domain router-prompts, got %s", prompts[0].Domain)
	}
}

func TestLoadByScope(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create in personal (project scope)
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: personal-prompt
domain: router-prompts
confidence: 0.85
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	prompts, err := loader.LoadByScope("project")

	if err != nil {
		t.Fatalf("LoadByScope failed: %v", err)
	}
	if len(prompts) >= 1 {
		if prompts[0].Scope != "project" {
			t.Errorf("expected scope project, got %s", prompts[0].Scope)
		}
	}
}

func TestSearch(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test data
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: test-search-01
domain: router-prompts
confidence: 0.85
---
This prompt is for classification of intent with keyword search`

	if err := os.WriteFile(filepath.Join(personalDir, "test-search.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	results, err := loader.Search("classification", "")

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearchWithDomain(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test data with multiple domains
	for i, domain := range []string{"router-prompts", "conversation-prompts"} {
		domainDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", domain)
		if err := os.MkdirAll(domainDir, 0755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}

		promptYAML := fmt.Sprintf(`---
id: prompt-%d
domain: %s
confidence: 0.85
---
Search for this keyword`, i, domain)

		if err := os.WriteFile(filepath.Join(domainDir, fmt.Sprintf("test%d.yaml", i)), []byte(promptYAML), 0644); err != nil {
			t.Fatalf("failed to write prompt: %v", err)
		}
	}

	// Search only in router-prompts domain
	results, err := loader.Search("keyword", "router-prompts")

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result in router-prompts, got %d", len(results))
	}
	if results[0].Domain != "router-prompts" {
		t.Errorf("expected router-prompts domain, got %s", results[0].Domain)
	}
}

func TestPromptExists_True(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test data
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: existing-prompt
domain: router-prompts
confidence: 0.85
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	exists, err := loader.PromptExists(context.Background(), "existing-prompt")

	if err != nil {
		t.Fatalf("PromptExists failed: %v", err)
	}
	if !exists {
		t.Error("expected prompt to exist")
	}
}

func TestPromptExists_False(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create empty structure
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	exists, err := loader.PromptExists(context.Background(), "nonexistent")

	if err != nil {
		t.Fatalf("PromptExists failed: %v", err)
	}
	if exists {
		t.Error("expected prompt to not exist")
	}
}

// Integration tests for atomic read-modify-write and concurrency

func TestReadModifyWriteAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test prompt
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: rmw-test
domain: router-prompts
confidence: 0.5
trigger: test trigger
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	// Load, modify, save
	prompt, err := loader.LoadByID("rmw-test")
	if err != nil {
		t.Fatalf("LoadByID failed: %v", err)
	}

	prompt.Confidence = 0.75
	prompt.Trigger = "modified trigger"

	if err := loader.SavePrompt(prompt, "project"); err != nil {
		t.Fatalf("SavePrompt failed: %v", err)
	}

	// Verify changes persisted
	reloaded, err := loader.LoadByID("rmw-test")
	if err != nil {
		t.Fatalf("reload LoadByID failed: %v", err)
	}

	if reloaded.Confidence != 0.75 {
		t.Errorf("expected confidence 0.75, got %f", reloaded.Confidence)
	}
	if reloaded.Trigger != "modified trigger" {
		t.Errorf("expected trigger 'modified trigger', got %s", reloaded.Trigger)
	}
}

func TestConcurrentWritersNoLostUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test prompt
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: concurrent-test
domain: router-prompts
confidence: 0.0
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	// Launch concurrent UpdateConfidence calls
	var wg sync.WaitGroup
	var updateCount atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := loader.UpdateConfidence(context.Background(), "concurrent-test", 0.1)
			if err == nil {
				updateCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// Verify final confidence is correct (should be 1.0, clamped)
	final, err := loader.LoadByID("concurrent-test")
	if err != nil {
		t.Fatalf("LoadByID failed: %v", err)
	}

	if final.Confidence != 1.0 {
		t.Errorf("expected final confidence 1.0, got %f (updates applied: %d)", final.Confidence, updateCount.Load())
	}
}

func TestRecoveryFromCorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create directory
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	// Write a corrupted file (invalid frontmatter)
	corruptYAML := `---
this: is: invalid
---
Content`

	filePath := filepath.Join(personalDir, "corrupt.yaml")
	if err := os.WriteFile(filePath, []byte(corruptYAML), 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}

	// Try to save over it with a valid prompt
	prompt := &models.Prompt{
		ID:              "corrupt",
		Domain:          "router-prompts",
		Content:         "Fixed content",
		Confidence:      0.5,
		FrontmatterYAML: corruptYAML, // Pass in the corrupted frontmatter
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	// Should not crash, should recover
	err := loader.SavePrompt(prompt, "project")
	if err != nil {
		t.Fatalf("SavePrompt failed to recover: %v", err)
	}

	// Verify file is now valid
	reloaded, err := loader.LoadByID("corrupt")
	if err != nil {
		t.Fatalf("LoadByID failed after recovery: %v", err)
	}
	if reloaded.Content != "Fixed content" {
		t.Errorf("expected 'Fixed content', got %s", reloaded.Content)
	}
}
