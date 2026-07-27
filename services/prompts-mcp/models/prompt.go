package models

import "time"

// Prompt represents a versioned, confidence-scored prompt
type Prompt struct {
	ID              string         `json:"id"`
	Trigger         string         `json:"trigger"`
	Confidence      float32        `json:"confidence"`
	Domain          string         `json:"domain"`
	Source          string         `json:"source"`
	Scope           string         `json:"scope"` // "project" or "global"
	Promoted        bool           `json:"promoted"` // true if promoted to registry
	AgentsTested    []string       `json:"agents_tested"`
	SuccessRate     float32        `json:"success_rate"`
	Content         string         `json:"content"`
	FrontmatterYAML string         `json:"frontmatter_yaml"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// Feedback represents an observation from an agent using a prompt
type Feedback struct {
	ID              string    `json:"id"`
	PromptID        string    `json:"prompt_id"`
	Agent           string    `json:"agent"`
	Task            string    `json:"task"`
	Success         bool      `json:"success"`
	ConfidenceUpdate float32  `json:"confidence_update"`
	Note            string    `json:"note"`
	Timestamp       time.Time `json:"timestamp"`
}

// ListRequest represents query parameters for listing prompts
type ListRequest struct {
	Domain string
	Scope  string // "project" or "global"
}

// SearchRequest represents query parameters for searching prompts
type SearchRequest struct {
	Query  string
	Domain string
}

// ExportRequest represents query parameters for exporting prompts
type ExportRequest struct {
	Format string // "yaml" or "json"
	Domain string
}
