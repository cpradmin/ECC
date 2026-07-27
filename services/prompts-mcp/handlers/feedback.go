package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
	"github.com/google/uuid"
)

// ErrInvalidFeedback marks a malformed feedback payload. Handlers map it to 400.
var ErrInvalidFeedback = errors.New("invalid feedback")

// maxFeedbackTextLen bounds the free-text fields an agent can submit, so the
// append-only ledger cannot be inflated by a single request.
const maxFeedbackTextLen = 4096

// FeedbackManager handles feedback logging and confidence evolution
type FeedbackManager struct {
	feedbackDir   string
	trinityWriter *TrinityWriter

	// loader is shared with the rest of the process so confidence updates
	// serialise against every other writer. Previously SubmitFeedback built a
	// throwaway loader per call, pointed at a hardcoded $HOME path — which both
	// defeated locking and ignored the configured data home entirely.
	loader *PromptLoader

	// logMu serialises appends to feedback.jsonl. O_APPEND is only atomic for
	// writes under PIPE_BUF; a fat Note field can exceed it and interleave two
	// records into one corrupt line.
	logMu sync.Mutex

	// onChange fires after a successful submission so caches (metrics) can be
	// invalidated. Registered at construction; never mutated afterwards.
	onChange []func()

	// validatePromptExists gates the B5 existence check. Always on in
	// production; tests that exercise pure ledger mechanics can disable it.
	validatePromptExists bool

	timeout time.Duration
}

// NewFeedbackManager creates a new FeedbackManager with its own loader.
func NewFeedbackManager(dataHome string) *FeedbackManager {
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local/share")
	}
	return NewFeedbackManagerWithLoader(dataHome, NewPromptLoader(dataHome))
}

// NewFeedbackManagerWithLoader wires the manager to an existing loader so all
// writers in the process share one lock and one cache. Preferred constructor.
func NewFeedbackManagerWithLoader(dataHome string, loader *PromptLoader) *FeedbackManager {
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local/share")
	}
	if loader == nil {
		loader = NewPromptLoader(dataHome)
	}
	return &FeedbackManager{
		feedbackDir:          filepath.Join(dataHome, "ecc-prompts", "projects", "ember-swarm"),
		trinityWriter:        NewTrinityWriter(dataHome),
		loader:               loader,
		validatePromptExists: true,
		timeout:              DefaultLoadTimeout,
	}
}

// OnChange registers a callback fired after each successful submission.
func (fm *FeedbackManager) OnChange(fn func()) {
	if fn != nil {
		fm.onChange = append(fm.onChange, fn)
	}
}

func (fm *FeedbackManager) notifyChange() {
	for _, fn := range fm.onChange {
		fn()
	}
}

// SetValidatePromptExists toggles the B5 existence check. Test-only.
func (fm *FeedbackManager) SetValidatePromptExists(v bool) { fm.validatePromptExists = v }

// ValidateFeedback checks a payload before any side effect. (B5 fix)
//
// The prompt_id is run through the same path-segment validator SavePrompt uses:
// the ID reaches the filesystem as a filename, so accepting a looser character
// set here than there would just move the traversal problem one layer up.
func ValidateFeedback(fb *models.Feedback) error {
	if fb == nil {
		return fmt.Errorf("%w: nil payload", ErrInvalidFeedback)
	}
	if fb.PromptID == "" {
		return fmt.Errorf("%w: prompt_id is required", ErrInvalidFeedback)
	}
	if err := validatePathSegment("prompt_id", fb.PromptID); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidFeedback, err)
	}
	// A single observation may never swing confidence across the whole range.
	if fb.ConfidenceUpdate < -1 || fb.ConfidenceUpdate > 1 {
		return fmt.Errorf("%w: confidence_update %.4f out of range [-1,1]",
			ErrInvalidFeedback, fb.ConfidenceUpdate)
	}
	for field, value := range map[string]string{
		"agent": fb.Agent, "task": fb.Task, "note": fb.Note,
	} {
		if len(value) > maxFeedbackTextLen {
			return fmt.Errorf("%w: %s exceeds %d characters",
				ErrInvalidFeedback, field, maxFeedbackTextLen)
		}
	}
	return nil
}

// SubmitFeedback logs an observation and atomically updates prompt confidence.
func (fm *FeedbackManager) SubmitFeedback(feedback *models.Feedback) error {
	return fm.SubmitFeedbackContext(context.Background(), feedback)
}

// SubmitFeedbackContext is SubmitFeedback with a caller-supplied deadline.
//
// Ordering is deliberate — validate, append to the ledger, then update the
// derived score:
//
//   - Validation runs first so a bad payload produces no side effect at all
//     (B5). In particular an unknown prompt_id is rejected before anything is
//     written, instead of being silently logged against a prompt that will
//     never exist.
//   - The append is the durable record. feedback.jsonl is the ledger that
//     EvolveConfidence and CalculateSuccessRate replay, so it is the source of
//     truth and must land first.
//   - The confidence write is derived state, applied through the loader's
//     atomic read-modify-write (B1). If it fails we surface the error, but the
//     observation stays in the ledger and can be reconciled — the reverse
//     order would let a confidence change exist with no audit trail.
func (fm *FeedbackManager) SubmitFeedbackContext(ctx context.Context, feedback *models.Feedback) error {
	// Always impose our own ceiling. An *http.Request context carries no
	// deadline of its own (the server's WriteTimeout does not propagate into
	// one), so relying on the caller's context would leave the filesystem work
	// unbounded. WithTimeout keeps whichever deadline is earlier.
	ctx, cancel := context.WithTimeout(ctx, fm.effectiveTimeout())
	defer cancel()

	if err := ValidateFeedback(feedback); err != nil {
		return err
	}

	// B5 FIX: refuse feedback for prompts that do not exist. Without this an
	// unauthenticated caller could inflate the ledger with records for
	// arbitrary IDs, skewing every aggregate metric derived from it.
	if fm.validatePromptExists {
		exists, err := fm.loader.PromptExists(ctx, feedback.PromptID)
		if err != nil {
			return fmt.Errorf("error validating prompt_id: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: %s", ErrPromptNotFound, feedback.PromptID)
		}
	}

	if err := os.MkdirAll(fm.feedbackDir, 0750); err != nil {
		return fmt.Errorf("error creating feedback directory: %w", err)
	}

	if feedback.ID == "" {
		feedback.ID = uuid.New().String()
	}
	if feedback.Timestamp.IsZero() {
		feedback.Timestamp = time.Now().UTC()
	}

	if err := fm.appendToLedger(feedback); err != nil {
		return err
	}

	// B1 FIX: one atomic load-modify-write under the corpus lock, replacing the
	// old LoadByID -> mutate -> SavePrompt sequence in which two concurrent
	// submissions both read the same confidence and the later write erased the
	// earlier delta.
	if feedback.ConfidenceUpdate != 0 {
		if _, err := fm.loader.UpdateConfidence(ctx, feedback.PromptID, feedback.ConfidenceUpdate); err != nil {
			return fmt.Errorf("feedback recorded but confidence update failed: %w", err)
		}
	}

	// Trinity logging is best effort: it is an external sink, not our ledger.
	if fm.trinityWriter != nil {
		if err := fm.trinityWriter.LogFeedbackObservation(feedback); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to log feedback to Trinity: %v\n", err)
		}
	}

	fm.notifyChange()
	return nil
}

// appendToLedger writes one JSON line, serialised against other writers.
func (fm *FeedbackManager) appendToLedger(feedback *models.Feedback) error {
	fm.logMu.Lock()
	defer fm.logMu.Unlock()

	feedbackFile := filepath.Join(fm.feedbackDir, "feedback.jsonl")
	f, err := os.OpenFile(feedbackFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("error opening feedback file: %w", err)
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(feedback); err != nil {
		return fmt.Errorf("error writing feedback: %w", err)
	}
	return nil
}

func (fm *FeedbackManager) effectiveTimeout() time.Duration {
	if fm.timeout <= 0 {
		return DefaultLoadTimeout
	}
	return fm.timeout
}

// ConfidenceUpdate represents a confidence score change
type ConfidenceUpdate struct {
	PromptID      string
	OldConfidence float32
	NewConfidence float32
	Delta         float32
	Reason        string
	Timestamp     time.Time
}

// LoadFeedbackLog loads all feedback entries for a prompt
func (fm *FeedbackManager) LoadFeedbackLog(promptID string) ([]models.Feedback, error) {
	var feedbacks []models.Feedback

	feedbackFile := filepath.Join(fm.feedbackDir, "feedback.jsonl")
	f, err := os.Open(feedbackFile)
	if err != nil {
		if os.IsNotExist(err) {
			return feedbacks, nil
		}
		return nil, fmt.Errorf("error opening feedback file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var feedback models.Feedback
		if err := json.Unmarshal(scanner.Bytes(), &feedback); err != nil {
			// Log error but continue processing
			fmt.Fprintf(os.Stderr, "Error parsing feedback entry: %v\n", err)
			continue
		}

		if feedback.PromptID == promptID {
			feedbacks = append(feedbacks, feedback)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading feedback file: %w", err)
	}

	return feedbacks, nil
}

// EvolveConfidence calculates new confidence based on feedback
func (fm *FeedbackManager) EvolveConfidence(prompt *models.Prompt) (*ConfidenceUpdate, error) {
	feedbacks, err := fm.LoadFeedbackLog(prompt.ID)
	if err != nil {
		return nil, err
	}

	oldConfidence := prompt.Confidence
	newConfidence := oldConfidence

	// Count successes and failures
	successes := 0
	failures := 0
	for _, f := range feedbacks {
		if f.Success {
			successes++
			newConfidence += 0.05 // +0.05 per success
		} else {
			failures++
			newConfidence -= 0.10 // -0.10 per failure
		}
	}

	// Clamp confidence to valid range
	if newConfidence < 0.3 {
		newConfidence = 0.3
	}
	if newConfidence > 0.95 {
		newConfidence = 0.95
	}

	delta := newConfidence - oldConfidence
	reason := fmt.Sprintf("feedback: %d successes, %d failures", successes, failures)

	return &ConfidenceUpdate{
		PromptID:      prompt.ID,
		OldConfidence: oldConfidence,
		NewConfidence: newConfidence,
		Delta:         delta,
		Reason:        reason,
		Timestamp:     time.Now().UTC(),
	}, nil
}

// CheckPromotion determines if a prompt should be promoted to global scope
func (fm *FeedbackManager) CheckPromotion(prompt *models.Prompt) (bool, error) {
	// Only check project-scoped prompts
	if prompt.Scope != "project" {
		return false, nil
	}

	// Need 0.8+ confidence and 3+ agents tested
	if prompt.Confidence < 0.8 {
		return false, nil
	}

	if len(prompt.AgentsTested) < 3 {
		return false, nil
	}

	// Promotion criteria met
	return true, nil
}

// LogPromotion logs a prompt promotion to Trinity
func (fm *FeedbackManager) LogPromotion(prompt *models.Prompt) error {
	if fm.trinityWriter == nil {
		return nil // Trinity not configured
	}
	return fm.trinityWriter.LogPromptPromotion(prompt)
}

// UpdatePromptConfidence updates a prompt's confidence in place
// This is a helper that would be called by the loader after evolution
func (fm *FeedbackManager) UpdatePromptConfidence(prompt *models.Prompt) error {
	update, err := fm.EvolveConfidence(prompt)
	if err != nil {
		return err
	}

	// Update the prompt's confidence
	prompt.Confidence = update.NewConfidence
	prompt.UpdatedAt = time.Now().UTC()

	return nil
}

// CalculateSuccessRate recalculates success rate from feedback
func (fm *FeedbackManager) CalculateSuccessRate(promptID string) (float32, error) {
	feedbacks, err := fm.LoadFeedbackLog(promptID)
	if err != nil {
		return 0, err
	}

	if len(feedbacks) == 0 {
		return 0, nil
	}

	successes := 0
	for _, f := range feedbacks {
		if f.Success {
			successes++
		}
	}

	return float32(successes) / float32(len(feedbacks)), nil
}

// LoadAllContext is LoadAll with a deadline. (B3 fix)
//
// feedback.jsonl is append-only and unbounded, so this scan grows without limit
// — it is the single most likely operation to blow a scrape budget. Same
// abandon-on-timeout pattern as PromptLoader.LoadAllContext, and safe for the
// same reason: the scan is read-only.
func (fm *FeedbackManager) LoadAllContext(ctx context.Context) ([]models.Feedback, error) {
	type result struct {
		feedback []models.Feedback
		err      error
	}
	ch := make(chan result, 1)

	go func() {
		fb, err := fm.LoadAll()
		ch <- result{fb, err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout loading feedback: %w", ctx.Err())
	case res := <-ch:
		return res.feedback, res.err
	}
}

// LoadAll loads all feedback records from the feedback log
func (fm *FeedbackManager) LoadAll() ([]models.Feedback, error) {
	feedbackFile := filepath.Join(fm.feedbackDir, "feedback.jsonl")

	// If file doesn't exist, return empty slice
	if _, err := os.Stat(feedbackFile); os.IsNotExist(err) {
		return []models.Feedback{}, nil
	}

	file, err := os.Open(feedbackFile)
	if err != nil {
		return nil, fmt.Errorf("error opening feedback file: %w", err)
	}
	defer file.Close()

	var feedbacks []models.Feedback
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var fb models.Feedback
		if err := json.Unmarshal(scanner.Bytes(), &fb); err != nil {
			// Skip malformed lines
			continue
		}
		feedbacks = append(feedbacks, fb)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading feedback file: %w", err)
	}

	return feedbacks, nil
}
