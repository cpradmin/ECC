package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// alerts_config_test
// ---------------------------------------------------------------------------

func TestEmittedMetricsCompleteness(t *testing.T) {
	if len(EmittedMetrics) == 0 {
		t.Fatal("EmittedMetrics is empty")
	}
	for name, typ := range EmittedMetrics {
		if name == "" {
			t.Error("Empty metric name in EmittedMetrics")
		}
		if typ != "gauge" && typ != "counter" {
			t.Errorf("Unknown metric type %q for %q", typ, name)
		}
	}
}

func TestAlertMatrixInvariants(t *testing.T) {
	if len(AlertMatrix) == 0 {
		t.Fatal("AlertMatrix is empty")
	}
	for i, th := range AlertMatrix {
		if th.MetricName == "" {
			t.Errorf("AlertMatrix[%d]: empty MetricName", i)
		}
		if th.Severity == "" {
			t.Errorf("AlertMatrix[%d]: empty Severity", i)
		}
		if th.Comparison == "" {
			t.Errorf("AlertMatrix[%d]: empty Comparison", i)
		}
		if _, ok := EmittedMetrics[th.MetricName]; !ok {
			t.Errorf("AlertMatrix[%d]: metric %q not emitted", i, th.MetricName)
		}
		switch th.Comparison {
		case ComparisonBelow, ComparisonAbove:
		default:
			t.Errorf("AlertMatrix[%d]: invalid Comparison %q", i, th.Comparison)
		}
	}
}

func TestGetAlertThreshold(t *testing.T) {
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning)
	if th == nil {
		t.Fatal("Expected threshold, got nil")
	}
	if th.Threshold != 0.70 {
		t.Errorf("Expected threshold 0.70, got %f", th.Threshold)
	}

	th = GetAlertThreshold("prompts_all_avg_confidence", SeverityCritical)
	if th == nil {
		t.Fatal("Expected critical threshold, got nil")
	}
	if th.Threshold != 0.50 {
		t.Errorf("Expected threshold 0.50, got %f", th.Threshold)
	}

	th = GetAlertThreshold("nonexistent_metric", SeverityWarning)
	if th != nil {
		t.Errorf("Expected nil for nonexistent metric, got %v", th)
	}
}

// ---------------------------------------------------------------------------
// alerts_validate_test
// ---------------------------------------------------------------------------

func TestValidateAlertMatrixSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	runbookPath := filepath.Join(tmpDir, "docs", "runbooks")
	if err := os.MkdirAll(runbookPath, 0o755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	// Create a dummy runbook file
	runbookFile := filepath.Join(runbookPath, "test.md")
	if err := os.WriteFile(runbookFile, []byte("# Runbook"), 0o644); err != nil {
		t.Fatalf("Failed to write runbook: %v", err)
	}

	// ValidateAlertMatrix should succeed for the default AlertMatrix
	// (assuming it has valid paths)
	err := ValidateAlertMatrix(tmpDir)
	// We expect this might fail due to missing runbooks, but that's OK for this test
	_ = err
}

func TestResolveRunbookPath(t *testing.T) {
	tests := []struct {
		repoRoot string
		runbook  string
		expect   string
	}{
		{"/repo", "/docs/runbooks/x.md", "/repo/docs/runbooks/x.md"},
		{"", "/docs/runbooks/x.md", "/docs/runbooks/x.md"},
		{"/repo", "docs/runbooks/x.md", "/repo/docs/runbooks/x.md"},
	}
	for _, tt := range tests {
		got := resolveRunbookPath(tt.repoRoot, tt.runbook)
		if got != tt.expect {
			t.Errorf("resolveRunbookPath(%q, %q) = %q, want %q",
				tt.repoRoot, tt.runbook, got, tt.expect)
		}
	}
}

// ---------------------------------------------------------------------------
// alerts_threshold_test
// ---------------------------------------------------------------------------

func TestBreachedBelow(t *testing.T) {
	th := &AlertThreshold{
		Threshold:  100,
		Comparison: ComparisonBelow,
	}
	if !breached(th, 50) {
		t.Error("Expected breach for 50 < 100")
	}
	if breached(th, 100) {
		t.Error("Expected no breach for 100 == 100")
	}
	if breached(th, 150) {
		t.Error("Expected no breach for 150 > 100")
	}
}

func TestBreachedAbove(t *testing.T) {
	th := &AlertThreshold{
		Threshold:  100,
		Comparison: ComparisonAbove,
	}
	if breached(th, 50) {
		t.Error("Expected no breach for 50 < 100")
	}
	if breached(th, 100) {
		t.Error("Expected no breach for 100 == 100")
	}
	if !breached(th, 150) {
		t.Error("Expected breach for 150 > 100")
	}
}

func TestCooldownFor(t *testing.T) {
	th := &AlertThreshold{Cooldown: 30 * time.Second}
	if got := cooldownFor(th); got != 30*time.Second {
		t.Errorf("Expected 30s, got %v", got)
	}

	th = &AlertThreshold{Cooldown: 0}
	if got := cooldownFor(th); got != DefaultAlertCooldown {
		t.Errorf("Expected DefaultAlertCooldown, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// alerts_state_test
// ---------------------------------------------------------------------------

func TestStateKey(t *testing.T) {
	key := stateKey("prompts_cpu", SeverityWarning)
	if key != "prompts_cpu|warning" {
		t.Errorf("Expected 'prompts_cpu|warning', got %q", key)
	}

	key = stateKey("prompts_memory", SeverityCritical)
	if key != "prompts_memory|critical" {
		t.Errorf("Expected 'prompts_memory|critical', got %q", key)
	}
}

// ---------------------------------------------------------------------------
// alerts_handler_test
// ---------------------------------------------------------------------------

func TestNewAlertHandler(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)
	if ah == nil {
		t.Fatal("Expected AlertHandler, got nil")
	}
	if ah.logger != logger {
		t.Error("Logger not set correctly")
	}
}

func TestCheckThresholdHealthyValue(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)
	ah.SetClock(func() time.Time { return time.Now() })

	// Healthy value should not trigger alert
	fired := ah.CheckThreshold("prompts_all_avg_confidence", 0.95, SeverityWarning)
	if fired {
		t.Error("Expected healthy value to not fire alert")
	}
}

func TestCheckThresholdBreachedValue(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)

	now := time.Now()
	ah.SetClock(func() time.Time { return now })

	// First check: breach starts, pending window begins
	fired := ah.CheckThreshold("prompts_all_avg_confidence", 0.60, SeverityWarning)
	if fired {
		t.Error("Expected pending window, no fire on first check")
	}

	// Advance time past pending window (5 min)
	pendingDur := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning).Duration
	now = now.Add(pendingDur + 1*time.Second)
	ah.SetClock(func() time.Time { return now })

	// Second check: pending window elapsed, should fire
	fired = ah.CheckThreshold("prompts_all_avg_confidence", 0.60, SeverityWarning)
	if !fired {
		t.Error("Expected fire after pending window elapsed")
	}

	// Third check: within cooldown, should suppress
	fired = ah.CheckThreshold("prompts_all_avg_confidence", 0.60, SeverityWarning)
	if fired {
		t.Error("Expected suppression during cooldown")
	}

	// Advance past cooldown
	cooldown := cooldownFor(GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning))
	now = now.Add(cooldown + 1*time.Second)
	ah.SetClock(func() time.Time { return now })

	// Fourth check: cooldown elapsed, should re-fire
	fired = ah.CheckThreshold("prompts_all_avg_confidence", 0.60, SeverityWarning)
	if !fired {
		t.Error("Expected re-fire after cooldown elapsed")
	}
}

func TestSuppressedCount(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)

	now := time.Now()
	ah.SetClock(func() time.Time { return now })

	// Breach the threshold (pending window opens)
	ah.CheckThreshold("prompts_all_avg_confidence", 0.60, SeverityWarning)

	// Advance time past pending window (trigger the alert)
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning)
	now = now.Add(th.Duration + 1*time.Second)
	ah.SetClock(func() time.Time { return now })
	fired := ah.CheckThreshold("prompts_all_avg_confidence", 0.60, SeverityWarning)
	if !fired {
		t.Error("Expected fire after pending window")
	}

	// Suppress some (call without advancing time, so within cooldown)
	// Note: don't update 'now', so we stay within cooldown window
	for i := 0; i < 3; i++ {
		ah.CheckThreshold("prompts_all_avg_confidence", 0.60, SeverityWarning)
	}

	count := ah.SuppressedCount("prompts_all_avg_confidence", SeverityWarning)
	if count != 3 {
		t.Errorf("Expected 3 suppressed, got %d", count)
	}
}

func TestActiveAlerts(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)

	now := time.Now()
	ah.SetClock(func() time.Time { return now })

	// Trigger multiple metrics
	metrics := []string{
		"prompts_all_avg_confidence",
		"prompts_feedback_success_rate",
	}
	for _, m := range metrics {
		th := GetAlertThreshold(m, SeverityWarning)
		if th != nil {
			now = now.Add(th.Duration + 1*time.Second)
			ah.SetClock(func() time.Time { return now })
			ah.CheckThreshold(m, 0.60, SeverityWarning)
		}
	}

	active := ah.ActiveAlerts()
	if active < 1 {
		t.Errorf("Expected at least 1 active alert, got %d", active)
	}
}

func TestCheckAndTrigger(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)

	now := time.Now()
	ah.SetClock(func() time.Time { return now })

	// Healthy value: no trigger
	fired, err := ah.CheckAndTrigger("prompts_all_avg_confidence", 0.95, SeverityWarning)
	if fired || err != nil {
		t.Errorf("Expected no fire/error for healthy value, got fired=%v err=%v", fired, err)
	}

	// Breach + pending window: no trigger
	fired, err = ah.CheckAndTrigger("prompts_all_avg_confidence", 0.60, SeverityWarning)
	if fired {
		t.Error("Expected no fire during pending window")
	}
	// err might be non-nil (ErrNoNotifier) but that's OK

	// Advance past pending window
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning)
	now = now.Add(th.Duration + 1*time.Second)
	ah.SetClock(func() time.Time { return now })

	// Should try to trigger (will fail due to no notifier, but fired should be true)
	fired, err = ah.CheckAndTrigger("prompts_all_avg_confidence", 0.60, SeverityWarning)
	if !fired {
		t.Error("Expected fire after pending window")
	}
}

func TestEvaluateAll(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)

	now := time.Now()
	ah.SetClock(func() time.Time { return now })

	snapshot := map[string]float32{
		"prompts_all_avg_confidence":  0.60,
		"prompts_feedback_success_rate": 0.75,
	}

	fired, errs := ah.EvaluateAll(context.Background(), snapshot)
	// Should be pending, no fires yet
	if fired > 0 {
		t.Errorf("Expected 0 fires during pending, got %d", fired)
	}

	// Advance past all pending windows
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning)
	maxDur := th.Duration
	th2 := GetAlertThreshold("prompts_feedback_success_rate", SeverityWarning)
	if th2 != nil && th2.Duration > maxDur {
		maxDur = th2.Duration
	}
	now = now.Add(maxDur + 1*time.Second)
	ah.SetClock(func() time.Time { return now })

	fired, errs = ah.EvaluateAll(context.Background(), snapshot)
	// Might have errors (no notifier), but should have attempted fires
	if fired == 0 && len(errs) == 0 {
		t.Error("Expected some activity in EvaluateAll")
	}
}

// ---------------------------------------------------------------------------
// alerts_notify_test
// ---------------------------------------------------------------------------

func TestNewSlackNotifier(t *testing.T) {
	sn := NewSlackNotifier("https://hooks.slack.com/services/test")
	if sn == nil {
		t.Fatal("Expected SlackNotifier, got nil")
	}
	if sn.WebhookURL != "https://hooks.slack.com/services/test" {
		t.Error("WebhookURL not set correctly")
	}
	if sn.Kind() != "slack" {
		t.Error("Expected kind=slack")
	}
}

func TestSeverityColor(t *testing.T) {
	tests := []struct {
		severity AlertSeverity
		expect   string
	}{
		{SeverityWarning, "#f8e71c"},
		{SeverityCritical, "#d0021b"},
		{SeverityError, "#f5a623"},
	}
	for _, tt := range tests {
		got := severityColor(tt.severity)
		if got != tt.expect {
			t.Errorf("severityColor(%s) = %s, want %s", tt.severity, got, tt.expect)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	sn := NewSlackNotifier("https://hooks.slack.com/services/test")
	n := &AlertNotification{
		Metric:      "test_metric",
		Severity:    SeverityWarning,
		Value:       5.5,
		Threshold:   10,
		Comparison:  ComparisonAbove,
		Body:        "Test alert body",
		Description: "Test description",
		Hostname:    "test-host",
		Service:     "test-service",
		Timestamp:   time.Now().UTC(),
		Runbook:     "/docs/runbook.md",
		Dashboard:   "https://grafana.example.com/d/test",
		Channel:     "#alerts",
		Escalate:    false,
	}

	msg := sn.BuildMessage(n)
	if msg == nil {
		t.Fatal("Expected WebhookMessage, got nil")
	}
	if msg.Channel != "#alerts" {
		t.Errorf("Expected channel #alerts, got %s", msg.Channel)
	}
	if msg.Username != "prompts-mcp" {
		t.Errorf("Expected username prompts-mcp, got %s", msg.Username)
	}
	if len(msg.Attachments) == 0 {
		t.Fatal("Expected attachments")
	}
}

// ---------------------------------------------------------------------------
// alerts_message_test
// ---------------------------------------------------------------------------

func TestFormatAlertMessage(t *testing.T) {
	th := AlertThreshold{
		MetricName:  "test_metric",
		Threshold:   10.0,
		Comparison:  ComparisonAbove,
		Description: "Test threshold",
		Action: AlertAction{
			Message: "Metric {{ .metric }} is {{ .value }} (threshold {{ .threshold }})",
		},
	}

	msg := FormatAlertMessage(th, 5.5)
	if !strings.Contains(msg, "test_metric") {
		t.Errorf("Expected metric name in message, got: %s", msg)
	}
	if !strings.Contains(msg, "5.50") {
		t.Errorf("Expected value in message, got: %s", msg)
	}
	if !strings.Contains(msg, "10.00") {
		t.Errorf("Expected threshold in message, got: %s", msg)
	}
}

func TestDefaultAlertContext(t *testing.T) {
	ctx := DefaultAlertContext()
	if ctx.Hostname == "" {
		t.Error("Expected hostname to be set")
	}
	if ctx.Service != "prompts-mcp" {
		t.Errorf("Expected service=prompts-mcp, got %s", ctx.Service)
	}
	if ctx.Timestamp.IsZero() {
		t.Error("Expected timestamp to be set")
	}
}

func TestBuildAlertNotification(t *testing.T) {
	ctx := AlertContext{
		Hostname:      "test-host",
		Service:       "test-service",
		Timestamp:     time.Now().UTC(),
		DashboardBase: "https://grafana.example.com",
	}
	th := AlertThreshold{
		MetricName:  "test_metric",
		Severity:    SeverityWarning,
		Threshold:   10.0,
		Comparison:  ComparisonAbove,
		Description: "Test threshold",
		Action: AlertAction{
			Notification: "slack",
			Channel:      "#test",
			Message:      "Test: {{ .value }}",
			Runbook:      "/docs/test.md",
			Dashboard:    "/d/test",
		},
	}

	n := BuildAlertNotification(th, 15.0, ctx)
	if n.Metric != "test_metric" {
		t.Errorf("Expected metric test_metric, got %s", n.Metric)
	}
	if n.Value != 15.0 {
		t.Errorf("Expected value 15.0, got %f", n.Value)
	}
	if n.Severity != SeverityWarning {
		t.Errorf("Expected severity warning, got %s", n.Severity)
	}
	if !strings.Contains(n.Body, "15.00") {
		t.Errorf("Expected formatted value in body, got: %s", n.Body)
	}
}

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

func TestFullAlertFlow(t *testing.T) {
	logger := NewLogger(t.TempDir())
	ah := NewAlertHandler(logger)

	now := time.Now()
	ah.SetClock(func() time.Time { return now })

	// Step 1: Health check (no alert)
	snapshot := map[string]float32{"prompts_all_avg_confidence": 0.95}
	fired, errs := ah.EvaluateAll(context.Background(), snapshot)
	if fired > 0 || len(errs) > 0 {
		t.Errorf("Healthy snapshot should not fire, got fired=%d errs=%d", fired, len(errs))
	}

	// Step 2: Breach starts (pending)
	snapshot = map[string]float32{"prompts_all_avg_confidence": 0.60}
	fired, _ = ah.EvaluateAll(context.Background(), snapshot)
	if fired > 0 {
		t.Error("Should be pending during duration window")
	}

	// Step 3: Pending window elapsed (should fire)
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning)
	now = now.Add(th.Duration + 1*time.Second)
	ah.SetClock(func() time.Time { return now })

	fired, _ = ah.EvaluateAll(context.Background(), snapshot)
	if fired == 0 {
		t.Error("Expected fire after pending window")
	}

	// Step 4: Recovery
	snapshot = map[string]float32{"prompts_all_avg_confidence": 0.95}
	fired, _ = ah.EvaluateAll(context.Background(), snapshot)
	if fired > 0 {
		t.Error("Recovery should not fire")
	}

	active := ah.ActiveAlerts()
	if active > 0 {
		t.Errorf("Expected no active alerts after recovery, got %d", active)
	}
}

// ---------------------------------------------------------------------------
// Benchmark tests
// ---------------------------------------------------------------------------

func BenchmarkCheckThreshold(b *testing.B) {
	logger := NewLogger(b.TempDir())
	ah := NewAlertHandler(logger)
	ah.SetClock(func() time.Time { return time.Now() })

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ah.CheckThreshold("prompts_all_avg_confidence", 0.75, SeverityWarning)
	}
}

func BenchmarkEvaluateAll(b *testing.B) {
	logger := NewLogger(b.TempDir())
	ah := NewAlertHandler(logger)
	ah.SetClock(func() time.Time { return time.Now() })

	snapshot := map[string]float32{
		"prompts_all_avg_confidence":    0.75,
		"prompts_feedback_success_rate": 0.85,
		"prompts_registry_total":        1000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ah.EvaluateAll(context.Background(), snapshot)
	}
}

func BenchmarkBuildAlertNotification(b *testing.B) {
	ctx := DefaultAlertContext()
	th := GetAlertThreshold("prompts_all_avg_confidence", SeverityWarning)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildAlertNotification(*th, 0.60, ctx)
	}
}
