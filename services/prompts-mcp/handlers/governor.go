package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// GovernorHandler manages Governor routing integration
type GovernorHandler struct {
	registryHandler *RegistryHandler
	loader          *PromptLoader
	feedbackManager *FeedbackManager
	logger          *Logger
}

// NewGovernorHandler creates a new governor handler
func NewGovernorHandler(registryHandler *RegistryHandler, loader *PromptLoader, feedbackManager *FeedbackManager, logger *Logger) *GovernorHandler {
	return &GovernorHandler{
		registryHandler: registryHandler,
		loader:          loader,
		feedbackManager: feedbackManager,
		logger:          logger,
	}
}

// RoutingRecommendation is a prompt recommendation for Governor routing
type RoutingRecommendation struct {
	PromptID    string  `json:"prompt_id"`
	Domain      string  `json:"domain"`
	Confidence  float32 `json:"confidence"`
	SuccessRate float32 `json:"success_rate"`
	Reason      string  `json:"reason"`
	Version     string  `json:"version"`
}

// QueryRoute returns high-confidence prompts for a task type
// GET /mcp/prompts/governor/route?task_type=routing&domain=router&min_confidence=0.8&limit=3
func (gh *GovernorHandler) QueryRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskType := r.URL.Query().Get("task_type")
	domain := r.URL.Query().Get("domain")
	minConfidenceStr := r.URL.Query().Get("min_confidence")
	minConfidence := float32(0.8) // default
	if minConfidenceStr != "" {
		if val, err := strconv.ParseFloat(minConfidenceStr, 32); err == nil {
			minConfidence = float32(val)
		}
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 3
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 && val <= 10 {
			limit = val
		}
	}

	// Get registry
	index, err := gh.registryHandler.GetIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Filter by domain if specified
	var candidates []models.RegistryEntry
	for _, entry := range index.Prompts {
		if entry.Confidence < minConfidence {
			continue
		}
		if domain != "" && entry.Domain != domain {
			continue
		}
		candidates = append(candidates, entry)
	}

	// Sort by success_rate (descending), then confidence (descending)
	for i := len(candidates) - 1; i > 0; i-- {
		for j := 0; j < i; j++ {
			if candidates[j].SuccessRate < candidates[j+1].SuccessRate {
				candidates[j], candidates[j+1] = candidates[j+1], candidates[j]
			} else if candidates[j].SuccessRate == candidates[j+1].SuccessRate && candidates[j].Confidence < candidates[j+1].Confidence {
				candidates[j], candidates[j+1] = candidates[j+1], candidates[j]
			}
		}
	}

	// Apply limit
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	// Build recommendations
	recommendations := []RoutingRecommendation{}
	for _, entry := range candidates {
		rec := RoutingRecommendation{
			PromptID:    entry.ID,
			Domain:      entry.Domain,
			Confidence:  entry.Confidence,
			SuccessRate: entry.SuccessRate,
			Version:     entry.CurrentVersion,
			Reason: fmt.Sprintf(
				"High success rate on %s tasks (%.1f%%), confidence %.2f, tested by %d agents",
				entry.Domain, entry.SuccessRate*100, entry.Confidence, len(entry.AgentsTested),
			),
		}
		recommendations = append(recommendations, rec)
	}

	// Generate routing session ID using crypto/rand
	sessionBytes := make([]byte, 8)
	if _, err := rand.Read(sessionBytes); err != nil {
		http.Error(w, fmt.Sprintf("Error generating session ID: %v", err), http.StatusInternalServerError)
		return
	}
	routingSession := fmt.Sprintf("gov-sess-%s", hex.EncodeToString(sessionBytes))

	// Log routing query
	gh.logger.LogGovernorQuery(taskType, domain, len(recommendations), routingSession)

	WriteJSONOK(w, map[string]any{
		"status":          "ok",
		"task_type":       taskType,
		"domain":          domain,
		"recommendations": recommendations,
		"count":           len(recommendations),
		"routing_session": routingSession,
		"min_confidence":  minConfidence,
		"queried_at":      time.Now().UTC(),
	})
}

// RecordFeedback records feedback on a prompt used by Governor
// POST /mcp/prompts/governor/feedback
func (gh *GovernorHandler) RecordFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RoutingSession string   `json:"routing_session"`
		PromptID       string   `json:"prompt_id"`
		TaskType       string   `json:"task_type"`
		Success        bool     `json:"success"`
		Reasoning      string   `json:"reasoning"`
		AgentsInvolved []string `json:"agents_involved"`
	}

	// B3: bounded read; oversize bodies get 413, malformed JSON gets 400.
	if !DecodeJSONBody(w, r, &req) {
		return
	}

	if req.PromptID == "" {
		http.Error(w, "Missing required field: prompt_id", http.StatusBadRequest)
		return
	}

	// Generate secure feedback ID
	feedbackIDBytes := make([]byte, 8)
	if _, err := rand.Read(feedbackIDBytes); err != nil {
		http.Error(w, fmt.Sprintf("Error generating feedback ID: %v", err), http.StatusInternalServerError)
		return
	}

	// Record feedback
	feedback := models.Feedback{
		ID:        fmt.Sprintf("gov-feedback-%s", hex.EncodeToString(feedbackIDBytes)),
		PromptID:  req.PromptID,
		Agent:     "governor",
		Task:      req.TaskType,
		Success:   req.Success,
		Note:      req.Reasoning,
		Timestamp: time.Now().UTC(),
	}

	// Apply confidence update
	if req.Success {
		feedback.ConfidenceUpdate = 0.05 // +0.05 for success
	} else {
		feedback.ConfidenceUpdate = -0.10 // -0.10 for failure
	}

	// Submit feedback. B1: this now applies the confidence delta atomically.
	// B5: an unknown prompt_id is rejected here rather than silently recorded.
	if err := gh.feedbackManager.SubmitFeedbackContext(r.Context(), &feedback); err != nil {
		http.Error(w, fmt.Sprintf("Error recording feedback: %v", err), feedbackErrorStatus(err))
		return
	}

	// Check context before loading all prompts
	ctx := r.Context()
	select {
	case <-ctx.Done():
		http.Error(w, "Request cancelled", http.StatusRequestTimeout)
		return
	default:
	}

	// Get updated prompt to return new confidence
	allPrompts, err := gh.loader.LoadAll()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error loading prompts: %v", err), http.StatusInternalServerError)
		return
	}

	var updatedPrompt *models.Prompt
	for i := range allPrompts {
		if allPrompts[i].ID == req.PromptID {
			updatedPrompt = &allPrompts[i]
			break
		}
	}

	newConfidence := float32(0)
	if updatedPrompt != nil {
		newConfidence = updatedPrompt.Confidence
	}

	// Calculate improvement percentage
	improvementPct := float32(0)
	if newConfidence > 0 {
		// Simple calculation: (new - old) / old * 100
		// where old is estimated from confidence_update
		oldConfidence := newConfidence - feedback.ConfidenceUpdate
		if oldConfidence > 0 {
			improvementPct = ((newConfidence - oldConfidence) / oldConfidence) * 100
		}
	}

	// Log governor feedback
	gh.logger.LogGovernorFeedback(req.RoutingSession, req.PromptID, req.Success, newConfidence)

	WriteJSONOK(w, map[string]any{
		"status":              "feedback_recorded",
		"prompt_id":           req.PromptID,
		"routing_session":     req.RoutingSession,
		"task_type":           req.TaskType,
		"success":             req.Success,
		"new_confidence":      newConfidence,
		"confidence_delta":    feedback.ConfidenceUpdate,
		"routing_improvement": fmt.Sprintf("%+.1f%%", improvementPct),
		"timestamp":           time.Now().UTC(),
	})
}

// GetRoutingIntelligence returns learned patterns from Trinity
// GET /mcp/prompts/governor/intelligence
func (gh *GovernorHandler) GetRoutingIntelligence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get registry for current state
	index, err := gh.registryHandler.GetIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Analyze domain patterns
	domainPatterns := make(map[string]map[string]any)
	var highConfidenceDomains []string
	var lowConfidenceDomains []string

	for domain, stats := range index.Domains {
		pattern := map[string]any{
			"success_rate":   stats.LatestSuccessRate,
			"avg_confidence": stats.AverageConfidence,
			"prompt_count":   stats.Count,
		}
		domainPatterns[domain] = pattern

		// Categorize by confidence
		if stats.AverageConfidence >= 0.8 {
			highConfidenceDomains = append(highConfidenceDomains, domain)
		} else if stats.AverageConfidence < 0.5 {
			lowConfidenceDomains = append(lowConfidenceDomains, domain)
		}
	}

	// Count prompts by task type (proxy using domain)
	taskTypes := []string{}
	for domain := range domainPatterns {
		taskTypes = append(taskTypes, domain)
	}

	// Log routing intelligence query
	gh.logger.LogGovernorQuery("intelligence", "", len(highConfidenceDomains), "")

	WriteJSONOK(w, map[string]any{
		"status":                  "ok",
		"task_types":              taskTypes,
		"domain_patterns":         domainPatterns,
		"high_confidence_domains": highConfidenceDomains,
		"low_confidence_domains":  lowConfidenceDomains,
		"total_prompts":           index.TotalPrompts,
		"analyzed_at":             time.Now().UTC(),
	})
}
