package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// UpdateConfidence atomically applies delta to a prompt's confidence. (B1 fix)
//
// This is the primitive that replaces the load / modify / save sequence
// FeedbackManager used to open-code. The entire cycle runs under the corpus
// write lock, so concurrent callers serialise and every delta lands.
//
// The read deliberately bypasses the TTL cache (loadAllUncached): a cached
// snapshot may predate a write by up to the TTL, and applying a delta to a
// stale base confidence is exactly the lost update this fix exists to prevent.
//
// The new confidence is clamped to [0,1]. Returns ErrPromptNotFound if the ID
// is unknown, so callers can answer 4xx rather than silently no-op — the old
// code discarded that error and reported success.
func (pl *PromptLoader) UpdateConfidence(ctx context.Context, id string, delta float32) (*models.Prompt, error) {
	if err := pl.acquireWrite(ctx); err != nil {
		return nil, err
	}
	defer pl.releaseWrite()

	// Re-check the deadline: we may have queued behind other writers.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context expired before update: %w", err)
	}

	prompts, err := pl.loadAllUncached()
	if err != nil {
		return nil, fmt.Errorf("error loading prompts for update: %w", err)
	}

	var target *models.Prompt
	for i := range prompts {
		if prompts[i].ID == id {
			target = &prompts[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: %s", ErrPromptNotFound, id)
	}

	newConfidence := target.Confidence + delta
	if newConfidence < 0 {
		newConfidence = 0
	}
	if newConfidence > 1 {
		newConfidence = 1
	}
	target.Confidence = newConfidence
	target.UpdatedAt = time.Now().UTC()

	if err := pl.savePromptLocked(target, target.Scope); err != nil {
		return nil, fmt.Errorf("error persisting confidence update: %w", err)
	}

	updated := *target
	return &updated, nil
}
