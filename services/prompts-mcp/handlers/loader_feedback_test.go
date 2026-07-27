package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Tests for PromptLoader confidence feedback updates

func TestUpdateConfidenceIncrement(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test prompt
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: confidence-test
domain: router-prompts
confidence: 0.5
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	updated, err := loader.UpdateConfidence(context.Background(), "confidence-test", 0.2)

	if err != nil {
		t.Fatalf("UpdateConfidence failed: %v", err)
	}
	if updated.Confidence != 0.7 {
		t.Errorf("expected confidence 0.7, got %f", updated.Confidence)
	}
}

func TestUpdateConfidenceDecrement(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test prompt
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: confidence-test
domain: router-prompts
confidence: 0.7
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	updated, err := loader.UpdateConfidence(context.Background(), "confidence-test", -0.2)

	if err != nil {
		t.Fatalf("UpdateConfidence failed: %v", err)
	}
	if updated.Confidence != 0.5 {
		t.Errorf("expected confidence 0.5, got %f", updated.Confidence)
	}
}

func TestUpdateConfidenceClamps(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test prompt
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: clamp-test
domain: router-prompts
confidence: 0.9
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	// Try to increment above 1
	updated, err := loader.UpdateConfidence(context.Background(), "clamp-test", 0.5)

	if err != nil {
		t.Fatalf("UpdateConfidence failed: %v", err)
	}
	if updated.Confidence != 1.0 {
		t.Errorf("expected confidence clamped to 1.0, got %f", updated.Confidence)
	}

	// Try to decrement below 0
	updated, err = loader.UpdateConfidence(context.Background(), "clamp-test", -2.0)

	if err != nil {
		t.Fatalf("UpdateConfidence failed: %v", err)
	}
	if updated.Confidence != 0.0 {
		t.Errorf("expected confidence clamped to 0.0, got %f", updated.Confidence)
	}
}

func TestUpdateConfidenceNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create empty structure
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	updated, err := loader.UpdateConfidence(context.Background(), "nonexistent", 0.1)

	if err == nil {
		t.Fatal("expected error for nonexistent prompt")
	}
	if updated != nil {
		t.Errorf("expected nil, got %v", updated)
	}
}

func TestUpdateConfidenceUpdatesTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Create test prompt
	personalDir := filepath.Join(tmpDir, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	promptYAML := `---
id: timestamp-test
domain: router-prompts
confidence: 0.5
---
Content`

	if err := os.WriteFile(filepath.Join(personalDir, "test.yaml"), []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write prompt: %v", err)
	}

	beforeUpdate := time.Now().UTC()
	time.Sleep(10 * time.Millisecond) // Ensure time passes

	updated, err := loader.UpdateConfidence(context.Background(), "timestamp-test", 0.1)

	if err != nil {
		t.Fatalf("UpdateConfidence failed: %v", err)
	}

	if updated.UpdatedAt.Before(beforeUpdate) {
		t.Error("UpdatedAt should be updated to current time")
	}
}

func TestUpdateConfidenceContextTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Acquire the lock to block UpdateConfidence
	if err := loader.acquireWrite(context.Background()); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer loader.releaseWrite()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	updated, err := loader.UpdateConfidence(ctx, "some-id", 0.1)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if updated != nil {
		t.Errorf("expected nil, got %v", updated)
	}
}
