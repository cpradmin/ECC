package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// Tests for PromptLoader write operations

func TestSavePromptNew(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	prompt := &models.Prompt{
		ID:        "new-prompt",
		Domain:    "router-prompts",
		Content:   "Test content",
		Trigger:   "when testing",
		Confidence: 0.75,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	err := loader.SavePrompt(prompt, "project")

	if err != nil {
		t.Fatalf("SavePrompt failed: %v", err)
	}

	// Verify file was written
	expectedPath := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts", "new-prompt.yaml")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("expected prompt file to exist at %s", expectedPath)
	}
}

func TestSavePromptUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create initial prompt
	prompt := &models.Prompt{
		ID:        "update-prompt",
		Domain:    "router-prompts",
		Content:   "Original content",
		Trigger:   "when testing",
		Confidence: 0.5,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := loader.SavePrompt(prompt, "project"); err != nil {
		t.Fatalf("initial SavePrompt failed: %v", err)
	}

	// Update the prompt
	prompt.Confidence = 0.85
	prompt.Content = "Updated content"

	if err := loader.SavePrompt(prompt, "project"); err != nil {
		t.Fatalf("update SavePrompt failed: %v", err)
	}

	// Verify updated content
	loaded, err := loader.LoadByID("update-prompt")
	if err != nil {
		t.Fatalf("LoadByID failed: %v", err)
	}
	if loaded.Confidence != 0.85 {
		t.Errorf("expected updated confidence 0.85, got %f", loaded.Confidence)
	}
	if loaded.Content != "Updated content" {
		t.Errorf("expected updated content, got %s", loaded.Content)
	}
}

func TestSavePromptPathValidation(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Try to save with invalid path traversal ID
	prompt := &models.Prompt{
		ID:        "../../../etc/passwd",
		Domain:    "router-prompts",
		Content:   "Malicious",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	err := loader.SavePrompt(prompt, "project")

	if err == nil {
		t.Fatal("expected error for path traversal attempt")
	}
}

func TestSavePromptCreatesDirs(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	prompt := &models.Prompt{
		ID:        "dir-test",
		Domain:    "new-domain-prompts",
		Content:   "Test",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	err := loader.SavePrompt(prompt, "project")

	if err != nil {
		t.Fatalf("SavePrompt failed: %v", err)
	}

	// Verify directories were created
	expectedDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "new-domain-prompts")
	if _, err := os.Stat(expectedDir); os.IsNotExist(err) {
		t.Errorf("expected directory to exist at %s", expectedDir)
	}
}

func TestSavePromptInvalidatesCacheAfterWrite(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)
	loader.SetCacheTTL(10 * time.Second)

	// Prime the cache
	loader.cache.mu.Lock()
	loader.cache.prompts = []models.Prompt{{ID: "old"}}
	loader.cache.loadedAt = time.Now()
	loader.cache.valid = true
	loader.cache.mu.Unlock()

	prompt := &models.Prompt{
		ID:        "new-prompt",
		Domain:    "router-prompts",
		Content:   "Test",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	loader.SavePrompt(prompt, "project")

	loader.cache.mu.RLock()
	isValid := loader.cache.valid
	loader.cache.mu.RUnlock()

	if isValid {
		t.Error("expected cache to be invalidated after SavePrompt")
	}
}

func TestSavePromptPreservesFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create initial prompt with custom field in frontmatter
	originalFrontmatter := `id: preserve-test
domain: router-prompts
confidence: 0.5
custom_field: custom_value
`

	prompt := &models.Prompt{
		ID:              "preserve-test",
		Domain:          "router-prompts",
		Content:         "Original",
		Confidence:      0.5,
		FrontmatterYAML: originalFrontmatter,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	err := loader.SavePrompt(prompt, "project")

	if err != nil {
		t.Fatalf("SavePrompt failed: %v", err)
	}

	// Re-load and check if frontmatter is preserved
	loaded, err := loader.LoadByID("preserve-test")
	if err != nil {
		t.Fatalf("LoadByID failed: %v", err)
	}

	if !strings.Contains(loaded.FrontmatterYAML, "custom_field") {
		t.Error("expected custom frontmatter field to be preserved")
	}
}

func TestSavePromptContextTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Acquire the lock to block SavePromptContext
	if err := loader.acquireWrite(context.Background()); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer loader.releaseWrite()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	prompt := &models.Prompt{
		ID:        "timeout-test",
		Domain:    "router-prompts",
		Content:   "Test",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	err := loader.SavePromptContext(ctx, prompt, "project")

	if err == nil {
		t.Fatal("expected timeout error")
	}
}
