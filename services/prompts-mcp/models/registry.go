package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RegistryIndexVersion is the schema version written into every registry index.
// LoadIndex rejects (and triggers a rebuild of) any file that does not match.
// Bump this whenever RegistryIndex, RegistryEntry, or DomainStats change shape.
const RegistryIndexVersion = "1.0"

// RegistryEntry represents a prompt in the registry with version metadata
type RegistryEntry struct {
	ID              string    `json:"id"`
	Domain          string    `json:"domain"`
	CurrentVersion  string    `json:"current_version"`
	Confidence      float32   `json:"confidence"`
	SuccessRate     float32   `json:"success_rate"`
	AgentsTested    []string  `json:"agents_tested"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Versions        []string  `json:"versions"`
	RegistryStatus  string    `json:"registry_status"` // active, experimental, deprecated
	Promoted        bool      `json:"promoted"`        // true if promoted to registry
}

// DomainStats represents statistics for a domain in the registry
type DomainStats struct {
	Count              int     `json:"count"`
	LatestVersion      string  `json:"latest_version"`
	AverageConfidence  float32 `json:"average_confidence"`
	LatestSuccessRate  float32 `json:"latest_success_rate"`
}

// RegistryIndex represents the complete registry index
type RegistryIndex struct {
	Version     string                 `json:"version"`
	GeneratedAt time.Time              `json:"generated_at"`
	TotalPrompts int                    `json:"total_prompts"`
	Domains     map[string]DomainStats `json:"domains"`
	Prompts     []RegistryEntry        `json:"prompts"`
}

// RegistryBuilder builds and manages the registry index
type RegistryBuilder struct {
	registryDir string
	indexFile   string
}

// NewRegistryBuilder creates a new registry builder
func NewRegistryBuilder(dataHome string) *RegistryBuilder {
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local/share")
	}
	registryDir := filepath.Join(dataHome, "ecc-prompts", "registry")
	return &RegistryBuilder{
		registryDir: registryDir,
		indexFile:   filepath.Join(registryDir, "registry-index.json"),
	}
}

// BuildIndex builds the registry index from loaded prompts
// Includes only prompts with confidence >= minConfidence (typically 0.8)
func (rb *RegistryBuilder) BuildIndex(prompts []Prompt, minConfidence float32) (*RegistryIndex, error) {
	// Create registry directory if needed
	if err := os.MkdirAll(rb.registryDir, 0755); err != nil {
		return nil, fmt.Errorf("error creating registry directory: %w", err)
	}

	index := &RegistryIndex{
		Version:     RegistryIndexVersion,
		GeneratedAt: time.Now().UTC(),
		Domains:     make(map[string]DomainStats),
		Prompts:     []RegistryEntry{},
	}

	// Filter prompts with sufficient confidence
	domainMap := make(map[string][]RegistryEntry)
	for _, p := range prompts {
		if p.Confidence < minConfidence {
			continue // Skip low-confidence prompts
		}

		entry := RegistryEntry{
			ID:             p.ID,
			Domain:         p.Domain,
			CurrentVersion: rb.inferVersion(p.ID, p.UpdatedAt),
			Confidence:     p.Confidence,
			SuccessRate:    p.SuccessRate,
			AgentsTested:   p.AgentsTested,
			CreatedAt:      p.CreatedAt,
			UpdatedAt:      p.UpdatedAt,
			Versions:       []string{rb.inferVersion(p.ID, p.UpdatedAt)},
			Promoted:       p.Promoted,
			RegistryStatus: deriveStatus(p.Promoted),
		}

		index.Prompts = append(index.Prompts, entry)
		domainMap[p.Domain] = append(domainMap[p.Domain], entry)
	}

	// Calculate domain statistics
	for domain, entries := range domainMap {
		if len(entries) == 0 {
			continue
		}

		// Calculate average confidence
		var sumConfidence float32
		var sumSuccessRate float32
		for _, e := range entries {
			sumConfidence += e.Confidence
			sumSuccessRate += e.SuccessRate
		}

		stats := DomainStats{
			Count:              len(entries),
			LatestVersion:      entries[0].CurrentVersion,
			AverageConfidence:  sumConfidence / float32(len(entries)),
			LatestSuccessRate:  sumSuccessRate / float32(len(entries)),
		}
		index.Domains[domain] = stats
	}

	index.TotalPrompts = len(index.Prompts)

	// Sort prompts by confidence (descending)
	sort.Slice(index.Prompts, func(i, j int) bool {
		return index.Prompts[i].Confidence > index.Prompts[j].Confidence
	})

	return index, nil
}

// SaveIndex writes the registry index to disk
func (rb *RegistryBuilder) SaveIndex(index *RegistryIndex) error {
	// Create registry directory if needed
	if err := os.MkdirAll(rb.registryDir, 0755); err != nil {
		return fmt.Errorf("error creating registry directory: %w", err)
	}

	// Marshal index to JSON
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling index: %w", err)
	}

	// Write to file atomically (write to temp, then rename)
	tmpFile := rb.indexFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("error writing temp index: %w", err)
	}

	if err := os.Rename(tmpFile, rb.indexFile); err != nil {
		_ = os.Remove(tmpFile) // best effort cleanup
		return fmt.Errorf("error saving index: %w", err)
	}

	return nil
}

// LoadIndex reads the registry index from disk.
//
// It returns (nil, nil) whenever the on-disk index is unusable — absent,
// unparseable, or written by a different schema version. That is the caller's
// signal to rebuild from the prompt corpus, which is always authoritative.
// Returning an error instead would wedge the server permanently on a single
// bad file, and silently accepting a foreign version would surface fields that
// no longer mean what the struct says they mean.
func (rb *RegistryBuilder) LoadIndex() (*RegistryIndex, error) {
	data, err := os.ReadFile(rb.indexFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Index doesn't exist yet
		}
		return nil, fmt.Errorf("error reading index: %w", err)
	}

	var index RegistryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: registry index %s is corrupt (%v); rebuilding\n", rb.indexFile, err)
		return nil, nil
	}

	// Version gate: a mismatch means the file was produced by different code.
	if index.Version != RegistryIndexVersion {
		fmt.Fprintf(os.Stderr,
			"Warning: registry index %s has version %q, want %q; rebuilding\n",
			rb.indexFile, index.Version, RegistryIndexVersion)
		return nil, nil
	}

	return &index, nil
}

// inferVersion infers a semantic version based on prompt ID and timestamp
// Format: 0.{day}.{hour} where day is day of month, hour is UTC hour
// This creates unique but somewhat meaningful versions
func (rb *RegistryBuilder) inferVersion(promptID string, updatedAt time.Time) string {
	day := updatedAt.Day()
	hour := updatedAt.Hour()
	return fmt.Sprintf("0.%d.%d", day, hour)
}

// GetEntryByID retrieves a specific registry entry
func (rb *RegistryBuilder) GetEntryByID(index *RegistryIndex, promptID string) *RegistryEntry {
	if index == nil {
		return nil
	}
	for i := range index.Prompts {
		if index.Prompts[i].ID == promptID {
			return &index.Prompts[i]
		}
	}
	return nil
}

// FilterByDomain filters registry entries by domain
func FilterByDomain(entries []RegistryEntry, domain string) []RegistryEntry {
	if domain == "" {
		return entries
	}
	var filtered []RegistryEntry
	for _, e := range entries {
		if e.Domain == domain {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// FilterByConfidence filters registry entries by minimum confidence
func FilterByConfidence(entries []RegistryEntry, minConfidence float32) []RegistryEntry {
	var filtered []RegistryEntry
	for _, e := range entries {
		if e.Confidence >= minConfidence {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// deriveStatus derives the registry status from the promoted flag
func deriveStatus(promoted bool) string {
	if promoted {
		return "promoted"
	}
	return "active"
}
