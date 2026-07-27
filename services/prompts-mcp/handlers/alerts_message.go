package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// AlertContext carries the enrichment attached to every alert. It is injected
// rather than read from globals so tests get deterministic output.
type AlertContext struct {
	Hostname      string
	Timestamp     time.Time
	DashboardBase string // e.g. https://grafana.bailey-home.org
	Service       string
}

// TemplateData renders the substitution map handed to Action.Message.
// Templates may reference: value, threshold, metric, severity, hostname,
// timestamp, dashboard, runbook, service.
func (c AlertContext) TemplateData(th AlertThreshold, value float32) map[string]interface{} {
	return map[string]interface{}{
		"value":     fmt.Sprintf("%.2f", value),
		"threshold": fmt.Sprintf("%.2f", th.Threshold),
		"metric":    th.MetricName,
		"severity":  string(th.Severity),
		"hostname":  c.Hostname,
		"timestamp": c.Timestamp.UTC().Format(time.RFC3339),
		"dashboard": c.DashboardURL(th),
		"runbook":   th.Action.Runbook,
		"service":   c.Service,
	}
}

// DashboardURL builds an absolute deeplink from AlertAction.Dashboard.
func (c AlertContext) DashboardURL(th AlertThreshold) string {
	d := th.Action.Dashboard
	if d == "" {
		return ""
	}
	if strings.HasPrefix(d, "http://") || strings.HasPrefix(d, "https://") {
		return d
	}
	if c.DashboardBase == "" {
		return d
	}
	return strings.TrimSuffix(c.DashboardBase, "/") + "/" + strings.TrimPrefix(d, "/")
}

// FormatAlertMessage renders the alert body with metric values substituted and
// enrichment (hostname/timestamp/dashboard/runbook) available to the template.
// Structured enrichment is also carried on AlertNotification so the delivered
// payload gets it whether or not the template asks for it.
func FormatAlertMessage(threshold AlertThreshold, value float32) string {
	return FormatAlertMessageContext(threshold, value, DefaultAlertContext())
}

// FormatAlertMessageContext is FormatAlertMessage with an injectable context.
func FormatAlertMessageContext(threshold AlertThreshold, value float32, ctx AlertContext) string {
	t, err := template.New("msg").Parse(threshold.Action.Message)
	if err != nil {
		return fmt.Sprintf("[Error parsing message template: %v]", err)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, ctx.TemplateData(threshold, value)); err != nil {
		return fmt.Sprintf("[Error formatting message: %v]", err)
	}
	return buf.String()
}

// DefaultAlertContext reads enrichment from the running process/environment.
func DefaultAlertContext() AlertContext {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return AlertContext{
		Hostname:      host,
		Timestamp:     time.Now().UTC(),
		DashboardBase: os.Getenv("PROMPTS_MCP_DASHBOARD_BASE"),
		Service:       "prompts-mcp",
	}
}

// BuildAlertNotification assembles the enriched payload for a firing threshold.
func BuildAlertNotification(th AlertThreshold, value float32, ctx AlertContext) *AlertNotification {
	return &AlertNotification{
		Metric:      th.MetricName,
		Severity:    th.Severity,
		Value:       value,
		Threshold:   th.Threshold,
		Comparison:  th.Comparison,
		Body:        FormatAlertMessageContext(th, value, ctx),
		Description: th.Description,
		Hostname:    ctx.Hostname,
		Service:     ctx.Service,
		Timestamp:   ctx.Timestamp.UTC(),
		Runbook:     th.Action.Runbook,
		Dashboard:   ctx.DashboardURL(th),
		Channel:     th.Action.Channel,
		Kind:        th.Action.Notification,
		Escalate:    th.Action.Escalate,
	}
}

// TriggerAlert delivers an alert through the configured notifier.
//
// Delivery contract:
//   - "slack"             -> posted to the Slack Incoming Webhook (with retries).
//   - "pagerduty"/"email" -> NOT implemented. Mirrored to Slack with an explicit
//     "mirrored" banner AND journaled to the fallback file, so a critical page is
//     never silently dropped just because PagerDuty isn't wired yet.
//   - Any delivery failure -> appended to the undelivered-alert JSONL journal and
//     returned as an error. This never returns nil for a failed send.
func (ah *AlertHandler) TriggerAlert(threshold AlertThreshold, value float32) error {
	return ah.TriggerAlertContext(context.Background(), threshold, value)
}

// TriggerAlertContext is TriggerAlert with a caller-supplied context.
func (ah *AlertHandler) TriggerAlertContext(ctx context.Context, threshold AlertThreshold, value float32) error {
	ah.mu.Lock()
	ctxFn := ah.ctxFn
	notifier := ah.notifier
	ah.mu.Unlock()

	if ctxFn == nil {
		ctxFn = DefaultAlertContext
	}
	n := BuildAlertNotification(threshold, value, ctxFn())

	ah.logger.LogError(
		fmt.Sprintf("alert_fired: %s", n.Metric),
		fmt.Errorf("severity=%s value=%f channel=%s host=%s dashboard=%s runbook=%s message=%s",
			n.Severity, n.Value, n.Channel, n.Hostname, n.Dashboard, n.Runbook, n.Body),
	)

	if notifier == nil {
		err := fmt.Errorf("alert %s/%s undelivered: %w (set SLACK_WEBHOOK_URL)", n.Metric, n.Severity, ErrNoNotifier)
		ah.recordFallback(n, err)
		return err
	}

	// pagerduty/email are not wired; mirror them to the available notifier and
	// journal them rather than pretending someone was paged.
	if n.Kind != notifier.Kind() {
		n.MirroredFrom = n.Kind
		ah.recordFallback(n, fmt.Errorf("notifier %q not implemented; mirrored to %q", n.Kind, notifier.Kind()))
	}

	if err := notifier.Notify(ctx, n); err != nil {
		wrapped := fmt.Errorf("alert %s/%s delivery failed: %w", n.Metric, n.Severity, err)
		ah.recordFallback(n, wrapped)
		return wrapped
	}
	return nil
}

// recordFallback appends an undelivered alert to the JSONL journal. Best effort:
// if even this fails we log it, because there is nowhere else left to put it.
func (ah *AlertHandler) recordFallback(n *AlertNotification, cause error) {
	ah.mu.Lock()
	path := ah.fallbackPath
	now := ah.clockLocked().UTC()
	ah.mu.Unlock()

	if path == "" {
		ah.logger.LogError("alert_fallback_unavailable", cause)
		return
	}

	record := struct {
		*AlertNotification
		RecordedAt time.Time `json:"recorded_at"`
		Reason     string    `json:"reason"`
	}{AlertNotification: n, RecordedAt: now, Reason: cause.Error()}

	line, err := json.Marshal(record)
	if err != nil {
		ah.logger.LogError("alert_fallback_marshal_failed", err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ah.logger.LogError("alert_fallback_mkdir_failed", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		ah.logger.LogError("alert_fallback_open_failed", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		ah.logger.LogError("alert_fallback_write_failed", err)
		return
	}
	ah.logger.LogError("alert_fallback_recorded", cause)
}
