package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
	"gopkg.in/yaml.v3"
)

// ValidatePromptPath is defined in validate.go (used by both import and save paths)
// B6 FIX: p.Domain and p.ID are attacker-controllable and must be validated
// before use in filesystem paths.

// buildFrontmatter renders a canonical frontmatter block from the prompt struct.
// Used both for brand-new prompts and as the recovery path when a file's
// existing frontmatter is unparseable.
func buildFrontmatter(p *models.Prompt, scope string) ([]byte, error) {
	fm := frontmatterData{
		ID:           p.ID,
		Domain:       p.Domain,
		Trigger:      p.Trigger,
		Confidence:   p.Confidence,
		Source:       p.Source,
		Scope:        scope,
		Promoted:     p.Promoted,
		SuccessRate:  p.SuccessRate,
		AgentsTested: p.AgentsTested,
		// I11 FIX: Persist timestamps
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
	return yaml.Marshal(fm)
}

// SavePrompt saves a prompt to disk in the appropriate directory.
//
// B1 FIX: acquires the corpus write lock so a bare save cannot interleave with
// an in-flight UpdateConfidence read-modify-write cycle. Uses the default
// timeout; callers needing their own deadline should use SavePromptContext.
func (pl *PromptLoader) SavePrompt(p *models.Prompt, scope string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultLoadTimeout)
	defer cancel()
	return pl.SavePromptContext(ctx, p, scope)
}

// SavePromptContext is SavePrompt with a caller-supplied deadline.
func (pl *PromptLoader) SavePromptContext(ctx context.Context, p *models.Prompt, scope string) error {
	if err := pl.acquireWrite(ctx); err != nil {
		return err
	}
	defer pl.releaseWrite()
	return pl.savePromptLocked(p, scope)
}

// savePromptLocked performs the actual write. Caller must hold the corpus
// write lock (see writeLock/acquireWrite).
func (pl *PromptLoader) savePromptLocked(p *models.Prompt, scope string) error {
	// B6 FIX: p.Domain and p.ID are attacker-controllable (import endpoint) and
	// are joined into a filesystem path below. Reject anything that is not a
	// plain path segment BEFORE touching the filesystem.
	if err := ValidatePromptPath(p.ID, p.Domain); err != nil {
		return err
	}

	// Determine base directory based on scope
	var baseScope string
	if scope == "project" {
		baseScope = "personal"
	} else {
		baseScope = "inherited"
	}

	// Create the domain directory if it doesn't exist
	domainDir := filepath.Join(pl.baseDir, baseScope, p.Domain)
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		return fmt.Errorf("error creating domain directory: %w", err)
	}

	// Filename based on prompt ID
	filename := filepath.Join(domainDir, p.ID+".yaml")

	// I5 FIX: Preserve original frontmatter if available, only update changed fields.
	// CORRUPTION RECOVERY: if the original frontmatter is not valid YAML (or is
	// valid YAML but not a mapping, e.g. a bare scalar or list), we must NOT write
	// it back out — doing so persists the corruption forever and makes the file
	// unloadable. In that case we discard it and rebuild a canonical block from
	// the in-memory prompt, which is fully populated by the caller.
	var frontmatterBytes []byte
	var err error

	rebuildFrontmatter := true

	if p.FrontmatterYAML != "" {
		// Merge updates into original frontmatter to preserve any custom fields
		originalFM := make(map[string]interface{})
		if unmarshalErr := yaml.Unmarshal([]byte(p.FrontmatterYAML), &originalFM); unmarshalErr == nil {
			// Update only the fields that changed
			originalFM["id"] = p.ID
			originalFM["domain"] = p.Domain
			originalFM["trigger"] = p.Trigger
			originalFM["confidence"] = p.Confidence
			originalFM["source"] = p.Source
			originalFM["scope"] = scope
			originalFM["promoted"] = p.Promoted
			originalFM["success_rate"] = p.SuccessRate
			originalFM["agents_tested"] = p.AgentsTested
			// I11 FIX: Persist timestamps
			originalFM["created_at"] = p.CreatedAt.Format(time.RFC3339)
			originalFM["updated_at"] = p.UpdatedAt.Format(time.RFC3339)

			frontmatterBytes, err = yaml.Marshal(originalFM)
			if err == nil {
				rebuildFrontmatter = false
			} else {
				fmt.Fprintf(os.Stderr, "Warning: re-marshaling frontmatter for %s failed (%v); rebuilding from scratch\n", p.ID, err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Warning: corrupted frontmatter for %s (%v); rebuilding from scratch\n", p.ID, unmarshalErr)
		}
	}

	if rebuildFrontmatter {
		frontmatterBytes, err = buildFrontmatter(p, scope)
		if err != nil {
			return fmt.Errorf("error marshaling frontmatter: %w", err)
		}
	}

	// Create file content: ---\nfrontmatter\n---\ncontent
	yamlContent := fmt.Sprintf("---\n%s---\n%s\n", string(frontmatterBytes), p.Content)

	// Write to temp file first, then atomic rename (C6: prevent partial writes)
	tmpFile := filename + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("error writing temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpFile, filename); err != nil {
		os.Remove(tmpFile) // Best effort cleanup
		return fmt.Errorf("error finalizing prompt file: %w", err)
	}

	// PERF FIX: bust the LoadAll cache so this write is immediately visible
	// (read-your-writes). Must happen after the rename succeeds, never before.
	pl.InvalidateCache()

	return nil
}
