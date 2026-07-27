package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Tests for YAML parsing and frontmatter handling

func TestYamlFloat32Float64(t *testing.T) {
	val, ok := yamlFloat32(0.85)

	if !ok {
		t.Fatal("expected successful conversion")
	}
	if val != 0.85 {
		t.Errorf("expected 0.85, got %f", val)
	}
}

func TestYamlFloat32Integer(t *testing.T) {
	// yamlFloat32 fix: handles integer 1 as float32(1)
	val, ok := yamlFloat32(1)

	if !ok {
		t.Fatal("expected successful conversion for integer 1")
	}
	if val != 1.0 {
		t.Errorf("expected 1.0, got %f", val)
	}
}

func TestYamlFloat32Zero(t *testing.T) {
	// yamlFloat32 fix: handles integer 0 as float32(0)
	val, ok := yamlFloat32(0)

	if !ok {
		t.Fatal("expected successful conversion for integer 0")
	}
	if val != 0.0 {
		t.Errorf("expected 0.0, got %f", val)
	}
}

func TestYamlFloat32Int64(t *testing.T) {
	val, ok := yamlFloat32(int64(42))

	if !ok {
		t.Fatal("expected successful conversion for int64")
	}
	if val != 42.0 {
		t.Errorf("expected 42.0, got %f", val)
	}
}

func TestYamlFloat32Uint64(t *testing.T) {
	val, ok := yamlFloat32(uint64(99))

	if !ok {
		t.Fatal("expected successful conversion for uint64")
	}
	if val != 99.0 {
		t.Errorf("expected 99.0, got %f", val)
	}
}

func TestYamlFloat32Invalid(t *testing.T) {
	val, ok := yamlFloat32("not a number")

	if ok {
		t.Fatal("expected conversion failure for string")
	}
	if val != 0 {
		t.Errorf("expected 0 for failed conversion, got %f", val)
	}
}

func TestParsePromptFileValid(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	promptYAML := `---
id: test-classifier-01
trigger: "when classifying domain"
confidence: 0.85
domain: router-prompts
source: session-observation
agents_tested:
  - nova
  - eve
success_rate: 0.92
---
This is a test prompt content for classification.`

	testFile := filepath.Join(tmpDir, "test-prompt.yaml")
	if err := os.WriteFile(testFile, []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	prompt, err := loader.parsePromptFile(testFile, "project")

	if err != nil {
		t.Fatalf("parsePromptFile failed: %v", err)
	}
	if prompt.ID != "test-classifier-01" {
		t.Errorf("expected ID test-classifier-01, got %s", prompt.ID)
	}
	if prompt.Confidence != 0.85 {
		t.Errorf("expected confidence 0.85, got %f", prompt.Confidence)
	}
	if len(prompt.AgentsTested) != 2 {
		t.Errorf("expected 2 agents tested, got %d", len(prompt.AgentsTested))
	}
	if prompt.Scope != "project" {
		t.Errorf("expected scope project, got %s", prompt.Scope)
	}
}

func TestParsePromptFileMissingFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	// Missing frontmatter delimiters
	invalidYAML := "This has no frontmatter"

	testFile := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(testFile, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	prompt, err := loader.parsePromptFile(testFile, "project")

	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
	if prompt != nil {
		t.Errorf("expected nil prompt, got %v", prompt)
	}
}

func TestParsePromptFileInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	invalidYAML := `---
invalid: yaml: syntax: here
---
Content`

	testFile := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(testFile, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	prompt, err := loader.parsePromptFile(testFile, "project")

	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if prompt != nil {
		t.Errorf("expected nil prompt, got %v", prompt)
	}
}

func TestParsePromptFileTimestamps(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	promptYAML := fmt.Sprintf(`---
id: test-with-timestamps
domain: test
confidence: 0.5
created_at: %s
updated_at: %s
---
Content`, nowStr, nowStr)

	testFile := filepath.Join(tmpDir, "test.yaml")
	if err := os.WriteFile(testFile, []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	prompt, err := loader.parsePromptFile(testFile, "project")

	if err != nil {
		t.Fatalf("parsePromptFile failed: %v", err)
	}

	if prompt.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if prompt.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestParsePromptFileMissingRequiredID(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewPromptLoader(tmpDir)

	promptYAML := `---
domain: test
confidence: 0.5
---
Content`

	testFile := filepath.Join(tmpDir, "test.yaml")
	if err := os.WriteFile(testFile, []byte(promptYAML), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	prompt, err := loader.parsePromptFile(testFile, "project")

	if err == nil {
		t.Fatal("expected error for missing ID")
	}
	if prompt != nil {
		t.Errorf("expected nil prompt, got %v", prompt)
	}
}
