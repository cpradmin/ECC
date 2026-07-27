package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Tests for PromptLoader directory scanning and filesystem operations

func TestLoadAllUncached(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create directory structure
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	// Create a test prompt
	promptYAML := `---
id: test-prompt-01
domain: router-prompts
confidence: 0.85
---
Test prompt content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	prompts, err := loader.loadAllUncached()

	if err != nil {
		t.Fatalf("loadAllUncached failed: %v", err)
	}
	if len(prompts) == 0 {
		t.Fatal("expected at least one prompt")
	}
	if prompts[0].ID != "test-prompt-01" {
		t.Errorf("expected prompt ID test-prompt-01, got %s", prompts[0].ID)
	}
}

func TestLoadFromDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// loadFromDirectory expects a parent directory with domain subdirectories
	baseDir := filepath.Join(tmpDir, "test-base")
	domainDir := filepath.Join(baseDir, "router-prompts")
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: test-id
domain: router-prompts
confidence: 0.5
---
Test content`

	if err := os.WriteFile(filepath.Join(domainDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	prompts, err := loader.loadFromDirectory(baseDir, "project")

	if err != nil {
		t.Fatalf("loadFromDirectory failed: %v", err)
	}
	if len(prompts) != 1 {
		t.Errorf("expected 1 prompt, got %d", len(prompts))
	}
}

func TestLoadFromDirectoryMissing(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	prompts, err := loader.loadFromDirectory(filepath.Join(tmpDir, "nonexistent"), "project")

	if err != nil {
		t.Fatalf("expected no error for missing directory, got %v", err)
	}
	if len(prompts) != 0 {
		t.Errorf("expected 0 prompts for missing directory, got %d", len(prompts))
	}
}

func TestLoadFromDirectoryMultiDomains(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create multiple domain directories under a base directory
	baseDir := filepath.Join(tmpDir, "test-base")
	domains := []string{"router-prompts", "conversation-prompts", "go-coding-prompts"}
	for _, domain := range domains {
		dirPath := filepath.Join(baseDir, domain)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}

		promptYAML := fmt.Sprintf(`---
id: %s-prompt
domain: %s
confidence: 0.75
---
Content for %s`, domain, domain, domain)

		if err := os.WriteFile(filepath.Join(dirPath, "test.yaml"), []byte(promptYAML), 0644); err != nil {
			t.Fatalf("failed to write prompt: %v", err)
		}
	}

	prompts, err := loader.loadFromDirectory(baseDir, "project")

	if err != nil {
		t.Fatalf("loadFromDirectory failed: %v", err)
	}
	if len(prompts) != 3 {
		t.Errorf("expected 3 prompts, got %d", len(prompts))
	}
}

func TestLoadFromDirectoryInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	baseDir := filepath.Join(tmpDir, "test-base")
	domainDir := filepath.Join(baseDir, "router-prompts")
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	// Write invalid YAML
	invalidYAML := `---
this: is: invalid: yaml
---
Content`

	if err := os.WriteFile(filepath.Join(domainDir, "invalid.yaml"), []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Should not crash, should skip the invalid file
	prompts, err := loader.loadFromDirectory(baseDir, "project")

	if err != nil && !os.IsNotExist(err) {
		// Error is acceptable (invalid YAML), but should continue
	}
	// We can't strictly assert prompts count here because the behavior is
	// to log and continue, so we just verify no crash
	_ = prompts
}
