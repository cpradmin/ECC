package handlers

import (
	"time"
)

// ---------------------------------------------------------------------------
// Metric contract
// ---------------------------------------------------------------------------

// EmittedMetrics is the authoritative list of metric names actually emitted by
// MetricsHandler.GetMetrics (/mcp/metrics). AlertMatrix MUST only reference
// names in this map — ValidateAlertMatrix enforces it at startup.
var EmittedMetrics = map[string]string{
	"prompts_registry_total":               "gauge",
	"prompts_registry_avg_confidence":      "gauge",
	"prompts_all_avg_confidence":           "gauge",
	"prompts_avg_success_rate":             "gauge",
	"prompts_promoted_count":               "gauge",
	"prompts_feedback_success_total":       "counter",
	"prompts_feedback_failure_total":       "counter",
	"prompts_feedback_success_rate":        "gauge",
	"prompts_feedback_scrape_errors_total": "counter",
	"prompts_registry_domains":             "gauge",
	"prompts_token_savings_percent":        "gauge",
	"prompts_uptime_seconds":               "gauge",

	// Phase 4 additions.
	"prompts_scrape_errors_total":              "counter",
	"prompts_token_savings_measured":           "gauge",
	"prompts_corpus_tokens_estimated":          "gauge",
	"prompts_served_tokens_estimated":          "gauge",
	"prompts_metrics_cache_hits_total":         "counter",
	"prompts_metrics_cache_misses_total":       "counter",
	"prompts_metrics_cache_stale_serves_total": "counter",
	"prompts_metrics_cache_age_seconds":        "gauge",
	"prompts_metrics_build_duration_seconds":   "gauge",
}

// ComparisonOp declares which side of the threshold is unhealthy.
type ComparisonOp string

const (
	// ComparisonBelow fires when value < threshold (confidence, success rates).
	ComparisonBelow ComparisonOp = "below"
	// ComparisonAbove fires when value > threshold (latency, error counts).
	ComparisonAbove ComparisonOp = "above"
)

// AlertSeverity represents alert urgency level
type AlertSeverity string

const (
	SeverityWarning  AlertSeverity = "warning"
	SeverityCritical AlertSeverity = "critical"
	SeverityError    AlertSeverity = "error"
)

// AlertThreshold defines a metric threshold and alert behavior
type AlertThreshold struct {
	MetricName  string
	Description string
	Threshold   float32
	Severity    AlertSeverity
	Comparison  ComparisonOp  // Which direction is unhealthy (required)
	Duration    time.Duration // Threshold must be breached this long before the FIRST fire
	Cooldown    time.Duration // Minimum gap between repeat fires (0 => DefaultAlertCooldown)
	Action      AlertAction
}

// AlertAction describes what to do when alert fires
type AlertAction struct {
	Notification string // "slack", "email", "pagerduty"
	Channel      string // "#prompts-mcp-alerts" or "on-call@example.com"
	Message      string // Custom message template
	Runbook      string // Repo-relative path to runbook (validated at startup)
	Dashboard    string // Grafana dashboard deeplink (path or absolute URL)
	Escalate     bool   // Escalate to on-call if true
}

// DefaultAlertCooldown suppresses duplicate alerts for a metric+severity pair.
// A metric parked on the unhealthy side of its threshold fires once per cooldown
// window instead of once per scrape.
const DefaultAlertCooldown = 5 * time.Minute

// AlertMatrix defines all monitoring thresholds for Phase 5.
//
// INVARIANT: every MetricName here must exist in EmittedMetrics. Enforced by
// ValidateAlertMatrix, which main() calls before serving traffic.
var AlertMatrix = []AlertThreshold{
	{
		MetricName:  "prompts_all_avg_confidence",
		Description: "Average confidence score across ALL prompts (including below-threshold)",
		Threshold:   0.70,
		Severity:    SeverityWarning,
		Comparison:  ComparisonBelow,
		Duration:    5 * time.Minute,
		Cooldown:    15 * time.Minute,
		Action: AlertAction{
			Notification: "slack",
			Channel:      "#prompts-mcp-alerts",
			Message:      "Registry avg confidence ({{ .value }}) is below {{ .threshold }} threshold. Review feedback logs.",
			Runbook:      "/docs/runbooks/low-confidence-recovery.md",
			Dashboard:    "/d/prompts-registry-health/prompts-registry-health",
			Escalate:     false,
		},
	},
	{
		MetricName:  "prompts_all_avg_confidence",
		Description: "CRITICAL: Average confidence critically low",
		Threshold:   0.50,
		Severity:    SeverityCritical,
		Comparison:  ComparisonBelow,
		Duration:    2 * time.Minute,
		Cooldown:    5 * time.Minute,
		Action: AlertAction{
			Notification: "pagerduty",
			Channel:      "prompts-team-oncall",
			Message:      "🚨 CRITICAL: Registry avg confidence ({{ .value }}) is BELOW {{ .threshold }}! Immediate investigation required.",
			Runbook:      "/docs/runbooks/critical-confidence-failure.md",
			Dashboard:    "/d/prompts-registry-health/prompts-registry-health",
			Escalate:     true,
		},
	},
	{
		MetricName:  "prompts_feedback_success_rate",
		Description: "Ratio of successful to total feedback submissions",
		Threshold:   0.80,
		Severity:    SeverityWarning,
		Comparison:  ComparisonBelow,
		Duration:    10 * time.Minute,
		Cooldown:    30 * time.Minute,
		Action: AlertAction{
			Notification: "slack",
			Channel:      "#prompts-mcp-alerts",
			Message:      "Governor feedback success rate ({{ .value }}) is below {{ .threshold }}. Check routing logic and agent feedback.",
			Runbook:      "/docs/runbooks/governor-degradation.md",
			Dashboard:    "/d/prompts-governor-intelligence/prompts-governor-routing-intelligence",
			Escalate:     false,
		},
	},
	// Deliberately NOT alerted on (documented so nobody re-adds them blind):
	//   prompts_promoted_count                  — counts prompts ELIGIBLE for promotion; legitimately 0.
	//   prompts_daily_pipeline_duration_seconds — pipeline observability, not emitted by /mcp/metrics.
	//   prompts_trinity_facts_exported          — Trinity RAG integration, not emitted by /mcp/metrics.
	// See PHASE5_DEPLOYMENT.md "Known Limitations".
}

// RecoveryProcedures documents how to handle each alert type
var RecoveryProcedures = map[AlertSeverity]string{
	SeverityWarning: `
## Warning Recovery Steps
1. Check the Monitoring dashboard for context
2. Review relevant logs in Loki (filter by service:prompts-mcp)
3. Assess impact: Is this affecting agent routing? User-facing features?
4. If actionable: Follow the runbook link in the alert
5. If not urgent: Create an issue for post-incident review
6. Silence the alert only after fixing root cause
`,
	SeverityCritical: `
## CRITICAL Alert Recovery
1. PAGE ON-CALL IMMEDIATELY (do not delay)
2. Acknowledge the PagerDuty alert
3. Open the runbook link (provided in alert message)
4. Gather context: Dashboard + logs + recent changes
5. Execute runbook steps or improvise based on observed behavior
6. Keep team in #prompts-mcp-alerts Slack channel updated
7. Post-incident: Update runbook with lessons learned
8. Do NOT silence alert without fixing the root cause
`,
	SeverityError: `
## Error Alert Recovery
1. This indicates a component failure (not performance degradation)
2. Check service health:
   - Is prompts-mcp running? (ps aux | grep prompts-mcp)
   - Is Loki reachable? (curl http://loki:3100/ready)
   - Is the daily pipeline process running?
3. Check logs for stack traces or error messages
4. Restart the affected component if needed
5. Re-run the pipeline if it failed mid-execution
6. Escalate to on-call if restart doesn't resolve
`,
}

// GetAlertThreshold looks up a threshold by metric name and severity
func GetAlertThreshold(metricName string, severity AlertSeverity) *AlertThreshold {
	for i := range AlertMatrix {
		if AlertMatrix[i].MetricName == metricName && AlertMatrix[i].Severity == severity {
			t := AlertMatrix[i]
			return &t
		}
	}
	return nil
}
