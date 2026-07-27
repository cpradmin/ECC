package handlers

// Phase 5 blocker regression suite.
//
// Install with:
//     cp /tmp/phase5_slack_integration_test.go \
//        /home/kntrnjb/Projects/prompts-mcp/handlers/phase5_alerts_test.go
//     go test ./handlers/ -run 'TestB4|TestDedup|TestRunbook|TestDeadCode|TestEnrich' -v
//
// Covers:
//   B4  — TriggerAlert really posts to Slack, with retries + fallback on failure
//   B5  — alert deduplication (Duration pending window + Cooldown suppression)
//   B6  — runbook link validation at startup
//   B7  — dead AlertMatrix/switch code removed; matrix only references emitted metrics
//   B8  — alert context enrichment (hostname, timestamp, dashboard deeplink, runbook)

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testLogger(t *testing.T) *Logger {
	t.Helper()
	return NewLogger(t.TempDir())
}

// fixedContext gives every test a deterministic hostname/timestamp/dashboard base.
func fixedContext() AlertContext {
	return AlertContext{
		Hostname:      "nobara-pc",
		Timestamp:     time.Date(2026, 7, 26, 15, 4, 5, 0, time.UTC),
		DashboardBase: "https://grafana.bailey-home.org",
		Service:       "prompts-mcp",
	}
}

// slackSpy stands in for Slack's Incoming Webhook endpoint.
type slackSpy struct {
	srv      *httptest.Server
	payloads chan map[string]interface{}
	hits     atomic.Int32
	// status returns the HTTP status for hit n (1-based).
	status func(n int32) int
}

func newSlackSpy(t *testing.T, status func(n int32) int) *slackSpy {
	t.Helper()
	if status == nil {
		status = func(int32) int { return http.StatusOK }
	}
	s := &slackSpy{payloads: make(chan map[string]interface{}, 16), status: status}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := s.hits.Add(1)
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		select {
		case s.payloads <- body:
		default:
		}
		code := s.status(n)
		if code == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "0")
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// fastNotifier is a SlackNotifier pointed at the spy with sleeps stubbed out.
func fastNotifier(spy *slackSpy) *SlackNotifier {
	n := NewSlackNotifier(spy.srv.URL)
	n.BaseBackoff = time.Millisecond
	n.MaxBackoff = 2 * time.Millisecond
	n.Sleep = func(time.Duration) {} // no wall-clock delay in tests
	return n
}

func warningThreshold() AlertThreshold {
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning)
	if th == nil {
		panic("prompts_all_avg_confidence/warning missing from AlertMatrix")
	}
	return *th
}

func criticalThreshold() AlertThreshold {
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityCritical)
	if th == nil {
		panic("prompts_all_avg_confidence/critical missing from AlertMatrix")
	}
	return *th
}

func readFallback(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []map[string]interface{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("fallback line is not JSON: %v (%q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func newTestHandler(t *testing.T, notifier Notifier) *AlertHandler {
	t.Helper()
	ah := NewAlertHandlerWithNotifier(testLogger(t), notifier)
	ah.SetAlertContextFunc(fixedContext)
	ah.SetFallbackPath(filepath.Join(t.TempDir(), "alerts-undelivered.jsonl"))
	return ah
}

// ---------------------------------------------------------------------------
// B4: TriggerAlert actually delivers to Slack
// ---------------------------------------------------------------------------

func TestB4TriggerAlertPostsToSlack(t *testing.T) {
	spy := newSlackSpy(t, nil)
	ah := newTestHandler(t, fastNotifier(spy))

	if err := ah.TriggerAlert(warningThreshold(), 0.42); err != nil {
		t.Fatalf("TriggerAlert returned error: %v", err)
	}
	if got := spy.hits.Load(); got != 1 {
		t.Fatalf("expected exactly 1 webhook POST, got %d", got)
	}

	var payload map[string]interface{}
	select {
	case payload = <-spy.payloads:
	case <-time.After(2 * time.Second):
		t.Fatal("no payload captured from Slack webhook")
	}

	if ch, _ := payload["channel"].(string); ch != "#prompts-mcp-alerts" {
		t.Errorf("channel = %q, want #prompts-mcp-alerts", ch)
	}
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "0.42") {
		t.Errorf("text does not carry the metric value: %q", text)
	}
	if strings.Contains(text, "{{") || strings.Contains(text, "}}") {
		t.Errorf("text has unsubstituted template markers: %q", text)
	}
	if _, ok := payload["attachments"]; !ok {
		t.Fatal("payload has no attachments — enrichment missing")
	}
}

func TestB4SlackRetriesTransientFailures(t *testing.T) {
	// 500, 500, then 200 — should succeed on the third attempt.
	spy := newSlackSpy(t, func(n int32) int {
		if n < 3 {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	})
	ah := newTestHandler(t, fastNotifier(spy))

	if err := ah.TriggerAlert(warningThreshold(), 0.31); err != nil {
		t.Fatalf("TriggerAlert should have recovered on retry, got: %v", err)
	}
	if got := spy.hits.Load(); got != 3 {
		t.Errorf("expected 3 attempts (2 retries), got %d", got)
	}
	if recs := readFallback(t, ah.FallbackPath()); len(recs) != 0 {
		t.Errorf("successful delivery should not write fallback, got %d record(s)", len(recs))
	}
}

func TestB4SlackDoesNotRetryPermanentFailures(t *testing.T) {
	// 404 = revoked/typo'd webhook. Retrying only delays the fallback write.
	spy := newSlackSpy(t, func(int32) int { return http.StatusNotFound })
	ah := newTestHandler(t, fastNotifier(spy))

	err := ah.TriggerAlert(warningThreshold(), 0.31)
	if err == nil {
		t.Fatal("TriggerAlert must return an error when Slack rejects the post")
	}
	if got := spy.hits.Load(); got != 1 {
		t.Errorf("4xx must not be retried; got %d attempts", got)
	}
	if !strings.Contains(err.Error(), "permanent") {
		t.Errorf("error should identify the failure as permanent: %v", err)
	}
}

func TestB4FallbackWritesUndeliveredAlertAndReturnsError(t *testing.T) {
	spy := newSlackSpy(t, func(int32) int { return http.StatusInternalServerError })
	ah := newTestHandler(t, fastNotifier(spy))

	err := ah.TriggerAlert(warningThreshold(), 0.11)
	if err == nil {
		t.Fatal("TriggerAlert must return an error when all Slack attempts fail")
	}

	recs := readFallback(t, ah.FallbackPath())
	if len(recs) != 1 {
		t.Fatalf("expected 1 fallback record, got %d", len(recs))
	}
	rec := recs[0]
	if rec["metric"] != "prompts_all_avg_confidence" {
		t.Errorf("fallback metric = %v", rec["metric"])
	}
	if rec["hostname"] != "nobara-pc" {
		t.Errorf("fallback lost hostname enrichment: %v", rec["hostname"])
	}
	if reason, _ := rec["reason"].(string); reason == "" {
		t.Error("fallback record has no failure reason")
	}
}

func TestB4NoNotifierConfiguredIsAnError(t *testing.T) {
	ah := newTestHandler(t, nil)

	err := ah.TriggerAlert(warningThreshold(), 0.1)
	if !errors.Is(err, ErrNoNotifier) {
		t.Fatalf("expected ErrNoNotifier, got %v", err)
	}
	if len(readFallback(t, ah.FallbackPath())) != 1 {
		t.Error("undeliverable alert must still be journaled")
	}
}

func TestB4PagerDutyIsMirroredNotSilentlyDropped(t *testing.T) {
	spy := newSlackSpy(t, nil)
	ah := newTestHandler(t, fastNotifier(spy))

	// The critical threshold declares notification "pagerduty", which is not wired.
	if err := ah.TriggerAlert(criticalThreshold(), 0.20); err != nil {
		t.Fatalf("mirrored alert should still be delivered: %v", err)
	}

	payload := <-spy.payloads
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "<!here>") {
		t.Errorf("escalating alert should @here the channel: %q", text)
	}

	recs := readFallback(t, ah.FallbackPath())
	if len(recs) != 1 {
		t.Fatalf("mirrored page must be journaled for audit, got %d record(s)", len(recs))
	}
	if recs[0]["mirrored_from"] != "pagerduty" {
		t.Errorf("mirrored_from = %v, want pagerduty", recs[0]["mirrored_from"])
	}
}

// ---------------------------------------------------------------------------
// B5: deduplication
// ---------------------------------------------------------------------------

func TestDedupDurationWindowDelaysFirstFire(t *testing.T) {
	ah := newTestHandler(t, nil)
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ah.SetClock(func() time.Time { return now })

	th := warningThreshold() // Duration = 5m

	if ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
		t.Fatal("first breach must open the pending window, not fire immediately")
	}
	now = now.Add(4 * time.Minute)
	if ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
		t.Fatal("fired before Duration elapsed — Duration is being ignored")
	}
	now = now.Add(2 * time.Minute) // total 6m > 5m
	if !ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
		t.Fatal("should fire once the Duration window has been held")
	}
}

func TestDedupOnlyOneAlertPerCooldown(t *testing.T) {
	ah := newTestHandler(t, nil)
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ah.SetClock(func() time.Time { return now })

	th := warningThreshold() // Duration 5m, Cooldown 15m

	// Hold the pending window open, then fire once.
	ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning)
	now = now.Add(6 * time.Minute)
	if !ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
		t.Fatal("expected the initial fire")
	}

	// 10 further minutes of scrapes at 30s intervals: all inside the cooldown.
	fires := 0
	for i := 0; i < 20; i++ {
		now = now.Add(30 * time.Second)
		if ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
			fires++
		}
	}
	if fires != 0 {
		t.Fatalf("metric parked below threshold fired %d duplicate alerts inside the cooldown", fires)
	}
	if got := ah.SuppressedCount(th.MetricName, SeverityWarning); got != 20 {
		t.Errorf("SuppressedCount = %d, want 20", got)
	}

	// Past the cooldown, one reminder is allowed.
	now = now.Add(6 * time.Minute) // 10m + 6m = 16m > 15m cooldown
	if !ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
		t.Error("expected a single reminder after the cooldown expired")
	}
}

func TestDedupRecoveryResetsState(t *testing.T) {
	ah := newTestHandler(t, nil)
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ah.SetClock(func() time.Time { return now })

	th := warningThreshold()
	ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning)
	now = now.Add(6 * time.Minute)
	if !ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
		t.Fatal("expected initial fire")
	}

	// Metric recovers — state must be dropped.
	now = now.Add(1 * time.Minute)
	if ah.CheckThreshold(th.MetricName, 0.95, SeverityWarning) {
		t.Fatal("healthy value must not fire")
	}
	if ah.ActiveAlerts() != 0 {
		t.Errorf("recovered alert left %d state(s) behind", ah.ActiveAlerts())
	}

	// A fresh breach restarts the pending window rather than firing instantly.
	now = now.Add(1 * time.Minute)
	if ah.CheckThreshold(th.MetricName, 0.40, SeverityWarning) {
		t.Error("re-breach should reopen the pending window, not fire immediately")
	}
}

func TestDedupSeveritiesTrackedIndependently(t *testing.T) {
	ah := newTestHandler(t, nil)
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ah.SetClock(func() time.Time { return now })

	// 0.30 breaches BOTH the 0.70 warning and the 0.50 critical threshold.
	ah.CheckThreshold("prompts_all_avg_confidence", 0.30, SeverityWarning)
	ah.CheckThreshold("prompts_all_avg_confidence", 0.30, SeverityCritical)

	now = now.Add(3 * time.Minute) // > critical Duration (2m), < warning Duration (5m)
	if ah.CheckThreshold("prompts_all_avg_confidence", 0.30, SeverityWarning) {
		t.Error("warning fired before its 5m Duration")
	}
	if !ah.CheckThreshold("prompts_all_avg_confidence", 0.30, SeverityCritical) {
		t.Error("critical should fire after its 2m Duration")
	}
	if ah.ActiveAlerts() != 2 {
		t.Errorf("expected 2 independent states, got %d", ah.ActiveAlerts())
	}
}

func TestDedupOnlyOneSlackMessagePerCooldown(t *testing.T) {
	// End-to-end: 20 scrapes above threshold => exactly 1 Slack POST.
	spy := newSlackSpy(t, nil)
	ah := newTestHandler(t, fastNotifier(spy))
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ah.SetClock(func() time.Time { return now })

	th := warningThreshold()
	for i := 0; i < 20; i++ {
		if fired, err := ah.CheckAndTrigger(th.MetricName, 0.40, SeverityWarning); fired && err != nil {
			t.Fatalf("delivery error: %v", err)
		}
		now = now.Add(30 * time.Second) // 10 minutes of scrapes total
	}
	if got := spy.hits.Load(); got != 1 {
		t.Fatalf("expected exactly 1 Slack message across the cooldown, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// B6: runbook validation
// ---------------------------------------------------------------------------

// stageRunbooks materialises every AlertMatrix runbook under a temp root.
func stageRunbooks(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, th := range AlertMatrix {
		p := filepath.Join(root, strings.TrimPrefix(th.Action.Runbook, "/"))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("# runbook\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRunbookValidationPassesWhenAllPresent(t *testing.T) {
	if err := ValidateAlertMatrix(stageRunbooks(t)); err != nil {
		t.Fatalf("validation should pass with all runbooks staged: %v", err)
	}
}

func TestRunbookValidationFailsWhenOneIsDeleted(t *testing.T) {
	root := stageRunbooks(t)
	victim := filepath.Join(root, strings.TrimPrefix(AlertMatrix[0].Action.Runbook, "/"))
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}

	err := ValidateAlertMatrix(root)
	if err == nil {
		t.Fatal("startup validation must fail when a runbook is missing")
	}
	if !strings.Contains(err.Error(), AlertMatrix[0].Action.Runbook) {
		t.Errorf("error should name the missing runbook: %v", err)
	}
}

func TestRunbookValidationRejectsEmptyRunbook(t *testing.T) {
	root := stageRunbooks(t)
	victim := filepath.Join(root, strings.TrimPrefix(AlertMatrix[0].Action.Runbook, "/"))
	if err := os.WriteFile(victim, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAlertMatrix(root); err == nil {
		t.Fatal("a zero-byte runbook is a broken link and must fail validation")
	}
}

func TestRunbookValidationAggregatesAllProblems(t *testing.T) {
	// An empty root => every runbook is missing; all should be reported at once
	// so an operator fixes them in one pass instead of one deploy per link.
	err := ValidateAlertMatrix(t.TempDir())
	if err == nil {
		t.Fatal("expected validation failure")
	}
	for _, th := range AlertMatrix {
		if !strings.Contains(err.Error(), th.Action.Runbook) {
			t.Errorf("aggregated error omits %s", th.Action.Runbook)
		}
	}
}

func TestRunbookValidationAgainstRealRepo(t *testing.T) {
	// Guards the checked-in docs/runbooks/ tree against drift.
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "runbooks")); err != nil {
		t.Skip("docs/runbooks not present; skipping repo-tree check")
	}
	if err := ValidateAlertMatrix(root); err != nil {
		t.Fatalf("checked-in runbooks are out of sync with AlertMatrix: %v", err)
	}
}

// ---------------------------------------------------------------------------
// B7: dead code removed / matrix references only emitted metrics
// ---------------------------------------------------------------------------

func TestDeadCodeAlertMatrixOnlyReferencesEmittedMetrics(t *testing.T) {
	for i, th := range AlertMatrix {
		if _, ok := EmittedMetrics[th.MetricName]; !ok {
			t.Errorf("AlertMatrix[%d] alerts on %q which /mcp/metrics never emits", i, th.MetricName)
		}
	}
}

func TestDeadCodeEveryThresholdDeclaresComparison(t *testing.T) {
	for i, th := range AlertMatrix {
		switch th.Comparison {
		case ComparisonBelow, ComparisonAbove:
		default:
			t.Errorf("AlertMatrix[%d] (%s) has no Comparison — direction would be guessed", i, th.MetricName)
		}
	}
}

func TestDeadCodeRemovedMetricsHaveNoThreshold(t *testing.T) {
	// These three drove the unreachable switch cases in the old CheckThreshold.
	for _, metric := range []string{
		"prompts_daily_pipeline_duration_seconds",
		"prompts_trinity_facts_exported",
		"prompts_promoted_count",
	} {
		for _, sev := range []AlertSeverity{SeverityWarning, SeverityCritical, SeverityError} {
			if GetAlertThreshold(metric, sev) != nil {
				t.Errorf("%s/%s resurrected in AlertMatrix without an emitted metric", metric, sev)
			}
		}
	}

	ah := newTestHandler(t, nil)
	if ah.CheckThreshold("prompts_daily_pipeline_duration_seconds", 99999, SeverityWarning) {
		t.Error("unknown metric must never fire")
	}
	if ah.ActiveAlerts() != 0 {
		t.Error("unknown metric must not allocate dedup state")
	}
}

func TestDeadCodeComparisonDirectionIsHonoured(t *testing.T) {
	ah := newTestHandler(t, nil)
	th := warningThreshold()
	if th.Comparison != ComparisonBelow {
		t.Fatalf("fixture assumption broken: %s", th.Comparison)
	}
	// Above a "below" threshold is healthy, no matter how large.
	if ah.CheckThreshold(th.MetricName, 9.99, SeverityWarning) {
		t.Error("a value above a ComparisonBelow threshold must not fire")
	}
}

// ---------------------------------------------------------------------------
// B8: enrichment
// ---------------------------------------------------------------------------

func TestEnrichNotificationCarriesFullContext(t *testing.T) {
	n := BuildAlertNotification(warningThreshold(), 0.42, fixedContext())

	if n.Hostname != "nobara-pc" {
		t.Errorf("hostname = %q", n.Hostname)
	}
	if n.Timestamp.IsZero() {
		t.Error("timestamp missing")
	}
	if n.Service != "prompts-mcp" {
		t.Errorf("service = %q", n.Service)
	}
	if n.Runbook != "/docs/runbooks/low-confidence-recovery.md" {
		t.Errorf("runbook = %q", n.Runbook)
	}
	want := "https://grafana.bailey-home.org/d/prompts-registry-health/prompts-registry-health"
	if n.Dashboard != want {
		t.Errorf("dashboard deeplink = %q, want %q", n.Dashboard, want)
	}
	if n.Comparison != ComparisonBelow {
		t.Errorf("comparison = %q", n.Comparison)
	}
}

func TestEnrichDashboardFallsBackToRawPathWithoutBase(t *testing.T) {
	ctx := fixedContext()
	ctx.DashboardBase = ""
	n := BuildAlertNotification(warningThreshold(), 0.42, ctx)
	if n.Dashboard != "/d/prompts-registry-health/prompts-registry-health" {
		t.Errorf("dashboard = %q, want the raw path", n.Dashboard)
	}
}

func TestEnrichTemplateExposesContextPlaceholders(t *testing.T) {
	th := warningThreshold()
	th.Action.Message = "{{ .metric }} {{ .value }}/{{ .threshold }} sev={{ .severity }} " +
		"host={{ .hostname }} at={{ .timestamp }} dash={{ .dashboard }} rb={{ .runbook }} svc={{ .service }}"

	got := FormatAlertMessageContext(th, 0.42, fixedContext())
	for _, want := range []string{
		"prompts_all_avg_confidence", "0.42", "0.70", "sev=warning",
		"host=nobara-pc", "at=2026-07-26T15:04:05Z",
		"dash=https://grafana.bailey-home.org/d/prompts-registry-health/prompts-registry-health",
		"rb=/docs/runbooks/low-confidence-recovery.md", "svc=prompts-mcp",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered message missing %q\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, "{{") {
		t.Errorf("unsubstituted markers remain: %s", got)
	}
}

func TestEnrichSlackPayloadIncludesAllContext(t *testing.T) {
	spy := newSlackSpy(t, nil)
	ah := newTestHandler(t, fastNotifier(spy))

	if err := ah.TriggerAlert(warningThreshold(), 0.42); err != nil {
		t.Fatalf("TriggerAlert: %v", err)
	}
	payload := <-spy.payloads

	raw, _ := json.Marshal(payload)
	blob := string(raw)
	for _, want := range []string{
		"nobara-pc",
		"2026-07-26T15:04:05Z",
		"https://grafana.bailey-home.org/d/prompts-registry-health/prompts-registry-health",
		"/docs/runbooks/low-confidence-recovery.md",
		"prompts_all_avg_confidence",
		"prompts-mcp",
	} {
		if !strings.Contains(blob, want) {
			t.Errorf("Slack payload missing enrichment %q\npayload: %s", want, blob)
		}
	}
}

func TestEnrichDefaultContextUsesRealHostname(t *testing.T) {
	ctx := DefaultAlertContext()
	host, err := os.Hostname()
	if err == nil && host != "" && ctx.Hostname != host {
		t.Errorf("DefaultAlertContext hostname = %q, want %q", ctx.Hostname, host)
	}
	if ctx.Hostname == "" {
		t.Error("hostname must never be empty")
	}
	if time.Since(ctx.Timestamp) > time.Minute {
		t.Errorf("timestamp is not current: %s", ctx.Timestamp)
	}
}

// ---------------------------------------------------------------------------
// EvaluateAll smoke test
// ---------------------------------------------------------------------------

func TestEvaluateAllFiresMatchingThresholdsOnly(t *testing.T) {
	spy := newSlackSpy(t, nil)
	ah := newTestHandler(t, fastNotifier(spy))
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ah.SetClock(func() time.Time { return now })

	snapshot := map[string]float32{
		"prompts_all_avg_confidence":    0.30, // breaches warning AND critical
		"prompts_feedback_success_rate": 0.95, // healthy
	}

	if fired, _ := ah.EvaluateAll(context.Background(), snapshot); fired != 0 {
		t.Fatalf("nothing should fire inside the pending window, got %d", fired)
	}
	now = now.Add(11 * time.Minute) // clears every Duration in the matrix
	fired, errs := ah.EvaluateAll(context.Background(), snapshot)
	if len(errs) != 0 {
		t.Fatalf("delivery errors: %v", errs)
	}
	if fired != 2 {
		t.Errorf("expected warning + critical to fire, got %d", fired)
	}
	if got := spy.hits.Load(); got != 2 {
		t.Errorf("expected 2 Slack posts, got %d", got)
	}
}
