package handlers

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
	"gopkg.in/yaml.v3"
)

// frontmatterData holds structured frontmatter for proper YAML serialization
type frontmatterData struct {
	ID           string   `yaml:"id"`
	Domain       string   `yaml:"domain"`
	Trigger      string   `yaml:"trigger,omitempty"`
	Confidence   float32  `yaml:"confidence"`
	Source       string   `yaml:"source,omitempty"`
	Scope        string   `yaml:"scope"`
	Promoted     bool     `yaml:"promoted,omitempty"`
	SuccessRate  float32  `yaml:"success_rate,omitempty"`
	AgentsTested []string `yaml:"agents_tested,omitempty"`
	CreatedAt    string   `yaml:"created_at,omitempty"` // I11: Persist timestamps
	UpdatedAt    string   `yaml:"updated_at,omitempty"` // I11: Persist timestamps
}

// yamlFloat32 coerces a YAML scalar to float32, accepting both float and
// integer encodings.
//
// DATA-LOSS FIX: yaml.v3 decodes `confidence: 0.85` as float64 but
// `confidence: 1` and `confidence: 0` as int, because the marshaller drops the
// ".0" from whole numbers. The old code asserted `.(float64)` and, on failure,
// silently substituted the 0.5 default — so a prompt that reached full
// confidence (1) was reset to 0.5 the next time it was read, and so was one
// that bottomed out at 0. The learning loop was destroying exactly the extreme
// values it had worked hardest to establish, and the write-read cycle was not
// idempotent. Discovered by TestConcurrentFeedbackClampsAtBounds.
func yamlFloat32(v any) (float32, bool) {
	switch n := v.(type) {
	case float64:
		return float32(n), true
	case float32:
		return n, true
	case int:
		return float32(n), true
	case int64:
		return float32(n), true
	case uint64:
		return float32(n), true
	default:
		return 0, false
	}
}

// parsePromptFile parses a single YAML prompt file
func (pl *PromptLoader) parsePromptFile(filePath string, scope string) (*models.Prompt, error) {
	if d := pl.ioDelay.Load(); d > 0 {
		time.Sleep(time.Duration(d)) // test-only slow-filesystem simulation
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Split frontmatter and content
	parts := strings.SplitN(string(content), "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid prompt format: missing frontmatter delimiters")
	}

	frontmatter := parts[1]
	promptContent := parts[2]

	// Parse YAML frontmatter
	var data map[string]interface{}
	if err := yaml.Unmarshal([]byte(frontmatter), &data); err != nil {
		return nil, fmt.Errorf("error parsing YAML frontmatter: %w", err)
	}

	// I11 FIX: Parse timestamps from YAML, only set to now() if missing
	createdAt := time.Now()
	updatedAt := time.Now()

	if createdAtStr, ok := data["created_at"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			createdAt = parsed
		}
	}

	if updatedAtStr, ok := data["updated_at"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, updatedAtStr); err == nil {
			updatedAt = parsed
		}
	}

	// Extract fields with defaults
	prompt := &models.Prompt{
		Content:         strings.TrimSpace(promptContent),
		FrontmatterYAML: frontmatter,
		Scope:           scope,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}

	// Required fields
	if id, ok := data["id"].(string); ok {
		prompt.ID = id
	} else {
		return nil, fmt.Errorf("missing required field: id")
	}

	// Optional fields with defaults
	if trigger, ok := data["trigger"].(string); ok {
		prompt.Trigger = trigger
	}

	if confidence, ok := yamlFloat32(data["confidence"]); ok {
		prompt.Confidence = confidence
	} else {
		prompt.Confidence = 0.5 // Default confidence
	}

	if domain, ok := data["domain"].(string); ok {
		prompt.Domain = domain
	}

	if source, ok := data["source"].(string); ok {
		prompt.Source = source
	}

	if successRate, ok := yamlFloat32(data["success_rate"]); ok {
		prompt.SuccessRate = successRate
	}

	// Parse agents_tested as array
	if agentsTested, ok := data["agents_tested"].([]interface{}); ok {
		for _, agent := range agentsTested {
			if a, ok := agent.(string); ok {
				prompt.AgentsTested = append(prompt.AgentsTested, a)
			}
		}
	}

	// Parse promoted flag
	if promoted, ok := data["promoted"].(bool); ok {
		prompt.Promoted = promoted
	}

	return prompt, nil
}
