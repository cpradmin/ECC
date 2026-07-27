package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// TestSubmitFeedback tests feedback submission
func TestSubmitFeedback(t *testing.T) {
	tmpDir := t.TempDir()
	fm := newTestFeedbackManager(t, tmpDir, "test-prompt-01")

	feedback := &models.Feedback{
		PromptID: "test-prompt-01",
		Agent:    "nova",
		Task:     "code-review",
		Success:  true,
		Note:     "Correctly classified intent",
	}

	err := fm.SubmitFeedback(feedback)
	if err != nil {
		t.Fatalf("SubmitFeedback failed: %v", err)
	}

	// Verify feedback was written
	feedbackFile := filepath.Join(tmpDir, "ecc-prompts", "projects", "ember-swarm", "feedback.jsonl")
	if _, err := os.Stat(feedbackFile); os.IsNotExist(err) {
		t.Fatalf("feedback.jsonl not created")
	}
}

// TestLoadFeedbackLog tests loading feedback for a prompt
func TestLoadFeedbackLog(t *testing.T) {
	tmpDir := t.TempDir()
	fm := newTestFeedbackManager(t, tmpDir, "test-01", "test-02")

	// Submit multiple feedback entries
	feedbacks := []models.Feedback{
		{
			PromptID: "test-01",
			Agent:    "nova",
			Task:     "task1",
			Success:  true,
		},
		{
			PromptID: "test-01",
			Agent:    "eve",
			Task:     "task2",
			Success:  false,
		},
		{
			PromptID: "test-02",
			Agent:    "claw",
			Task:     "task3",
			Success:  true,
		},
	}

	for _, fb := range feedbacks {
		if err := fm.SubmitFeedback(&fb); err != nil {
			t.Fatalf("Failed to submit feedback: %v", err)
		}
	}

	// Load feedback for test-01
	loaded, err := fm.LoadFeedbackLog("test-01")
	if err != nil {
		t.Fatalf("LoadFeedbackLog failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Errorf("expected 2 feedback entries, got %d", len(loaded))
	}

	// Verify filtering
	for _, fb := range loaded {
		if fb.PromptID != "test-01" {
			t.Errorf("expected PromptID test-01, got %s", fb.PromptID)
		}
	}
}

// TestEvolveConfidence tests confidence evolution
func TestEvolveConfidence(t *testing.T) {
	tmpDir := t.TempDir()
	fm := newTestFeedbackManager(t, tmpDir, "evolve-test-01")

	promptID := "evolve-test-01"

	// Submit 3 successes and 1 failure
	feedbacks := []models.Feedback{
		{PromptID: promptID, Agent: "nova", Success: true},
		{PromptID: promptID, Agent: "eva", Success: true},
		{PromptID: promptID, Agent: "claw", Success: true},
		{PromptID: promptID, Agent: "selah", Success: false},
	}

	for _, fb := range feedbacks {
		if err := fm.SubmitFeedback(&fb); err != nil {
			t.Fatalf("Failed to submit feedback: %v", err)
		}
	}

	// Create a prompt to test evolution
	prompt := &models.Prompt{
		ID:         promptID,
		Confidence: 0.70,
	}

	update, err := fm.EvolveConfidence(prompt)
	if err != nil {
		t.Fatalf("EvolveConfidence failed: %v", err)
	}

	// Expected: 0.70 + (3 * 0.05) - (1 * 0.10) = 0.70 + 0.15 - 0.10 = 0.75
	expectedConfidence := float32(0.75)
	if update.NewConfidence != expectedConfidence {
		t.Errorf("expected confidence %.2f, got %.2f", expectedConfidence, update.NewConfidence)
	}

	// Allow small floating-point tolerance
	if update.Delta < 0.04 || update.Delta > 0.06 {
		t.Errorf("expected delta ~0.05, got %f", update.Delta)
	}
}

// TestConfidenceBounds tests confidence clamping
func TestConfidenceBounds(t *testing.T) {
	tmpDir := t.TempDir()
	fm := newTestFeedbackManager(t, tmpDir, "bounds-test-01")

	promptID := "bounds-test-01"

	// Submit many failures to push confidence below 0.3
	for i := 0; i < 10; i++ {
		fb := models.Feedback{
			PromptID: promptID,
			Agent:    "test-agent",
			Success:  false,
		}
		if err := fm.SubmitFeedback(&fb); err != nil {
			t.Fatalf("Failed to submit feedback: %v", err)
		}
	}

	prompt := &models.Prompt{
		ID:         promptID,
		Confidence: 0.50,
	}

	update, err := fm.EvolveConfidence(prompt)
	if err != nil {
		t.Fatalf("EvolveConfidence failed: %v", err)
	}

	// Should be clamped to 0.3
	if update.NewConfidence != 0.3 {
		t.Errorf("expected confidence 0.3 (clamped), got %.2f", update.NewConfidence)
	}
}

// TestCheckPromotion tests promotion logic
func TestCheckPromotion(t *testing.T) {
	tmpDir := t.TempDir()
	fm := NewFeedbackManager(tmpDir)

	// Test 1: Project-scoped prompt with sufficient confidence and agents
	prompt1 := &models.Prompt{
		ID:           "promote-test-01",
		Scope:        "project",
		Confidence:   0.85,
		AgentsTested: []string{"nova", "eve", "claw"},
	}

	shouldPromote, err := fm.CheckPromotion(prompt1)
	if err != nil {
		t.Fatalf("CheckPromotion failed: %v", err)
	}
	if !shouldPromote {
		t.Errorf("expected promotion, but got false")
	}

	// Test 2: Insufficient confidence
	prompt2 := &models.Prompt{
		ID:           "promote-test-02",
		Scope:        "project",
		Confidence:   0.70,
		AgentsTested: []string{"nova", "eve", "claw"},
	}

	shouldPromote, err = fm.CheckPromotion(prompt2)
	if err != nil {
		t.Fatalf("CheckPromotion failed: %v", err)
	}
	if shouldPromote {
		t.Errorf("expected no promotion (low confidence), but got true")
	}

	// Test 3: Insufficient agents tested
	prompt3 := &models.Prompt{
		ID:           "promote-test-03",
		Scope:        "project",
		Confidence:   0.85,
		AgentsTested: []string{"nova", "eve"},
	}

	shouldPromote, err = fm.CheckPromotion(prompt3)
	if err != nil {
		t.Fatalf("CheckPromotion failed: %v", err)
	}
	if shouldPromote {
		t.Errorf("expected no promotion (few agents), but got true")
	}

	// Test 4: Already global scope
	prompt4 := &models.Prompt{
		ID:           "promote-test-04",
		Scope:        "global",
		Confidence:   0.85,
		AgentsTested: []string{"nova", "eve", "claw"},
	}

	shouldPromote, err = fm.CheckPromotion(prompt4)
	if err != nil {
		t.Fatalf("CheckPromotion failed: %v", err)
	}
	if shouldPromote {
		t.Errorf("expected no promotion (already global), but got true")
	}
}

// TestCalculateSuccessRate tests success rate calculation
func TestCalculateSuccessRate(t *testing.T) {
	tmpDir := t.TempDir()
	fm := newTestFeedbackManager(t, tmpDir, "rate-test-01")

	promptID := "rate-test-01"

	// Submit 3 successes and 2 failures (60% success rate)
	feedbacks := []models.Feedback{
		{PromptID: promptID, Success: true},
		{PromptID: promptID, Success: true},
		{PromptID: promptID, Success: true},
		{PromptID: promptID, Success: false},
		{PromptID: promptID, Success: false},
	}

	for _, fb := range feedbacks {
		if err := fm.SubmitFeedback(&fb); err != nil {
			t.Fatalf("Failed to submit feedback: %v", err)
		}
	}

	rate, err := fm.CalculateSuccessRate(promptID)
	if err != nil {
		t.Fatalf("CalculateSuccessRate failed: %v", err)
	}

	expectedRate := float32(0.6)
	if rate != expectedRate {
		t.Errorf("expected success rate %.2f, got %.2f", expectedRate, rate)
	}
}

// newTestFeedbackManager builds a FeedbackManager whose prompt corpus already
// contains the given IDs.
//
// B5 made prompt existence a precondition of SubmitFeedback, so these tests can
// no longer submit against IDs that were never created. Seeding real prompts is
// also closer to production than the old behaviour, where feedback for an
// unknown prompt was accepted and silently skipped the confidence update.
func newTestFeedbackManager(t *testing.T, dataHome string, promptIDs ...string) *FeedbackManager {
	t.Helper()
	loader := NewPromptLoader(dataHome)
	now := time.Now().UTC()
	for _, id := range promptIDs {
		p := models.Prompt{
			ID:         id,
			Domain:     "router-prompts",
			Scope:      "project",
			Confidence: 0.7,
			Content:    "seeded for " + id,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := loader.SavePrompt(&p, p.Scope); err != nil {
			t.Fatalf("seed prompt %s: %v", id, err)
		}
	}
	return NewFeedbackManagerWithLoader(dataHome, loader)
}
