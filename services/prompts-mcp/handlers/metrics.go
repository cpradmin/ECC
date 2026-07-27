package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// DefaultMetricsCacheTTL bounds how stale a scrape may be. (B2 fix)
//
// Every scrape used to walk the prompt corpus AND replay the entire
// append-only feedback ledger. Prometheus scrapes every 15-60s and nothing
// stops a second scraper, a curl loop, or an AWX health check from doing the
// same concurrently, so cost scaled with scrapers x corpus size while the
// underlying numbers only move when a human or the nightly pipeline changes
// something.
//
// 60s is chosen to sit at or above the scrape interval so the steady state is
// at most one real computation per minute, while staying well under the 5m
// alert evaluation window in deploy/grafana — alerts cannot fire on data older
// than they would have seen anyway.
//
// Correctness comes from invalidation, not the TTL: writes call Invalidate()
// synchronously, so the TTL only ever bounds drift from out-of-band edits.
const DefaultMetricsCacheTTL = 60 * time.Second

// metricsCacheTTLEnv lets operators tighten or disable the cache (0 = off).
const metricsCacheTTLEnv = "PROMPTS_METRICS_CACHE_TTL"

// tokenSavingsEnv pins token savings to an externally measured value.
const tokenSavingsEnv = "PROMPTS_TOKEN_SAVINGS_PERCENT"

// governorTopN mirrors the default `limit` of the Governor routing endpoint.
// Token savings is modelled on what an agent actually receives per query.
const governorTopN = 3

// charsPerToken is the standard ~4 chars/token approximation. Exact enough for
// a ratio, where the constant cancels out of numerator and denominator.
const charsPerToken = 4

// metricsSnapshot is one computed view of every derived metric.
type metricsSnapshot struct {
	registryTotal         int
	registryAvgConfidence float32
	allAvgConfidence      float32
	avgSuccessRate        float32
	promotedCount         int
	successCount          int
	failureCount          int
	feedbackSuccessRate   float32
	feedbackScrapeErrors  int
	promptScrapeErrors    int
	domains               int

	tokenSavingsPercent  float32
	tokenSavingsMeasured int
	corpusTokens         int
	servedTokens         int

	computedAt time.Time
	buildTime  time.Duration
}

// MetricsHandler provides Prometheus-format metrics
type MetricsHandler struct {
	registryHandler   *RegistryHandler
	loader            *PromptLoader
	feedbackManager   *FeedbackManager
	governorHandler   *GovernorHandler
	pipelineStartTime time.Time

	cacheMu  sync.Mutex
	cache    *metricsSnapshot
	cacheTTL time.Duration

	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
	staleServes atomic.Int64

	timeout time.Duration
}

// NewMetricsHandler creates a new metrics handler
func NewMetricsHandler(registryHandler *RegistryHandler, loader *PromptLoader,
	feedbackManager *FeedbackManager, governorHandler *GovernorHandler) *MetricsHandler {
	return &MetricsHandler{
		registryHandler:   registryHandler,
		loader:            loader,
		feedbackManager:   feedbackManager,
		governorHandler:   governorHandler,
		pipelineStartTime: time.Now(),
		cacheTTL:          metricsCacheTTLFromEnv(),
		timeout:           DefaultLoadTimeout,
	}
}

func metricsCacheTTLFromEnv() time.Duration {
	raw := os.Getenv(metricsCacheTTLEnv)
	if raw == "" {
		return DefaultMetricsCacheTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		fmt.Fprintf(os.Stderr, "Warning: invalid %s=%q, using default %s\n",
			metricsCacheTTLEnv, raw, DefaultMetricsCacheTTL)
		return DefaultMetricsCacheTTL
	}
	return d
}

// SetCacheTTL overrides the cache lifetime (0 disables caching).
func (mh *MetricsHandler) SetCacheTTL(ttl time.Duration) {
	mh.cacheMu.Lock()
	mh.cacheTTL = ttl
	mh.cache = nil
	mh.cacheMu.Unlock()
}

// Invalidate drops the cached snapshot. Wired to feedback submission and
// registry rebuild so an operator who POSTs a change and immediately scrapes
// sees it, rather than waiting out the TTL.
func (mh *MetricsHandler) Invalidate() {
	mh.cacheMu.Lock()
	mh.cache = nil
	mh.cacheMu.Unlock()
}

// MetricsCacheStats exposes counters for tests and diagnostics.
func (mh *MetricsHandler) MetricsCacheStats() (hits, misses, stale int64) {
	return mh.cacheHits.Load(), mh.cacheMisses.Load(), mh.staleServes.Load()
}

// snapshot returns a fresh-enough snapshot, computing one if needed.
//
// The whole check-compute-store sequence runs under a plain Mutex rather than
// an RWMutex. That is deliberate: it collapses a thundering herd of concurrent
// scrapes into exactly one filesystem walk, with the losers waiting on the
// lock and then serving the result the winner just stored. An RWMutex would
// let N scrapers all miss and all compute.
func (mh *MetricsHandler) snapshot(ctx context.Context) (*metricsSnapshot, error) {
	mh.cacheMu.Lock()
	defer mh.cacheMu.Unlock()

	if mh.cache != nil && mh.cacheTTL > 0 && time.Since(mh.cache.computedAt) < mh.cacheTTL {
		mh.cacheHits.Add(1)
		return mh.cache, nil
	}
	mh.cacheMisses.Add(1)

	snap, err := mh.computeSnapshot(ctx)
	if err != nil {
		// Serving a stale snapshot beats serving nothing: a scrape gap creates
		// false "no data" alerts, which are worse than metrics a minute old.
		if mh.cache != nil {
			mh.staleServes.Add(1)
			return mh.cache, nil
		}
		return nil, err
	}

	if mh.cacheTTL > 0 {
		mh.cache = snap
	}
	return snap, nil
}

// computeSnapshot does the expensive aggregation.
func (mh *MetricsHandler) computeSnapshot(ctx context.Context) (*metricsSnapshot, error) {
	start := time.Now()
	snap := &metricsSnapshot{}

	index, err := mh.registryHandler.GetIndex()
	if err != nil {
		return nil, fmt.Errorf("error getting registry: %w", err)
	}

	for _, entry := range index.Prompts {
		snap.registryAvgConfidence += entry.Confidence
		snap.avgSuccessRate += entry.SuccessRate
		if entry.Promoted {
			snap.promotedCount++
		}
	}
	if len(index.Prompts) > 0 {
		snap.registryAvgConfidence /= float32(len(index.Prompts))
		snap.avgSuccessRate /= float32(len(index.Prompts))
	}
	snap.registryTotal = len(index.Prompts)
	snap.domains = len(index.Domains)

	// C1: average confidence over ALL prompts, not just the filtered registry.
	// B3: bounded so a stalled filesystem degrades one metric instead of
	// hanging the scrape.
	allPrompts, err := mh.loader.LoadAllContext(ctx)
	if err != nil {
		snap.promptScrapeErrors = 1
	} else if len(allPrompts) > 0 {
		var sum float32
		for _, p := range allPrompts {
			sum += p.Confidence
		}
		snap.allAvgConfidence = sum / float32(len(allPrompts))
	}

	// I6: explicit error metric rather than a faked success rate.
	feedback, err := mh.feedbackManager.LoadAllContext(ctx)
	if err != nil {
		snap.feedbackScrapeErrors = 1
	} else {
		for _, fb := range feedback {
			if fb.Success {
				snap.successCount++
			} else {
				snap.failureCount++
			}
		}
		if total := snap.successCount + snap.failureCount; total > 0 {
			snap.feedbackSuccessRate = float32(snap.successCount) / float32(total)
		}
	}

	savings := computeTokenSavings(allPrompts, index)
	snap.tokenSavingsPercent = savings.percent
	snap.tokenSavingsMeasured = savings.measured
	snap.corpusTokens = savings.corpusTokens
	snap.servedTokens = savings.servedTokens

	snap.computedAt = time.Now()
	snap.buildTime = time.Since(start)
	return snap, nil
}

type tokenSavings struct {
	percent      float32
	measured     int // 1 = derived from live corpus, 2 = operator-configured, 0 = unavailable
	corpusTokens int
	servedTokens int
}

// estimateTokens approximates token count from character length.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + charsPerToken - 1) / charsPerToken
}

// computeTokenSavings derives the token savings figure from real data. (B4 fix)
//
// The old value was the literal constant 89, which made the metric a decoration
// — it could never move, so it could never alert, diverge, or be falsified.
//
// The quantity being measured: without the registry an agent has to ingest the
// whole prompt corpus to find what it needs; with it, the Governor returns the
// top-N ranked prompts and the agent ingests only those. Savings is therefore
//
//	100 * (1 - servedTokens / corpusTokens)
//
// Both inputs are exported alongside it so the ratio is auditable rather than
// asserted, and `prompts_token_savings_measured` states the provenance instead
// of letting a fallback masquerade as a measurement.
//
// PROMPTS_TOKEN_SAVINGS_PERCENT overrides this when savings have been measured
// properly end-to-end (real tokenizer, real agent traffic); that is a more
// trustworthy number than a char-length estimate and takes precedence.
func computeTokenSavings(allPrompts []models.Prompt, index *models.RegistryIndex) tokenSavings {
	if raw := os.Getenv(tokenSavingsEnv); raw != "" {
		if v, err := strconv.ParseFloat(raw, 32); err == nil && v >= 0 && v <= 100 {
			return tokenSavings{percent: float32(v), measured: 2}
		}
		fmt.Fprintf(os.Stderr, "Warning: invalid %s=%q, falling back to measured value\n",
			tokenSavingsEnv, raw)
	}

	if len(allPrompts) == 0 || index == nil {
		return tokenSavings{measured: 0}
	}

	tokensByID := make(map[string]int, len(allPrompts))
	corpusTokens := 0
	for _, p := range allPrompts {
		t := estimateTokens(p.Content) + estimateTokens(p.FrontmatterYAML)
		tokensByID[p.ID] = t
		corpusTokens += t
	}
	if corpusTokens == 0 {
		return tokenSavings{measured: 0}
	}

	// index.Prompts is already ranked by confidence, matching what the
	// Governor would hand back for a routing query.
	servedTokens := 0
	served := 0
	for _, entry := range index.Prompts {
		if served >= governorTopN {
			break
		}
		if t, ok := tokensByID[entry.ID]; ok {
			servedTokens += t
			served++
		}
	}

	// Nothing served means nothing measured. Reporting 100% here would be the
	// same failure mode as the hardcoded constant: an empty registry looking
	// like a perfect one.
	if served == 0 || servedTokens == 0 {
		return tokenSavings{measured: 0, corpusTokens: corpusTokens}
	}

	percent := (1 - float32(servedTokens)/float32(corpusTokens)) * 100
	if percent < 0 {
		percent = 0
	}
	return tokenSavings{
		percent:      percent,
		measured:     1,
		corpusTokens: corpusTokens,
		servedTokens: servedTokens,
	}
}

// GetMetrics returns Prometheus-format metrics
// GET /mcp/prompts/metrics
func (mh *MetricsHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Bound the scrape independently of the client's own deadline.
	ctx, cancel := context.WithTimeout(r.Context(), mh.effectiveTimeout())
	defer cancel()

	snap, err := mh.snapshot(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error building metrics: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	hits, misses, stale := mh.MetricsCacheStats()

	metrics := fmt.Sprintf(`# HELP prompts_registry_total Total prompts in registry
# TYPE prompts_registry_total gauge
prompts_registry_total %d

# HELP prompts_registry_avg_confidence Average confidence score of prompts in registry
# TYPE prompts_registry_avg_confidence gauge
prompts_registry_avg_confidence %.4f

# HELP prompts_all_avg_confidence Average confidence score across ALL prompts (including below-threshold)
# TYPE prompts_all_avg_confidence gauge
prompts_all_avg_confidence %.4f

# HELP prompts_avg_success_rate Average success rate across all prompts
# TYPE prompts_avg_success_rate gauge
prompts_avg_success_rate %.4f

# HELP prompts_promoted_count Prompts promoted to registry (Promoted=true)
# TYPE prompts_promoted_count gauge
prompts_promoted_count %d

# HELP prompts_feedback_success_total Successful feedback records
# TYPE prompts_feedback_success_total counter
prompts_feedback_success_total %d

# HELP prompts_feedback_failure_total Failed feedback records
# TYPE prompts_feedback_failure_total counter
prompts_feedback_failure_total %d

# HELP prompts_feedback_success_rate Success rate of all feedback
# TYPE prompts_feedback_success_rate gauge
prompts_feedback_success_rate %.4f

# HELP prompts_feedback_scrape_errors_total Errors loading feedback data
# TYPE prompts_feedback_scrape_errors_total counter
prompts_feedback_scrape_errors_total %d

# HELP prompts_scrape_errors_total Errors loading prompt corpus during scrape
# TYPE prompts_scrape_errors_total counter
prompts_scrape_errors_total %d

# HELP prompts_registry_domains Number of unique domains in registry
# TYPE prompts_registry_domains gauge
prompts_registry_domains %d

# HELP prompts_token_savings_percent Token savings from registry-based routing vs ingesting the full corpus
# TYPE prompts_token_savings_percent gauge
prompts_token_savings_percent %.2f

# HELP prompts_token_savings_measured Provenance of token savings (0=unavailable, 1=measured from corpus, 2=operator-configured)
# TYPE prompts_token_savings_measured gauge
prompts_token_savings_measured %d

# HELP prompts_corpus_tokens_estimated Estimated tokens to ingest the entire prompt corpus
# TYPE prompts_corpus_tokens_estimated gauge
prompts_corpus_tokens_estimated %d

# HELP prompts_served_tokens_estimated Estimated tokens served per routed query (top-N)
# TYPE prompts_served_tokens_estimated gauge
prompts_served_tokens_estimated %d

# HELP prompts_metrics_cache_hits_total Metrics scrapes served from cache
# TYPE prompts_metrics_cache_hits_total counter
prompts_metrics_cache_hits_total %d

# HELP prompts_metrics_cache_misses_total Metrics scrapes requiring recomputation
# TYPE prompts_metrics_cache_misses_total counter
prompts_metrics_cache_misses_total %d

# HELP prompts_metrics_cache_stale_serves_total Scrapes served from a stale snapshot after a compute error
# TYPE prompts_metrics_cache_stale_serves_total counter
prompts_metrics_cache_stale_serves_total %d

# HELP prompts_metrics_cache_age_seconds Age of the cached metrics snapshot
# TYPE prompts_metrics_cache_age_seconds gauge
prompts_metrics_cache_age_seconds %.3f

# HELP prompts_metrics_build_duration_seconds Time to compute the last metrics snapshot
# TYPE prompts_metrics_build_duration_seconds gauge
prompts_metrics_build_duration_seconds %.6f

# HELP prompts_uptime_seconds Uptime of prompts-mcp service
# TYPE prompts_uptime_seconds gauge
prompts_uptime_seconds %.0f
`,
		snap.registryTotal,
		snap.registryAvgConfidence,
		snap.allAvgConfidence,
		snap.avgSuccessRate,
		snap.promotedCount,
		snap.successCount,
		snap.failureCount,
		snap.feedbackSuccessRate,
		snap.feedbackScrapeErrors,
		snap.promptScrapeErrors,
		snap.domains,
		snap.tokenSavingsPercent,
		snap.tokenSavingsMeasured,
		snap.corpusTokens,
		snap.servedTokens,
		hits,
		misses,
		stale,
		time.Since(snap.computedAt).Seconds(),
		snap.buildTime.Seconds(),
		// Uptime is rendered live, never cached — a frozen uptime counter looks
		// exactly like a hung process to an alert rule.
		time.Since(mh.pipelineStartTime).Seconds(),
	)

	fmt.Fprint(w, metrics)
}

func (mh *MetricsHandler) effectiveTimeout() time.Duration {
	if mh.timeout <= 0 {
		return DefaultLoadTimeout
	}
	return mh.timeout
}
