package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func seedPhase4Prompt(t testing.TB, loader *PromptLoader, id string, confidence float32) {
	t.Helper()
	now := time.Now().UTC()
	p := models.Prompt{
		ID:         id,
		Domain:     "router-prompts",
		Scope:      "project",
		Confidence: confidence,
		Content:    "seeded content for " + id,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := loader.SavePrompt(&p, p.Scope); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func countLedgerLines(t testing.TB, dataHome string) int {
	t.Helper()
	path := filepath.Join(dataHome, "ecc-prompts", "projects", "ember-swarm", "feedback.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return len(strings.Split(strings.TrimSpace(string(raw)), "\n"))
}

// ---------------------------------------------------------------------------
// B1 — race in SubmitFeedback
// ---------------------------------------------------------------------------

// TestConcurrentFeedback is the B1 regression. Run with -race.
//
// 100 goroutines each apply +0.005 to a prompt that starts at 0.0, so a correct
// implementation lands at exactly 0.5. The pre-fix code did
// LoadByID -> mutate -> SavePrompt with no lock, so writers read the same base
// confidence and the last write erased the others; it converged near 0.005
// rather than 0.5.
//
// The assertion is on the *sum*, not merely on "no data race": a mutex that
// serialises the writes but still reads a stale cached snapshot would pass the
// race detector and fail this.
func TestConcurrentFeedback(t *testing.T) {
	const goroutines = 100
	const delta = float32(0.005)
	const promptID = "concurrent-feedback-01"

	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase4Prompt(t, loader, promptID, 0.0)

	fm := NewFeedbackManagerWithLoader(dir, loader)

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start // maximise contention: release all at once
			fb := &models.Feedback{
				PromptID:         promptID,
				Agent:            fmt.Sprintf("agent-%03d", n),
				Task:             "concurrency-test",
				Success:          true,
				ConfidenceUpdate: delta,
			}
			if err := fm.SubmitFeedback(fb); err != nil {
				errCh <- err
			}
		}(i)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("SubmitFeedback: %v", err)
	}

	final, err := loader.LoadByID(promptID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	want := delta * goroutines // 0.5
	const tolerance = 0.01     // float32 round-trips through YAML each cycle
	if diff := final.Confidence - want; diff > tolerance || diff < -tolerance {
		t.Errorf("lost updates: confidence = %.4f, want %.4f (+/-%.2f). "+
			"%d of %d deltas were dropped",
			final.Confidence, want, tolerance,
			int((want-final.Confidence)/delta), goroutines)
	}

	// Every observation must also appear in the ledger exactly once.
	if got := countLedgerLines(t, dir); got != goroutines {
		t.Errorf("ledger has %d records, want %d", got, goroutines)
	}
}

// TestConcurrentFeedbackClampsAtBounds proves serialisation still respects the
// [0,1] clamp under contention — 200 increments of +0.05 from 0.5 must saturate
// at exactly 1.0, never overshoot.
func TestConcurrentFeedbackClampsAtBounds(t *testing.T) {
	const promptID = "clamp-under-load"
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase4Prompt(t, loader, promptID, 0.5)
	fm := NewFeedbackManagerWithLoader(dir, loader)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = fm.SubmitFeedback(&models.Feedback{
				PromptID: promptID, Agent: "clamp", Success: true, ConfidenceUpdate: 0.05,
			})
		}()
	}
	wg.Wait()

	final, err := loader.LoadByID(promptID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if final.Confidence != 1.0 {
		t.Errorf("confidence = %.4f, want exactly 1.0 (clamped)", final.Confidence)
	}
}

// TestConcurrentMixedWriters runs feedback updates against direct SavePrompt
// calls. Both take the same corpus lock, so no write may be torn or lost.
func TestConcurrentMixedWriters(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 5; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("mixed-%02d", i), 0.5)
	}
	fm := NewFeedbackManagerWithLoader(dir, loader)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("mixed-%02d", n%5)
			if n%2 == 0 {
				_ = fm.SubmitFeedback(&models.Feedback{
					PromptID: id, Agent: "mixed", Success: true, ConfidenceUpdate: 0.01,
				})
				return
			}
			p, err := loader.LoadByID(id)
			if err != nil {
				return
			}
			p.Trigger = fmt.Sprintf("rewritten-%d", n)
			_ = loader.SavePrompt(p, p.Scope)
		}(i)
	}
	wg.Wait()

	// Every prompt must still be parseable — a torn write would fail to load.
	all, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after mixed writes: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("corpus has %d prompts, want 5 (torn or lost file)", len(all))
	}
}

// ---------------------------------------------------------------------------
// B5 — prompt_id validation
// ---------------------------------------------------------------------------

func TestFeedbackRejectsUnknownPrompt(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(dir)

	body, _ := json.Marshal(map[string]any{
		"prompt_id":         "does-not-exist-anywhere",
		"agent":             "attacker",
		"success":           true,
		"confidence_update": 0.5,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/feedback", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.SubmitFeedback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	// Fail closed: nothing may have been written to the ledger.
	ledger := filepath.Join(dir, "ecc-prompts", "projects", "ember-swarm", "feedback.jsonl")
	if _, err := os.Stat(ledger); err == nil {
		t.Error("feedback for a nonexistent prompt was persisted")
	}
}

func TestFeedbackRejectsInvalidPayloads(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase4Prompt(t, loader, "valid-prompt", 0.5)
	h := NewHandler(dir)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty prompt_id", map[string]any{"prompt_id": ""}},
		{"path traversal id", map[string]any{"prompt_id": "../../../etc/passwd"}},
		{"slash in id", map[string]any{"prompt_id": "router/evil"}},
		{"null byte in id", map[string]any{"prompt_id": "valid\x00prompt"}},
		{"confidence above range", map[string]any{"prompt_id": "valid-prompt", "confidence_update": 5.0}},
		{"confidence below range", map[string]any{"prompt_id": "valid-prompt", "confidence_update": -9.0}},
		{"oversize note", map[string]any{"prompt_id": "valid-prompt", "note": strings.Repeat("x", maxFeedbackTextLen+1)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/feedback", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			h.SubmitFeedback(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestFeedbackAcceptsValidPrompt(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase4Prompt(t, loader, "accepted-prompt", 0.5)
	h := NewHandler(dir)

	body, _ := json.Marshal(map[string]any{
		"prompt_id":         "accepted-prompt",
		"agent":             "nova",
		"task":              "routing",
		"success":           true,
		"confidence_update": 0.1,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/feedback", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.SubmitFeedback(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	updated, err := h.loader.LoadByID("accepted-prompt")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if updated.Confidence <= 0.5 {
		t.Errorf("confidence = %.4f, want > 0.5 (learning loop did not fire)", updated.Confidence)
	}
}

// TestGovernorFeedbackRejectsUnknownPrompt covers the second entry point into
// the same manager — the Governor route must not be a validation bypass.
func TestGovernorFeedbackRejectsUnknownPrompt(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(dir)

	body, _ := json.Marshal(map[string]any{
		"prompt_id": "ghost-prompt", "task_type": "routing", "success": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/governor/feedback", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.RecordFeedback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// B3 — timeouts
// ---------------------------------------------------------------------------

// TestLoadAllContextTimesOut simulates a slow filesystem via the ioDelay hook
// and asserts the caller is released on schedule rather than blocking on I/O.
func TestLoadAllContextTimesOut(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 10; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("slow-%02d", i), 0.5)
	}

	loader.setIODelay(100 * time.Millisecond) // 10 files => ~1s of I/O
	loader.InvalidateCache()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := loader.LoadAllContext(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("LoadAllContext returned nil error, want timeout")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %v, want a timeout error", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("returned after %v; timeout did not release the caller", elapsed)
	}
}

// TestSubmitFeedbackTimesOutOnSlowFilesystem proves the deadline reaches the
// write path, not just reads.
func TestSubmitFeedbackTimesOutOnSlowFilesystem(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 10; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("slowfb-%02d", i), 0.5)
	}

	fm := NewFeedbackManagerWithLoader(dir, loader)
	fm.timeout = 100 * time.Millisecond

	loader.setIODelay(100 * time.Millisecond)
	loader.InvalidateCache()

	start := time.Now()
	err := fm.SubmitFeedback(&models.Feedback{
		PromptID: "slowfb-00", Agent: "nova", Success: true, ConfidenceUpdate: 0.1,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("SubmitFeedback succeeded, want timeout")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("returned after %v; deadline was not enforced", elapsed)
	}
}

// TestMetricsSurvivesSlowFilesystem asserts a stalled corpus degrades the
// scrape into an error counter instead of hanging it.
func TestMetricsSurvivesSlowFilesystem(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 10; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("metricslow-%02d", i), 0.8)
	}

	h := NewHandler(dir)
	h.metricsHandler.timeout = 100 * time.Millisecond
	h.metricsHandler.SetCacheTTL(0) // force recompute
	h.loader.setIODelay(100 * time.Millisecond)
	h.loader.InvalidateCache()

	req := httptest.NewRequest(http.MethodGet, "/mcp/prompts/metrics", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { defer close(done); h.GetMetrics(rec, req) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GetMetrics hung on a slow filesystem")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degrade, not fail)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "prompts_scrape_errors_total 1") {
		t.Error("slow corpus did not surface as prompts_scrape_errors_total 1")
	}
}

// ---------------------------------------------------------------------------
// B2 — metrics cache
// ---------------------------------------------------------------------------

func scrape(t testing.TB, h *Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/mcp/prompts/metrics", nil)
	rec := httptest.NewRecorder()
	h.GetMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", rec.Code)
	}
	return rec.Body.String()
}

func TestMetricsCacheServesRepeatScrapes(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 20; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("cache-%02d", i), 0.85)
	}
	h := NewHandler(dir)

	for i := 0; i < 10; i++ {
		scrape(t, h)
	}

	hits, misses, _ := h.metricsHandler.MetricsCacheStats()
	if misses != 1 {
		t.Errorf("misses = %d, want 1 (only the first scrape should compute)", misses)
	}
	if hits != 9 {
		t.Errorf("hits = %d, want 9", hits)
	}
}

// TestMetricsCacheInvalidatedByFeedback is the correctness half of the cache:
// a write must be visible on the very next scrape, not TTL seconds later.
func TestMetricsCacheInvalidatedByFeedback(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase4Prompt(t, loader, "cache-invalidate-01", 0.9)
	h := NewHandler(dir)

	first := scrape(t, h)
	if !strings.Contains(first, "prompts_feedback_success_total 0") {
		t.Fatalf("unexpected initial feedback count:\n%s", first)
	}

	body, _ := json.Marshal(map[string]any{
		"prompt_id": "cache-invalidate-01", "agent": "nova", "success": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/feedback", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.SubmitFeedback(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("feedback status = %d: %s", rec.Code, rec.Body.String())
	}

	second := scrape(t, h)
	if !strings.Contains(second, "prompts_feedback_success_total 1") {
		t.Errorf("cache was not invalidated by feedback; still reporting stale counts:\n%s", second)
	}
}

func TestMetricsCacheConcurrentScrapesComputeOnce(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 20; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("herd-%02d", i), 0.85)
	}
	h := NewHandler(dir)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/mcp/prompts/metrics", nil)
			rec := httptest.NewRecorder()
			h.GetMetrics(rec, req)
		}()
	}
	wg.Wait()

	_, misses, _ := h.metricsHandler.MetricsCacheStats()
	if misses != 1 {
		t.Errorf("misses = %d, want 1: concurrent scrapes should collapse into one computation", misses)
	}
}

// TestMetricsCacheLatencyImprovement is the measured before/after for the
// cache. Compares TTL=0 (pre-fix behaviour: recompute per scrape) against the
// shipped cache over the same corpus.
func TestMetricsCacheLatencyImprovement(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 100; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("latency-%03d", i), 0.85)
	}
	h := NewHandler(dir)

	const iterations = 30

	// BEFORE: no cache anywhere — recompute every scrape.
	h.metricsHandler.SetCacheTTL(0)
	h.loader.SetCacheTTL(0)
	uncachedStart := time.Now()
	for i := 0; i < iterations; i++ {
		scrape(t, h)
	}
	uncached := time.Since(uncachedStart) / iterations

	// AFTER: shipped configuration.
	h.loader.SetCacheTTL(DefaultCacheTTL)
	h.metricsHandler.SetCacheTTL(DefaultMetricsCacheTTL)
	scrape(t, h) // prime
	cachedStart := time.Now()
	for i := 0; i < iterations; i++ {
		scrape(t, h)
	}
	cached := time.Since(cachedStart) / iterations

	t.Logf("scrape latency: uncached=%v cached=%v speedup=%.1fx",
		uncached, cached, float64(uncached)/float64(cached))

	if cached >= uncached {
		t.Errorf("cache did not reduce scrape latency: uncached=%v cached=%v", uncached, cached)
	}
	// Target from the blocker: 90% reduction.
	if float64(cached) > float64(uncached)*0.10 {
		t.Errorf("latency reduction below target: uncached=%v cached=%v (want cached <= 10%% of uncached)",
			uncached, cached)
	}
}

// ---------------------------------------------------------------------------
// B4 — token savings
// ---------------------------------------------------------------------------

// TestTokenSavingsIsNotHardcoded proves the metric now tracks real data: two
// corpora with different served/total ratios must report different savings.
func TestTokenSavingsIsNotHardcoded(t *testing.T) {
	long := strings.Repeat("word ", 500)

	small := []models.Prompt{
		{ID: "a", Content: "short"},
		{ID: "b", Content: "short"},
		{ID: "c", Content: "short"},
		{ID: "d", Content: "short"},
	}
	large := []models.Prompt{
		{ID: "a", Content: "short"},
		{ID: "b", Content: "short"},
		{ID: "c", Content: "short"},
	}
	for i := 0; i < 50; i++ {
		large = append(large, models.Prompt{ID: fmt.Sprintf("bulk-%d", i), Content: long})
	}

	idx := func(ps []models.Prompt) *models.RegistryIndex {
		entries := make([]models.RegistryEntry, 0, len(ps))
		for _, p := range ps {
			entries = append(entries, models.RegistryEntry{ID: p.ID, Domain: "router-prompts"})
		}
		return &models.RegistryIndex{Prompts: entries}
	}

	smallSavings := computeTokenSavings(small, idx(small))
	largeSavings := computeTokenSavings(large, idx(large))

	if smallSavings.measured != 1 || largeSavings.measured != 1 {
		t.Fatalf("expected measured=1 for both, got %d and %d",
			smallSavings.measured, largeSavings.measured)
	}
	if smallSavings.percent == largeSavings.percent {
		t.Errorf("savings identical (%.2f) across different corpora — still effectively hardcoded",
			smallSavings.percent)
	}
	// A big corpus where only 3 tiny prompts are served must save far more.
	if largeSavings.percent <= smallSavings.percent {
		t.Errorf("large corpus savings %.2f%% <= small corpus %.2f%%; ratio is inverted",
			largeSavings.percent, smallSavings.percent)
	}
	if largeSavings.corpusTokens <= largeSavings.servedTokens {
		t.Errorf("corpus tokens (%d) must exceed served tokens (%d)",
			largeSavings.corpusTokens, largeSavings.servedTokens)
	}
}

func TestTokenSavingsEmptyCorpusIsUnavailableNotPerfect(t *testing.T) {
	got := computeTokenSavings(nil, &models.RegistryIndex{})
	if got.measured != 0 {
		t.Errorf("measured = %d, want 0 for an empty corpus", got.measured)
	}
	if got.percent != 0 {
		t.Errorf("percent = %.2f, want 0 — an empty registry must not report perfect savings", got.percent)
	}
}

func TestTokenSavingsEnvOverride(t *testing.T) {
	t.Setenv(tokenSavingsEnv, "42.5")

	got := computeTokenSavings([]models.Prompt{{ID: "a", Content: "x"}},
		&models.RegistryIndex{Prompts: []models.RegistryEntry{{ID: "a"}}})

	if got.percent != 42.5 {
		t.Errorf("percent = %.2f, want 42.5 from %s", got.percent, tokenSavingsEnv)
	}
	if got.measured != 2 {
		t.Errorf("measured = %d, want 2 (operator-configured)", got.measured)
	}
}

func TestMetricsEmitsDynamicTokenSavings(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < 12; i++ {
		seedPhase4Prompt(t, loader, fmt.Sprintf("tok-%02d", i), 0.85)
	}
	h := NewHandler(dir)

	body := scrape(t, h)

	if strings.Contains(body, "prompts_token_savings_percent 89\n") {
		t.Error("token savings is still the hardcoded 89")
	}
	for _, want := range []string{
		"prompts_token_savings_measured 1",
		"prompts_corpus_tokens_estimated",
		"prompts_served_tokens_estimated",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Load test — 100 concurrent feedback submissions over HTTP
// ---------------------------------------------------------------------------

// TestLoadConcurrentFeedbackHTTP drives the real HTTP handler, so it covers
// decode, validation, ledger append and the atomic confidence update together.
func TestLoadConcurrentFeedbackHTTP(t *testing.T) {
	const requests = 100
	const promptID = "load-test-prompt"

	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase4Prompt(t, loader, promptID, 0.0)
	h := NewHandler(dir)

	var wg sync.WaitGroup
	codes := make([]int, requests)
	latencies := make([]time.Duration, requests)
	start := make(chan struct{})

	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{
				"prompt_id":         promptID,
				"agent":             fmt.Sprintf("load-agent-%03d", n),
				"task":              "load-test",
				"success":           true,
				"confidence_update": 0.005,
			})
			req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/feedback", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			<-start
			t0 := time.Now()
			h.SubmitFeedback(rec, req)
			latencies[n] = time.Since(t0)
			codes[n] = rec.Code
		}(i)
	}

	close(start)
	wg.Wait()

	accepted := 0
	var maxLatency time.Duration
	for i := range codes {
		if codes[i] == http.StatusAccepted {
			accepted++
		}
		if latencies[i] > maxLatency {
			maxLatency = latencies[i]
		}
	}

	if accepted != requests {
		t.Errorf("accepted %d/%d requests", accepted, requests)
	}

	final, err := h.loader.LoadByID(promptID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	want := float32(0.005 * requests)
	if diff := final.Confidence - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("confidence = %.4f, want %.4f: updates were lost under load",
			final.Confidence, want)
	}
	if got := countLedgerLines(t, dir); got != requests {
		t.Errorf("ledger has %d records, want %d", got, requests)
	}

	t.Logf("100 concurrent submissions: all accepted, max latency %v, final confidence %.4f",
		maxLatency, final.Confidence)
}

// ---------------------------------------------------------------------------
// Benchmarks — cache before/after
// ---------------------------------------------------------------------------

func benchmarkScrape(b *testing.B, corpus int, metricsTTL, loaderTTL time.Duration) {
	dir := b.TempDir()
	loader := NewPromptLoader(dir)
	for i := 0; i < corpus; i++ {
		seedPhase4Prompt(b, loader, fmt.Sprintf("bench-%04d", i), 0.85)
	}
	h := NewHandler(dir)
	h.metricsHandler.SetCacheTTL(metricsTTL)
	h.loader.SetCacheTTL(loaderTTL)

	req := httptest.NewRequest(http.MethodGet, "/mcp/prompts/metrics", nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.GetMetrics(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}

// BenchmarkMetricsScrapeBefore_NoCache reproduces the pre-fix hot path.
func BenchmarkMetricsScrapeBefore_NoCache(b *testing.B) {
	for _, n := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("corpus=%d", n), func(b *testing.B) {
			benchmarkScrape(b, n, 0, 0)
		})
	}
}

// BenchmarkMetricsScrapeAfter_Cached measures the shipped configuration.
func BenchmarkMetricsScrapeAfter_Cached(b *testing.B) {
	for _, n := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("corpus=%d", n), func(b *testing.B) {
			benchmarkScrape(b, n, DefaultMetricsCacheTTL, DefaultCacheTTL)
		})
	}
}

// BenchmarkConcurrentFeedback measures throughput of the serialised write path.
func BenchmarkConcurrentFeedback(b *testing.B) {
	dir := b.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase4Prompt(b, loader, "bench-feedback", 0.5)
	fm := NewFeedbackManagerWithLoader(dir, loader)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = fm.SubmitFeedback(&models.Feedback{
				PromptID: "bench-feedback", Agent: "bench", Success: true,
			})
		}
	})
}

// ---------------------------------------------------------------------------
// Data-loss regression: whole-number confidence
// ---------------------------------------------------------------------------

// TestWholeNumberConfidenceSurvivesRoundTrip guards the bug found by
// TestConcurrentFeedbackClampsAtBounds: yaml.v3 writes float32(1) as the
// integer `1`, which failed the loader's `.(float64)` assertion and was
// silently replaced by the 0.5 default. A prompt at full confidence was
// therefore demoted to 0.5 on its next read.
func TestWholeNumberConfidenceSurvivesRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name       string
		confidence float32
		rate       float32
	}{
		{"saturated", 1.0, 1.0},
		{"zeroed", 0.0, 0.0},
		{"fractional", 0.85, 0.72},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			loader := NewPromptLoader(dir)

			now := time.Now().UTC()
			p := models.Prompt{
				ID: "roundtrip", Domain: "router-prompts", Scope: "project",
				Confidence: tc.confidence, SuccessRate: tc.rate,
				Content: "x", CreatedAt: now, UpdatedAt: now,
			}
			if err := loader.SavePrompt(&p, p.Scope); err != nil {
				t.Fatalf("save: %v", err)
			}

			got, err := loader.LoadByID("roundtrip")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.Confidence != tc.confidence {
				t.Errorf("confidence = %v, want %v (silently reset on load)", got.Confidence, tc.confidence)
			}
			if got.SuccessRate != tc.rate {
				t.Errorf("success_rate = %v, want %v", got.SuccessRate, tc.rate)
			}
		})
	}
}
