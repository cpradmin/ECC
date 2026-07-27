package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AlertHandler evaluates thresholds, deduplicates, and delivers alerts.
type AlertHandler struct {
	logger   *Logger
	notifier Notifier

	// fallbackPath receives newline-delimited JSON for alerts that could not be
	// delivered, so a Slack outage degrades to "recoverable on disk", not "lost".
	fallbackPath string

	ctxFn func() AlertContext // enrichment source (injectable for tests)
	now   func() time.Time    // clock (injectable for tests)

	mu     sync.Mutex
	states map[string]*alertState
}

// NewAlertHandler creates a new alert handler.
//
// The Slack webhook is read from SLACK_WEBHOOK_URL. Optional env:
//
//	PROMPTS_MCP_DASHBOARD_BASE  — Grafana base URL for dashboard deeplinks
//	PROMPTS_MCP_ALERT_FALLBACK  — path for the undelivered-alert JSONL journal
func NewAlertHandler(logger *Logger) *AlertHandler {
	ah := NewAlertHandlerWithNotifier(logger, nil)
	if url := os.Getenv("SLACK_WEBHOOK_URL"); url != "" {
		ah.notifier = NewSlackNotifier(url)
	}
	if p := os.Getenv("PROMPTS_MCP_ALERT_FALLBACK"); p != "" {
		ah.fallbackPath = p
	}
	return ah
}

// NewAlertHandlerWithNotifier creates a handler with an explicit notifier.
func NewAlertHandlerWithNotifier(logger *Logger, notifier Notifier) *AlertHandler {
	return &AlertHandler{
		logger:       logger,
		notifier:     notifier,
		fallbackPath: defaultFallbackPath(),
		ctxFn:        DefaultAlertContext,
		now:          time.Now,
		states:       make(map[string]*alertState),
	}
}

func defaultFallbackPath() string {
	dir := "/var/log/prompts-mcp"
	if _, err := os.Stat(dir); err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".local/var/log/prompts-mcp")
	}
	return filepath.Join(dir, "alerts-undelivered.jsonl")
}

// SetClock overrides the handler clock (tests only).
func (ah *AlertHandler) SetClock(now func() time.Time) {
	ah.mu.Lock()
	defer ah.mu.Unlock()
	ah.now = now
}

// SetAlertContextFunc overrides enrichment (tests only).
func (ah *AlertHandler) SetAlertContextFunc(fn func() AlertContext) {
	ah.mu.Lock()
	defer ah.mu.Unlock()
	ah.ctxFn = fn
}

// SetFallbackPath overrides where undelivered alerts are journaled.
func (ah *AlertHandler) SetFallbackPath(p string) {
	ah.mu.Lock()
	defer ah.mu.Unlock()
	ah.fallbackPath = p
}

// FallbackPath reports where undelivered alerts are journaled.
func (ah *AlertHandler) FallbackPath() string {
	ah.mu.Lock()
	defer ah.mu.Unlock()
	return ah.fallbackPath
}

// clockLocked returns the current time. Caller holds ah.mu.
func (ah *AlertHandler) clockLocked() time.Time {
	if ah.now != nil {
		return ah.now()
	}
	return time.Now()
}

// CheckThreshold evaluates a metric against its threshold and applies
// deduplication. It returns true ONLY when an alert should actually be
// delivered right now.
//
// Semantics:
//
//  1. Value healthy                            -> state cleared (resolve), false
//  2. Breach starts                            -> pending window opens, false
//  3. Breach held < Duration                   -> still pending, false
//  4. Breach held >= Duration and never fired  -> FIRES, true
//  5. Already fired, within Cooldown           -> suppressed (counted), false
//  6. Already fired, Cooldown elapsed          -> re-fires (reminder), true
//
// Comparison direction comes from the threshold, not a name-keyed switch.
func (ah *AlertHandler) CheckThreshold(metricName string, value float32, severity AlertSeverity) bool {
	threshold := GetAlertThreshold(metricName, severity)
	if threshold == nil {
		return false
	}

	ah.mu.Lock()
	defer ah.mu.Unlock()

	now := ah.clockLocked()
	ah.pruneStaleLocked(now)

	key := stateKey(metricName, severity)
	st := ah.states[key]

	if !breached(threshold, value) {
		if st != nil && st.Fired {
			ah.logger.LogError(
				fmt.Sprintf("alert_resolved: %s", metricName),
				fmt.Errorf("metric %s recovered to %f (threshold: %f, severity: %s, suppressed_duplicates=%d)",
					metricName, value, threshold.Threshold, severity, st.Suppressed),
			)
		}
		delete(ah.states, key)
		return false
	}

	if st == nil {
		st = &alertState{FirstBreach: now}
		ah.states[key] = st
	}
	st.LastSeen = now

	// Pending window: threshold must be breached continuously for Duration.
	if !st.Fired && now.Sub(st.FirstBreach) < threshold.Duration {
		return false
	}

	// Cooldown window: suppress repeats.
	if st.Fired && now.Sub(st.LastFired) < cooldownFor(threshold) {
		st.Suppressed++
		return false
	}

	st.Fired = true
	st.LastFired = now
	st.Suppressed = 0

	ah.logger.LogError(
		fmt.Sprintf("alert_triggered: %s", metricName),
		fmt.Errorf("metric %s = %f (comparison: %s, threshold: %f, severity: %s, pending_for: %s)",
			metricName, value, threshold.Comparison, threshold.Threshold, severity, now.Sub(st.FirstBreach)),
	)
	return true
}

// pruneStaleLocked drops states untouched for staleStateTTL. Caller holds ah.mu.
func (ah *AlertHandler) pruneStaleLocked(now time.Time) {
	for k, st := range ah.states {
		last := st.LastSeen
		if last.IsZero() {
			last = st.FirstBreach
		}
		if now.Sub(last) > staleStateTTL {
			delete(ah.states, k)
		}
	}
}

// SuppressedCount reports duplicates suppressed since the last delivered alert.
func (ah *AlertHandler) SuppressedCount(metricName string, severity AlertSeverity) int {
	ah.mu.Lock()
	defer ah.mu.Unlock()
	if st := ah.states[stateKey(metricName, severity)]; st != nil {
		return st.Suppressed
	}
	return 0
}

// ActiveAlerts returns the number of tracked (breaching) metric+severity pairs.
func (ah *AlertHandler) ActiveAlerts() int {
	ah.mu.Lock()
	defer ah.mu.Unlock()
	return len(ah.states)
}

// CheckAndTrigger evaluates a metric and delivers an alert if dedup allows it.
// Returns whether an alert fired plus any delivery error.
func (ah *AlertHandler) CheckAndTrigger(metricName string, value float32, severity AlertSeverity) (bool, error) {
	if !ah.CheckThreshold(metricName, value, severity) {
		return false, nil
	}
	th := GetAlertThreshold(metricName, severity)
	if th == nil {
		return false, nil
	}
	return true, ah.TriggerAlert(*th, value)
}

// EvaluateAll runs every AlertMatrix threshold against a metric snapshot.
// Returns the number of alerts delivered and any delivery errors encountered.
func (ah *AlertHandler) EvaluateAll(ctx context.Context, snapshot map[string]float32) (int, []error) {
	var fired int
	var errs []error
	for _, th := range AlertMatrix {
		value, ok := snapshot[th.MetricName]
		if !ok {
			continue
		}
		if !ah.CheckThreshold(th.MetricName, value, th.Severity) {
			continue
		}
		fired++
		if err := ah.TriggerAlertContext(ctx, th, value); err != nil {
			errs = append(errs, err)
		}
	}
	return fired, errs
}
