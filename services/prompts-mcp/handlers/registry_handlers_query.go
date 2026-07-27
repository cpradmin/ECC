package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/cpradmin/prompts-mcp/models"
)

// ListRegistry lists all registry entries with filtering and pagination
// GET /mcp/prompts/registry?domain=router&min_confidence=0.8&limit=50&offset=0&sort=confidence
func (rh *RegistryHandler) ListRegistry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get or rebuild index
	index, err := rh.GetIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse query parameters
	domain := r.URL.Query().Get("domain")
	minConfidenceStr := r.URL.Query().Get("min_confidence")
	minConfidence := float32(0.7) // default
	if minConfidenceStr != "" {
		if val, err := strconv.ParseFloat(minConfidenceStr, 32); err == nil {
			minConfidence = float32(val)
		}
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 50 // default
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 && val <= 1000 {
			limit = val
		}
	}

	offsetStr := r.URL.Query().Get("offset")
	offset := 0
	if offsetStr != "" {
		if val, err := strconv.Atoi(offsetStr); err == nil && val >= 0 {
			offset = val
		}
	}

	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "confidence"
	}

	// No lock needed: GetIndex returned a private deep copy. Taking rh.mu here
	// only added contention (and, in PromotePrompt, a self-deadlock).

	// Filter prompts
	filtered := index.Prompts

	// Apply domain filter
	if domain != "" {
		filtered = models.FilterByDomain(filtered, domain)
	}

	// Apply confidence filter
	filtered = models.FilterByConfidence(filtered, minConfidence)

	// Apply sort (already sorted by confidence in BuildIndex, but apply requested sort).
	// PERF FIX: these were hand-rolled bubble sorts — O(n^2) comparisons and
	// O(n^2) swaps on every request. sort.SliceStable is O(n log n) and keeps the
	// existing confidence ordering as the tiebreaker, which the bubble sort also
	// happened to do (it was stable).
	switch sortBy {
	case "success_rate":
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].SuccessRate > filtered[j].SuccessRate
		})
	case "updated":
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		})
	}
	// default: confidence (already sorted by BuildIndex)

	// Apply pagination
	total := len(filtered)
	if offset >= total {
		filtered = []models.RegistryEntry{}
	} else if offset+limit >= total {
		filtered = filtered[offset:]
	} else {
		filtered = filtered[offset : offset+limit]
	}

	WriteJSONOK(w, map[string]any{
		"status":  "ok",
		"total":   total,
		"limit":   limit,
		"offset":  offset,
		"domain":  domain,
		"prompts": filtered,
	})
}

// GetRegistryStats returns registry metrics
// GET /mcp/prompts/registry/stats
func (rh *RegistryHandler) GetRegistryStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	index, err := rh.GetIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	// No lock needed: index is a private deep copy returned by GetIndex.

	// Calculate average confidence and success rate
	var totalConfidence float32
	var totalSuccessRate float32
	if len(index.Prompts) > 0 {
		for _, p := range index.Prompts {
			totalConfidence += p.Confidence
			totalSuccessRate += p.SuccessRate
		}
		totalConfidence /= float32(len(index.Prompts))
		totalSuccessRate /= float32(len(index.Prompts))
	}

	// Build domain summary
	domainCounts := make(map[string]int)
	for _, p := range index.Prompts {
		domainCounts[p.Domain]++
	}

	WriteJSONOK(w, map[string]any{
		"status":               "ok",
		"total_prompts":        index.TotalPrompts,
		"domains":              len(index.Domains),
		"average_confidence":   totalConfidence,
		"average_success_rate": totalSuccessRate,
		"last_update":          index.GeneratedAt,
		"prompts_by_domain":    domainCounts,
	})
}

// SearchRegistry performs keyword + semantic search across registry
// GET /mcp/prompts/registry/search?query=confidence&domain=router&limit=20
func (rh *RegistryHandler) SearchRegistry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	index, err := rh.GetIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, "Missing required parameter: query", http.StatusBadRequest)
		return
	}

	domain := r.URL.Query().Get("domain")
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	// No lock needed: index is a private deep copy returned by GetIndex.

	// Search: keyword matching in ID and snippet text
	type searchResult struct {
		ID             string  `json:"id"`
		Domain         string  `json:"domain"`
		RelevanceScore float32 `json:"relevance_score"`
		Confidence     float32 `json:"confidence"`
		SuccessRate    float32 `json:"success_rate"`
		Snippet        string  `json:"snippet"`
	}

	results := []searchResult{}
	queryLower := strings.ToLower(query)

	for _, entry := range index.Prompts {
		// Skip if domain filter doesn't match
		if domain != "" && entry.Domain != domain {
			continue
		}

		// Simple keyword matching: score based on keyword presence in ID
		score := float32(0)
		if strings.Contains(strings.ToLower(entry.ID), queryLower) {
			score = 0.95 // High score for ID match
		} else if strings.Contains(strings.ToLower(entry.Domain), queryLower) {
			score = 0.7 // Medium score for domain match
		}

		// If no match, skip
		if score == 0 {
			continue
		}

		result := searchResult{
			ID:             entry.ID,
			Domain:         entry.Domain,
			RelevanceScore: score,
			Confidence:     entry.Confidence,
			SuccessRate:    entry.SuccessRate,
			Snippet:        fmt.Sprintf("Domain: %s | Confidence: %.2f | Success Rate: %.2f | Agents: %d", entry.Domain, entry.Confidence, entry.SuccessRate, len(entry.AgentsTested)),
		}
		results = append(results, result)
	}

	// Sort by relevance score (descending).
	// PERF FIX: was a bubble sort, O(n^2) on every search request.
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	WriteJSONOK(w, map[string]any{
		"status":  "ok",
		"query":   query,
		"domain":  domain,
		"results": results,
	})
}
