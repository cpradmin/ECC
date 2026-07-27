package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

type Handler struct {
	dataHome        string
	loader          *PromptLoader
	feedbackManager *FeedbackManager
	registryHandler *RegistryHandler
	releaseHandler  *ReleaseHandler
	governorHandler *GovernorHandler
	metricsHandler  *MetricsHandler
	logger          *Logger
}

// NewHandler creates a new Handler with the given data home directory
func NewHandler(dataHome string) *Handler {
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local/share")
	}

	// Initialize structured logger first
	logDir := "/var/log/prompts-mcp"
	// Fall back to local directory if /var/log not writable
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		logDir = filepath.Join(os.Getenv("HOME"), ".local/var/log/prompts-mcp")
	}
	logger := NewLogger(logDir)
	// Middleware and the WriteJSON helper have no handler receiver to reach a
	// logger through, so publish this one package-wide.
	SetPackageLogger(logger)

	loader := NewPromptLoader(dataHome)
	// B1 FIX: the feedback manager must share this loader, not build its own.
	// A second loader means a second write lock, and two locks guarding one
	// directory guard nothing.
	feedbackManager := NewFeedbackManagerWithLoader(dataHome, loader)
	registryHandler := NewRegistryHandler(dataHome, loader)
	governorHandler := NewGovernorHandler(registryHandler, loader, feedbackManager, logger)
	metricsHandler := NewMetricsHandler(registryHandler, loader, feedbackManager, governorHandler)

	// B2 FIX: bust the metrics snapshot on every write path, so the TTL only
	// ever covers out-of-band edits rather than our own changes.
	feedbackManager.OnChange(metricsHandler.Invalidate)
	registryHandler.OnChange(metricsHandler.Invalidate)

	return &Handler{
		dataHome:        dataHome,
		loader:          loader,
		feedbackManager: feedbackManager,
		registryHandler: registryHandler,
		releaseHandler:  NewReleaseHandler(dataHome, registryHandler, logger),
		governorHandler: governorHandler,
		metricsHandler:  metricsHandler,
		logger:          logger,
	}
}

// feedbackErrorStatus maps a submission error to an HTTP status.
//
// B5 FIX: an unknown or malformed prompt_id is the caller's mistake, so it must
// not be reported as a 500. The spec for this endpoint calls for 400 on an
// unknown prompt (rather than 404) because the ID arrives in the request body
// as a field value, not as the addressed resource.
func feedbackErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrPromptNotFound),
		errors.Is(err, ErrInvalidFeedback),
		errors.Is(err, ErrInvalidPathSegment):
		return http.StatusBadRequest
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// ListPrompts lists all prompts, optionally filtered by domain
// GET /mcp/prompts/list?domain=go-coding&scope=project
func (h *Handler) ListPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	domain := r.URL.Query().Get("domain")
	scope := r.URL.Query().Get("scope") // project or global

	// Load all prompts
	all, err := h.loader.LoadAll()
	if err != nil {
		h.logger.LogError("list_prompts", err)
		http.Error(w, fmt.Sprintf("Error loading prompts: %v", err), http.StatusInternalServerError)
		return
	}

	// PERF FIX: preallocate. The nil slice grew by repeated append/realloc
	// (log2(n) copies of the whole Prompt array); len(all) is the exact upper
	// bound and is already known. Also guarantees a non-nil slice, so an empty
	// result marshals as [] rather than null.
	prompts := make([]models.Prompt, 0, len(all))

	// Filter by domain and scope
	for _, p := range all {
		if domain != "" && p.Domain != domain {
			continue
		}
		if scope != "" && p.Scope != scope {
			continue
		}
		prompts = append(prompts, p)
	}

	// Log the query
	h.logger.LogRegistryQuery(domain, 0, len(prompts))

	WriteJSONOK(w, map[string]interface{}{
		"status":  "ok",
		"domain":  domain,
		"scope":   scope,
		"count":   len(prompts),
		"prompts": prompts,
	})
}

// GetPrompt retrieves a single prompt by ID
// GET /mcp/prompts/get?id=router-classifier-01
func (h *Handler) GetPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}

	prompt, err := h.loader.LoadByID(id)
	if err != nil {
		h.logger.LogPromptFetch(id, false)
		http.Error(w, fmt.Sprintf("Prompt not found: %s", id), http.StatusNotFound)
		return
	}

	h.logger.LogPromptFetch(id, true)

	WriteJSONOK(w, map[string]interface{}{
		"status": "ok",
		"prompt": prompt,
	})
}

// SearchPrompts searches prompts by keyword or criteria
// GET /mcp/prompts/search?q=classification&domain=router
func (h *Handler) SearchPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query().Get("q")
	domain := r.URL.Query().Get("domain")

	results, err := h.loader.Search(q, domain)
	if err != nil {
		h.logger.LogError("search_prompts", err)
		http.Error(w, fmt.Sprintf("Error searching prompts: %v", err), http.StatusInternalServerError)
		return
	}

	// Log search query
	h.logger.LogPromptSearch(q, len(results))

	WriteJSONOK(w, map[string]interface{}{
		"status":  "ok",
		"query":   q,
		"domain":  domain,
		"count":   len(results),
		"results": results,
	})
}

// SubmitFeedback records feedback for a prompt (success/failure observation)
// POST /mcp/prompts/feedback
// Body: {prompt_id, agent, task, success, note}
func (h *Handler) SubmitFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// B3: bounded read; oversize bodies get 413, malformed JSON gets 400.
	var feedback models.Feedback
	if !DecodeJSONBody(w, r, &feedback) {
		return
	}

	// B5 FIX: full validation (shape, ranges, and prompt existence) lives in the
	// manager so every caller — HTTP, Governor, import — goes through it. The
	// handler's job is only to translate the failure into the right status.
	if err := h.feedbackManager.SubmitFeedbackContext(r.Context(), &feedback); err != nil {
		status := feedbackErrorStatus(err)
		if status >= http.StatusInternalServerError {
			h.logger.LogError("submit_feedback", err)
		}
		http.Error(w, fmt.Sprintf("Error recording feedback: %v", err), status)
		return
	}

	// Log feedback submission
	h.logger.LogFeedbackSubmission(feedback.PromptID, feedback.Success, feedback.ConfidenceUpdate)

	WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":   "ok",
		"message":  "Feedback recorded",
		"feedback": feedback,
	})
}

// ListRegistry lists registry entries
// Delegates to registryHandler.ListRegistry
func (h *Handler) ListRegistry(w http.ResponseWriter, r *http.Request) {
	h.registryHandler.ListRegistry(w, r)
}

// GetRegistryStats returns registry statistics
// Delegates to registryHandler.GetRegistryStats
func (h *Handler) GetRegistryStats(w http.ResponseWriter, r *http.Request) {
	h.registryHandler.GetRegistryStats(w, r)
}

// RebuildRegistry rebuilds the registry index
// Delegates to registryHandler.RebuildRegistryEndpoint
func (h *Handler) RebuildRegistry(w http.ResponseWriter, r *http.Request) {
	h.registryHandler.RebuildRegistryEndpoint(w, r)
}

// SearchRegistry searches the registry
// Delegates to registryHandler.SearchRegistry
func (h *Handler) SearchRegistry(w http.ResponseWriter, r *http.Request) {
	h.registryHandler.SearchRegistry(w, r)
}

// PromotePrompt promotes a prompt to registry
// Delegates to registryHandler.PromotePrompt
func (h *Handler) PromotePrompt(w http.ResponseWriter, r *http.Request) {
	h.registryHandler.PromotePrompt(w, r)
}

// GenerateRelease generates a release manifest
// Delegates to releaseHandler.GenerateRelease
func (h *Handler) GenerateRelease(w http.ResponseWriter, r *http.Request) {
	h.releaseHandler.GenerateRelease(w, r)
}

// PublishRelease publishes a release
// Delegates to releaseHandler.PublishRelease
func (h *Handler) PublishRelease(w http.ResponseWriter, r *http.Request) {
	h.releaseHandler.PublishRelease(w, r)
}

// ListReleases lists all published releases
// Delegates to releaseHandler.ListReleases
func (h *Handler) ListReleases(w http.ResponseWriter, r *http.Request) {
	h.releaseHandler.ListReleases(w, r)
}

// QueryRoute returns high-confidence prompts for Governor routing
// Delegates to governorHandler.QueryRoute
func (h *Handler) QueryRoute(w http.ResponseWriter, r *http.Request) {
	h.governorHandler.QueryRoute(w, r)
}

// RecordFeedback records feedback on prompt usage by Governor
// Delegates to governorHandler.RecordFeedback
func (h *Handler) RecordFeedback(w http.ResponseWriter, r *http.Request) {
	h.governorHandler.RecordFeedback(w, r)
}

// GetRoutingIntelligence returns learned patterns for Governor
// Delegates to governorHandler.GetRoutingIntelligence
func (h *Handler) GetRoutingIntelligence(w http.ResponseWriter, r *http.Request) {
	h.governorHandler.GetRoutingIntelligence(w, r)
}

// GetMetrics returns Prometheus-format metrics
// Delegates to metricsHandler.GetMetrics
func (h *Handler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	h.metricsHandler.GetMetrics(w, r)
}

// ExportTrinityFacts exports accumulated Trinity facts for import
// GET /mcp/prompts/export-trinity
func (h *Handler) ExportTrinityFacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	trinityWriter := NewTrinityWriter(h.dataHome)
	facts, err := trinityWriter.ExportForTrinity()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error exporting Trinity facts: %v", err), http.StatusInternalServerError)
		return
	}

	if facts == "" {
		WriteJSONOK(w, map[string]interface{}{
			"status": "ok",
			"facts":  "",
			"count":  0,
		})
		return
	}

	// Return TSV format ready for Trinity import
	w.Header().Set("Content-Type", "text/tab-separated-values")
	w.Header().Set("Content-Disposition", "attachment; filename=\"trinity-facts.tsv\"")
	// B7: a failed body write must not be silently swallowed.
	if _, err := io.WriteString(w, facts); err != nil {
		h.logger.LogError("export_trinity_write", err)
		return // client gone; do not clear facts we never delivered
	}

	// Clear facts after successful export (optional, can be disabled)
	if clearParam := r.URL.Query().Get("clear"); clearParam == "true" {
		if err := trinityWriter.ClearExported(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clear exported Trinity facts: %v\n", err)
		}
	}
}

// ExportPrompts exports prompts in a portable format
// GET /mcp/prompts/export?format=yaml&domain=router
func (h *Handler) ExportPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}
	domain := r.URL.Query().Get("domain")

	// Load prompts, filtered by domain if specified
	var prompts []models.Prompt
	var err error
	if domain != "" {
		prompts, err = h.loader.LoadByDomain(domain)
	} else {
		prompts, err = h.loader.LoadAll()
	}

	if err != nil {
		h.logger.LogError("export_prompts", err)
		http.Error(w, fmt.Sprintf("Error loading prompts: %v", err), http.StatusInternalServerError)
		return
	}

	// Log export
	h.logger.LogPromptExport(format, domain, len(prompts))

	// Export based on format
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"prompts-%s.json\"", domain))
		WriteJSONOK(w, map[string]interface{}{
			"version": "1.0",
			"domain":  domain,
			"count":   len(prompts),
			"prompts": prompts,
		})
		return
	}

	// YAML export (simple format with metadata).
	// B7: build the whole document first so a serialisation problem is caught
	// before any header goes out, and one write error ends the loop instead of
	// N ignored failures.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Ember Swarm Prompts Export\n")
	fmt.Fprintf(&buf, "# Domain: %s\n", domain)
	fmt.Fprintf(&buf, "# Count: %d\n", len(prompts))
	fmt.Fprintf(&buf, "# Exported: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	for _, p := range prompts {
		fmt.Fprintf(&buf, "---\n%s---\n", p.FrontmatterYAML)
		fmt.Fprintf(&buf, "%s\n\n", p.Content)
	}

	w.Header().Set("Content-Type", "text/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"prompts-%s.yaml\"", domain))
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	if _, err := w.Write(buf.Bytes()); err != nil {
		h.logger.LogError("export_prompts_write", err)
	}
}

// ImportPrompts imports prompts from an external source and merges feedback
// POST /mcp/prompts/import
// Body: JSON array of prompts with feedback observations
func (h *Handler) ImportPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var importData struct {
		Prompts  []models.Prompt   `json:"prompts"`
		Feedback []models.Feedback `json:"feedback,omitempty"`
		Source   string            `json:"source,omitempty"`
	}

	// B3: import is the largest legitimate payload and therefore the prime DoS
	// target; MaxBytesReader caps it at MaxRequestBodyBytes.
	if !DecodeJSONBody(w, r, &importData) {
		return
	}

	if len(importData.Prompts) == 0 {
		http.Error(w, "No prompts provided", http.StatusBadRequest)
		return
	}

	// B6 FIX: validate every prompt BEFORE writing anything. p.Domain and p.ID
	// are joined into filesystem paths by SavePrompt, so a payload carrying
	// Domain="../../../etc" must be rejected outright — and rejected as a
	// client error, not swallowed into a partial import.
	for i := range importData.Prompts {
		p := &importData.Prompts[i]
		if err := ValidatePromptPath(p.ID, p.Domain); err != nil {
			http.Error(w, fmt.Sprintf("Rejected prompt at index %d: %v", i, err), http.StatusBadRequest)
			return
		}
	}

	// Save prompts to filesystem
	importedCount := 0
	feedbackCount := 0

	// Save each prompt to the appropriate directory
	for _, prompt := range importData.Prompts {
		p := prompt // Make a copy
		if err := h.loader.SavePrompt(&p, p.Scope); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving prompt %s: %v\n", p.ID, err)
			continue
		}
		importedCount++

		// Check if this prompt is eligible for promotion and log to Trinity if so
		if eligible, err := h.feedbackManager.CheckPromotion(&p); err == nil && eligible {
			if err := h.feedbackManager.LogPromotion(&p); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to log promotion for %s: %v\n", p.ID, err)
			}
		}
	}

	// Log any feedback observations
	for _, fb := range importData.Feedback {
		if err := h.feedbackManager.SubmitFeedback(&fb); err != nil {
			// Log error but continue with other feedback entries
			fmt.Fprintf(os.Stderr, "Error recording feedback during import: %v\n", err)
			continue
		}
		feedbackCount++
	}

	// Log import
	h.logger.LogPromptImport(len(importData.Prompts), importedCount, len(importData.Prompts)-importedCount)

	WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":            "ok",
		"message":           "Import queued",
		"prompts_received":  importedCount,
		"feedback_received": feedbackCount,
		"source":            importData.Source,
	})
}
