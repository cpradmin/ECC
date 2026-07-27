package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// ErrPromptNotFound is returned when a prompt ID does not exist on disk.
// Handlers map this to a 4xx rather than a 500 — it is a client error.
var ErrPromptNotFound = errors.New("prompt not found")

// LoadAll returns all prompts, served from a TTL cache (see DefaultCacheTTL).
//
// The returned slice is a copy, so callers may append to or reorder it without
// corrupting the shared cache entry.
func (pl *PromptLoader) LoadAll() ([]models.Prompt, error) {
	if cached, ok := pl.cachedPrompts(); ok {
		pl.cache.hits.Add(1)
		return cached, nil
	}
	pl.cache.misses.Add(1)

	prompts, err := pl.loadAllUncached()
	if err != nil {
		return nil, err
	}

	pl.cache.mu.Lock()
	if pl.cache.ttl > 0 {
		pl.cache.prompts = prompts
		pl.cache.loadedAt = time.Now()
		pl.cache.valid = true
	}
	pl.cache.mu.Unlock()

	return copyPrompts(prompts), nil
}

// LoadAllContext is LoadAll with a caller-supplied deadline. (B3 fix)
//
// The walk runs on its own goroutine so a stalled filesystem cannot pin the
// caller: on timeout we abandon the goroutine and return. Abandoning is safe
// precisely because this path is read-only — it has no side effects to half
// apply — and the buffered channel means the orphan never blocks on send. The
// goroutine ends when the filesystem finally answers.
func (pl *PromptLoader) LoadAllContext(ctx context.Context) ([]models.Prompt, error) {
	type result struct {
		prompts []models.Prompt
		err     error
	}
	ch := make(chan result, 1)

	go func() {
		prompts, err := pl.LoadAll()
		ch <- result{prompts, err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout loading prompts: %w", ctx.Err())
	case res := <-ch:
		return res.prompts, res.err
	}
}

// LoadByID loads a single prompt by ID
func (pl *PromptLoader) LoadByID(id string) (*models.Prompt, error) {
	all, err := pl.LoadAll()
	if err != nil {
		return nil, err
	}

	for i := range all {
		if all[i].ID == id {
			p := all[i]
			return &p, nil
		}
	}
	// B5: wrap the sentinel so callers can errors.Is() this into a 4xx.
	return nil, fmt.Errorf("%w: %s", ErrPromptNotFound, id)
}

// LoadByIDContext resolves one prompt under a deadline, returning
// ErrPromptNotFound when the ID is unknown.
func (pl *PromptLoader) LoadByIDContext(ctx context.Context, id string) (*models.Prompt, error) {
	all, err := pl.LoadAllContext(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			p := all[i]
			return &p, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrPromptNotFound, id)
}

// LoadByDomain loads prompts filtered by domain
func (pl *PromptLoader) LoadByDomain(domain string) ([]models.Prompt, error) {
	all, err := pl.LoadAll()
	if err != nil {
		return nil, err
	}

	var filtered []models.Prompt
	for _, p := range all {
		if p.Domain == domain {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

// LoadByScope loads prompts filtered by scope (project or global)
func (pl *PromptLoader) LoadByScope(scope string) ([]models.Prompt, error) {
	all, err := pl.LoadAll()
	if err != nil {
		return nil, err
	}

	var filtered []models.Prompt
	for _, p := range all {
		if p.Scope == scope {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

// Search searches prompts by keyword
func (pl *PromptLoader) Search(query string, domain string) ([]models.Prompt, error) {
	all, err := pl.LoadAll()
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var results []models.Prompt

	for _, p := range all {
		// Filter by domain if specified
		if domain != "" && p.Domain != domain {
			continue
		}

		// Search in ID, trigger, and content
		if strings.Contains(strings.ToLower(p.ID), query) ||
			strings.Contains(strings.ToLower(p.Trigger), query) ||
			strings.Contains(strings.ToLower(p.Content), query) {
			results = append(results, p)
		}
	}

	return results, nil
}

// PromptExists reports whether a prompt ID is present in the corpus. (B5 fix)
func (pl *PromptLoader) PromptExists(ctx context.Context, id string) (bool, error) {
	_, err := pl.LoadByIDContext(ctx, id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrPromptNotFound) {
		return false, nil
	}
	return false, err
}
