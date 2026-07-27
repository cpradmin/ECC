package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// Notifier delivers an enriched alert to an external system.
type Notifier interface {
	Kind() string
	Notify(ctx context.Context, n *AlertNotification) error
}

// ErrNoNotifier is returned when no delivery channel is configured at all.
var ErrNoNotifier = errors.New("no alert notifier configured")

// AlertNotification is the fully enriched alert handed to a Notifier and, on
// failure, written to the fallback journal.
type AlertNotification struct {
	Metric       string        `json:"metric"`
	Severity     AlertSeverity `json:"severity"`
	Value        float32       `json:"value"`
	Threshold    float32       `json:"threshold"`
	Comparison   ComparisonOp  `json:"comparison"`
	Body         string        `json:"body"`
	Description  string        `json:"description"`
	Hostname     string        `json:"hostname"`
	Service      string        `json:"service"`
	Timestamp    time.Time     `json:"timestamp"`
	Runbook      string        `json:"runbook"`
	Dashboard    string        `json:"dashboard"`
	Channel      string        `json:"channel"`
	Kind         string        `json:"notification_kind"`
	Escalate     bool          `json:"escalate"`
	MirroredFrom string        `json:"mirrored_from,omitempty"`
}

// SlackNotifier posts alerts to a Slack Incoming Webhook via the slack-go SDK.
//
// Retry strategy: at most MaxAttempts tries with exponential backoff + jitter.
// Only transient failures are retried — transport errors, HTTP 5xx and 429
// (slack-go surfaces the latter two as errors implementing Retryable()). A 4xx
// is a permanent configuration fault (revoked or typo'd webhook, malformed
// payload); retrying it only delays the fallback write, so we bail immediately.
type SlackNotifier struct {
	WebhookURL        string
	Client            *http.Client
	MaxAttempts       int
	BaseBackoff       time.Duration
	MaxBackoff        time.Duration
	PerAttemptTimeout time.Duration

	// Sleep is injectable so retry tests run without wall-clock delay.
	Sleep func(time.Duration)

	mu  sync.Mutex
	rnd *rand.Rand
}

// NewSlackNotifier builds a notifier for the given Incoming Webhook URL.
func NewSlackNotifier(webhookURL string) *SlackNotifier {
	return &SlackNotifier{
		WebhookURL:        webhookURL,
		Client:            &http.Client{Timeout: 10 * time.Second},
		MaxAttempts:       3,
		BaseBackoff:       250 * time.Millisecond,
		MaxBackoff:        4 * time.Second,
		PerAttemptTimeout: 5 * time.Second,
		Sleep:             time.Sleep,
		rnd:               rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Kind implements Notifier.
func (s *SlackNotifier) Kind() string { return "slack" }

// Notify posts the alert, retrying transient failures.
func (s *SlackNotifier) Notify(ctx context.Context, n *AlertNotification) error {
	if s.WebhookURL == "" {
		return fmt.Errorf("slack notifier: %w", ErrNoNotifier)
	}

	msg := s.BuildMessage(n)
	attempts := s.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	perAttempt := s.PerAttemptTimeout
	if perAttempt <= 0 {
		perAttempt = 5 * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, perAttempt)
		err := slack.PostWebhookCustomHTTPContext(attemptCtx, s.WebhookURL, s.httpClient(), msg)
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err

		if !isRetryableSlackError(err) {
			return fmt.Errorf("slack notify (permanent, attempt %d/%d): %w", attempt, attempts, err)
		}
		if attempt == attempts {
			break
		}
		if ctx.Err() != nil {
			return fmt.Errorf("slack notify aborted: %w", ctx.Err())
		}
		s.doSleep(s.backoffFor(attempt, err))
	}
	return fmt.Errorf("slack notify failed after %d attempt(s): %w", attempts, lastErr)
}

func (s *SlackNotifier) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *SlackNotifier) doSleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

// backoffFor returns exponential backoff with jitter, honoring an explicit
// Retry-After from Slack when present.
func (s *SlackNotifier) backoffFor(attempt int, err error) time.Duration {
	var rl *slack.RateLimitedError
	if errors.As(err, &rl) && rl.RetryAfter > 0 {
		return rl.RetryAfter
	}

	base := s.BaseBackoff
	if base <= 0 {
		base = 250 * time.Millisecond
	}
	d := base << (attempt - 1)
	if s.MaxBackoff > 0 && d > s.MaxBackoff {
		d = s.MaxBackoff
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rnd == nil {
		s.rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	// Jitter uniformly in [d/2, d] — spreads retries without starving them.
	half := d / 2
	return half + time.Duration(s.rnd.Int63n(int64(half)+1))
}

func isRetryableSlackError(err error) bool {
	var retryable interface{ Retryable() bool }
	if errors.As(err, &retryable) {
		return retryable.Retryable()
	}
	// Transport-level failure (DNS, TCP, TLS, timeout): worth another try.
	return true
}

// severityColor maps severity to a Slack attachment color bar.
func severityColor(s AlertSeverity) string {
	switch s {
	case SeverityCritical:
		return "#d0021b"
	case SeverityError:
		return "#f5a623"
	default:
		return "#f8e71c"
	}
}

// BuildMessage renders the enriched Slack payload. Every alert carries hostname,
// timestamp, dashboard deeplink and runbook so the responder never has to go
// looking for context.
func (s *SlackNotifier) BuildMessage(n *AlertNotification) *slack.WebhookMessage {
	title := fmt.Sprintf("[%s] %s", strings.ToUpper(string(n.Severity)), n.Metric)
	if n.MirroredFrom != "" {
		title = fmt.Sprintf("[%s] %s (mirrored — %s notifier not wired)",
			strings.ToUpper(string(n.Severity)), n.Metric, n.MirroredFrom)
	}

	fields := []slack.AttachmentField{
		{Title: "Metric", Value: n.Metric, Short: true},
		{Title: "Severity", Value: string(n.Severity), Short: true},
		{Title: "Value", Value: fmt.Sprintf("%.4f", n.Value), Short: true},
		{Title: "Threshold", Value: fmt.Sprintf("%s %.4f", n.Comparison, n.Threshold), Short: true},
		{Title: "Host", Value: n.Hostname, Short: true},
		{Title: "Service", Value: n.Service, Short: true},
		{Title: "Timestamp", Value: n.Timestamp.UTC().Format(time.RFC3339), Short: true},
	}
	if n.Dashboard != "" {
		fields = append(fields, slack.AttachmentField{Title: "Dashboard", Value: n.Dashboard, Short: false})
	}
	if n.Runbook != "" {
		fields = append(fields, slack.AttachmentField{Title: "Runbook", Value: n.Runbook, Short: false})
	}

	text := n.Body
	if n.Escalate {
		text = "<!here> " + text
	}

	return &slack.WebhookMessage{
		Channel:  n.Channel,
		Username: "prompts-mcp",
		Text:     text,
		Attachments: []slack.Attachment{{
			Color:    severityColor(n.Severity),
			Fallback: fmt.Sprintf("%s: %s", title, n.Body),
			Title:    title,
			Text:     n.Description,
			Fields:   fields,
			Footer:   fmt.Sprintf("prompts-mcp on %s", n.Hostname),
			Ts:       json.Number(fmt.Sprintf("%d", n.Timestamp.Unix())),
		}},
	}
}
