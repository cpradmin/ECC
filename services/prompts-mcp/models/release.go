package models

import (
	"time"
)

// Release represents a published registry snapshot (GitHub release)
type Release struct {
	Version      string    `json:"version"`      // e.g., v0.1.0, v0.2.0
	ReleasedAt   time.Time `json:"released_at"`
	PromptCount  int       `json:"prompt_count"`
	Prompts      []Prompt  `json:"prompts"`      // All prompts in this release
	Changelog    string    `json:"changelog"`
	AvgConfidence float32  `json:"avg_confidence"`
	AvgSuccessRate float32 `json:"avg_success_rate"`
}

// ReleaseManifest is what gets published to GitHub
type ReleaseManifest struct {
	Version        string                 `json:"version"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	ReleasedAt     time.Time              `json:"released_at"`
	PromptCount    int                    `json:"prompt_count"`
	DomainCounts   map[string]int         `json:"domain_counts"`
	AvgConfidence  float32                `json:"avg_confidence"`
	AvgSuccessRate float32                `json:"avg_success_rate"`
	Changelog      string                 `json:"changelog"`
	HighlightedPrompts []HighlightedPrompt `json:"highlighted_prompts"`
}

// HighlightedPrompt is a featured prompt in the release
type HighlightedPrompt struct {
	ID         string  `json:"id"`
	Domain     string  `json:"domain"`
	Confidence float32 `json:"confidence"`
	Summary    string  `json:"summary"`
}
