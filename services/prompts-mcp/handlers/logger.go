package handlers

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Logger provides structured logging across all handlers
type Logger struct {
	handler slog.Handler
	logger  *slog.Logger
}

// NewLogger creates a new structured logger
func NewLogger(logDir string) *Logger {
	// Create log directory if needed
	os.MkdirAll(logDir, 0755)

	// Open log file
	logFile := filepath.Join(logDir, "service.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// Fall back to stderr if file creation fails, but warn
		fmt.Fprintf(os.Stderr, "⚠️  Logger fallback: Failed to open %s: %v (writing to stderr)\n", logFile, err)
		f = os.Stderr
	}

	// Create JSON handler for structured logging
	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: false,
	})

	logger := slog.New(handler)
	return &Logger{
		handler: handler,
		logger:  logger,
	}
}

// LogRegistryQuery logs a registry query
func (l *Logger) LogRegistryQuery(domain string, minConfidence float32, resultCount int) {
	l.logger.Info("registry_query",
		slog.String("domain", domain),
		slog.Float64("min_confidence", float64(minConfidence)),
		slog.Int("result_count", resultCount),
	)
}

// LogPromptFetch logs a single prompt fetch
func (l *Logger) LogPromptFetch(promptID string, found bool) {
	status := "found"
	if !found {
		status = "not_found"
	}
	l.logger.Info("prompt_fetch",
		slog.String("prompt_id", promptID),
		slog.String("status", status),
	)
}

// LogPromptSearch logs a search operation
func (l *Logger) LogPromptSearch(query string, resultCount int) {
	l.logger.Info("prompt_search",
		slog.String("query", query),
		slog.Int("result_count", resultCount),
	)
}

// LogFeedbackSubmission logs feedback submission
func (l *Logger) LogFeedbackSubmission(promptID string, success bool, newConfidence float32) {
	l.logger.Info("feedback_submitted",
		slog.String("prompt_id", promptID),
		slog.Bool("success", success),
		slog.Float64("new_confidence", float64(newConfidence)),
	)
}

// LogPromptExport logs prompt export
func (l *Logger) LogPromptExport(format string, domain string, count int) {
	l.logger.Info("prompt_export",
		slog.String("format", format),
		slog.String("domain", domain),
		slog.Int("count", count),
	)
}

// LogPromptImport logs prompt import
func (l *Logger) LogPromptImport(count int, successCount int, failureCount int) {
	l.logger.Info("prompt_import",
		slog.Int("count", count),
		slog.Int("success_count", successCount),
		slog.Int("failure_count", failureCount),
	)
}

// LogRegistryRebuild logs registry rebuild
func (l *Logger) LogRegistryRebuild(elapsedMs int64, promptCount int) {
	l.logger.Info("registry_rebuild",
		slog.Int64("elapsed_ms", elapsedMs),
		slog.Int("prompt_count", promptCount),
	)
}

// LogGovernorQuery logs Governor routing query
func (l *Logger) LogGovernorQuery(taskType string, domain string, recommendationCount int, routingSession string) {
	l.logger.Info("governor_route_query",
		slog.String("task_type", taskType),
		slog.String("domain", domain),
		slog.Int("recommendation_count", recommendationCount),
		slog.String("routing_session", routingSession),
	)
}

// LogGovernorFeedback logs Governor feedback
func (l *Logger) LogGovernorFeedback(routingSession string, promptID string, success bool, newConfidence float32) {
	l.logger.Info("governor_feedback",
		slog.String("routing_session", routingSession),
		slog.String("prompt_id", promptID),
		slog.Bool("success", success),
		slog.Float64("new_confidence", float64(newConfidence)),
	)
}

// LogReleaseGeneration logs release manifest generation
func (l *Logger) LogReleaseGeneration(version string, promptCount int) {
	l.logger.Info("release_generated",
		slog.String("version", version),
		slog.Int("prompt_count", promptCount),
	)
}

// LogReleasePublish logs release publishing
func (l *Logger) LogReleasePublish(version string, promptCount int, manifestPath string) {
	l.logger.Info("release_published",
		slog.String("version", version),
		slog.Int("prompt_count", promptCount),
		slog.String("manifest_path", manifestPath),
	)
}

// LogTrinityExport logs Trinity facts export
func (l *Logger) LogTrinityExport(factCount int) {
	l.logger.Info("trinity_export",
		slog.Int("fact_count", factCount),
	)
}

// LogError logs an error event
func (l *Logger) LogError(operation string, err error) {
	l.logger.Error(operation,
		slog.String("error", err.Error()),
	)
}

// LogPipelineStep logs a daily pipeline step
func (l *Logger) LogPipelineStep(step int, action string, status string, details map[string]interface{}) {
	attrs := []slog.Attr{
		slog.Int("step", step),
		slog.String("action", action),
		slog.String("status", status),
	}
	for k, v := range details {
		attrs = append(attrs, slog.Any(k, v))
	}
	l.logger.LogAttrs(nil, slog.LevelInfo, "pipeline_step", attrs...)
}
