package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// TrinityWriter handles writing prompt feedback facts to Trinity knowledge base
type TrinityWriter struct {
	factsDir string
}

// NewTrinityWriter creates a new TrinityWriter
func NewTrinityWriter(dataHome string) *TrinityWriter {
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local/share")
	}
	return &TrinityWriter{
		factsDir: filepath.Join(dataHome, "trinity-facts"),
	}
}

// TrinityFact represents a fact to be added to Trinity
type TrinityFact struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// LogPromptPromotion logs a prompt promotion event to Trinity queue
func (tw *TrinityWriter) LogPromptPromotion(p *models.Prompt) error {
	if p == nil || p.ID == "" {
		return fmt.Errorf("prompt missing required fields for Trinity logging")
	}

	// Create facts directory if needed
	if err := os.MkdirAll(tw.factsDir, 0750); err != nil {
		return fmt.Errorf("error creating facts directory: %w", err)
	}

	// Build Trinity fact
	fact := TrinityFact{
		ID: fmt.Sprintf("prompt-promoted-%s-%d", p.ID, time.Now().Unix()),
		Content: fmt.Sprintf(
			"Prompt '%s' (domain: %s) promoted to global scope with confidence %.2f after testing by %d agents",
			p.ID, p.Domain, p.Confidence, len(p.AgentsTested),
		),
		Tags: []string{"prompts", "promotion", "confidence-evolved", p.Domain},
	}

	// Write as TSV line for easy import (ID\tCONTENT\tTAGS)
	tags := strings.Join(fact.Tags, ",")
	entry := fmt.Sprintf("%s\t%s\t%s\n", fact.ID, fact.Content, tags)

	// Append to trinity-facts.tsv for bulk Trinity import
	factsFile := filepath.Join(tw.factsDir, "trinity-facts.tsv")
	f, err := os.OpenFile(factsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("error opening facts file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("error writing fact: %w", err)
	}

	return nil
}

// LogFeedbackObservation logs a feedback observation to Trinity queue
func (tw *TrinityWriter) LogFeedbackObservation(fb *models.Feedback) error {
	if fb == nil || fb.PromptID == "" {
		return fmt.Errorf("feedback missing required fields")
	}

	// Create facts directory if needed
	if err := os.MkdirAll(tw.factsDir, 0750); err != nil {
		return fmt.Errorf("error creating facts directory: %w", err)
	}

	// Build Trinity fact for feedback
	status := "failed"
	if fb.Success {
		status = "succeeded"
	}

	fact := TrinityFact{
		ID: fmt.Sprintf("feedback-%s-%s-%d", fb.PromptID, fb.Agent, time.Now().Unix()),
		Content: fmt.Sprintf(
			"Agent '%s' tested prompt '%s' on task '%s' and %s. Note: %s",
			fb.Agent, fb.PromptID, fb.Task, status, fb.Note,
		),
		Tags: []string{"prompts", "feedback", "test-result", status, fb.Agent},
	}

	// Append to trinity-facts.tsv
	tags := strings.Join(fact.Tags, ",")
	entry := fmt.Sprintf("%s\t%s\t%s\n", fact.ID, fact.Content, tags)

	factsFile := filepath.Join(tw.factsDir, "trinity-facts.tsv")
	f, err := os.OpenFile(factsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("error opening facts file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("error writing fact: %w", err)
	}

	return nil
}

// ExportForTrinity reads accumulated facts and formats them for Trinity import
// This is called by the daily pipeline to push facts into Trinity
func (tw *TrinityWriter) ExportForTrinity() (string, error) {
	factsFile := filepath.Join(tw.factsDir, "trinity-facts.tsv")

	// Check if file exists
	if _, err := os.Stat(factsFile); os.IsNotExist(err) {
		return "", nil // No facts accumulated yet
	}

	// Read all facts
	content, err := os.ReadFile(factsFile)
	if err != nil {
		return "", fmt.Errorf("error reading facts file: %w", err)
	}

	return string(content), nil
}

// ClearExported clears the facts file after successful Trinity import
func (tw *TrinityWriter) ClearExported() error {
	factsFile := filepath.Join(tw.factsDir, "trinity-facts.tsv")
	return os.Remove(factsFile)
}
