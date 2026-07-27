package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// ReleaseHandler manages registry releases (GitHub releases)
type ReleaseHandler struct {
	registryHandler *RegistryHandler
	releaseDir      string
	logger          *Logger
}

// NewReleaseHandler creates a new release handler
func NewReleaseHandler(dataHome string, registryHandler *RegistryHandler, logger *Logger) *ReleaseHandler {
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local/share")
	}
	releaseDir := filepath.Join(dataHome, "ecc-prompts", "releases")
	return &ReleaseHandler{
		registryHandler: registryHandler,
		releaseDir:      releaseDir,
		logger:          logger,
	}
}

// GenerateRelease creates a release manifest from current registry
// GET /mcp/prompts/release/generate?version=v0.1.0&changelog=...
func (rh *ReleaseHandler) GenerateRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	version := r.URL.Query().Get("version")
	if version == "" {
		http.Error(w, "Missing required parameter: version", http.StatusBadRequest)
		return
	}

	changelog := r.URL.Query().Get("changelog")
	if changelog == "" {
		changelog = "Initial release"
	}

	// Get current registry
	index, err := rh.registryHandler.GetIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Calculate stats
	var totalConfidence float32
	var totalSuccessRate float32
	domainCounts := make(map[string]int)
	highlighted := []models.HighlightedPrompt{}

	for _, entry := range index.Prompts {
		totalConfidence += entry.Confidence
		totalSuccessRate += entry.SuccessRate
		domainCounts[entry.Domain]++

		// Highlight top 3 highest-confidence prompts
		if len(highlighted) < 3 {
			highlighted = append(highlighted, models.HighlightedPrompt{
				ID:         entry.ID,
				Domain:     entry.Domain,
				Confidence: entry.Confidence,
				Summary:    fmt.Sprintf("Confidence: %.2f | Success Rate: %.2f%% | Agents: %d", entry.Confidence, entry.SuccessRate*100, len(entry.AgentsTested)),
			})
		}
	}

	if len(index.Prompts) > 0 {
		totalConfidence /= float32(len(index.Prompts))
		totalSuccessRate /= float32(len(index.Prompts))
	}

	manifest := models.ReleaseManifest{
		Version:            version,
		Name:               fmt.Sprintf("Prompts Registry %s", version),
		Description:        fmt.Sprintf("Published %d prompts across %d domains", len(index.Prompts), len(domainCounts)),
		ReleasedAt:         time.Now().UTC(),
		PromptCount:        len(index.Prompts),
		DomainCounts:       domainCounts,
		AvgConfidence:      totalConfidence,
		AvgSuccessRate:     totalSuccessRate,
		Changelog:          changelog,
		HighlightedPrompts: highlighted,
	}

	// Log release generation
	rh.logger.LogReleaseGeneration(version, len(index.Prompts))

	WriteJSONOK(w, manifest)
}

// PublishRelease saves a release manifest and creates a publishable archive
// POST /mcp/prompts/release/publish
func (rh *ReleaseHandler) PublishRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Version   string `json:"version"`
		Changelog string `json:"changelog"`
	}

	// B3: bounded read; oversize bodies get 413, malformed JSON gets 400.
	if !DecodeJSONBody(w, r, &req) {
		return
	}

	if req.Version == "" {
		http.Error(w, "Missing required field: version", http.StatusBadRequest)
		return
	}

	// Create release directory if needed
	if err := os.MkdirAll(rh.releaseDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Error creating release directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Get current registry
	index, err := rh.registryHandler.GetIndex()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting registry: %v", err), http.StatusInternalServerError)
		return
	}

	// Create release directory for this version
	versionDir := filepath.Join(rh.releaseDir, req.Version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Error creating version directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Calculate stats
	var totalConfidence float32
	var totalSuccessRate float32
	domainCounts := make(map[string]int)

	for _, entry := range index.Prompts {
		totalConfidence += entry.Confidence
		totalSuccessRate += entry.SuccessRate
		domainCounts[entry.Domain]++
	}

	if len(index.Prompts) > 0 {
		totalConfidence /= float32(len(index.Prompts))
		totalSuccessRate /= float32(len(index.Prompts))
	}

	// Create manifest
	manifest := models.ReleaseManifest{
		Version:        req.Version,
		Name:           fmt.Sprintf("Prompts Registry %s", req.Version),
		Description:    fmt.Sprintf("Published %d prompts across %d domains", len(index.Prompts), len(domainCounts)),
		ReleasedAt:     time.Now().UTC(),
		PromptCount:    len(index.Prompts),
		DomainCounts:   domainCounts,
		AvgConfidence:  totalConfidence,
		AvgSuccessRate: totalSuccessRate,
		Changelog:      req.Changelog,
	}

	// Save manifest
	manifestPath := filepath.Join(versionDir, "manifest.json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error marshaling manifest: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Error saving manifest: %v", err), http.StatusInternalServerError)
		return
	}

	// Save prompts as JSON export
	promptsPath := filepath.Join(versionDir, "prompts.json")
	promptsData := map[string]any{
		"version":      req.Version,
		"released_at":  time.Now().UTC(),
		"prompt_count": len(index.Prompts),
		"prompts":      index.Prompts,
	}

	promptsJSON, err := json.MarshalIndent(promptsData, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error marshaling prompts: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(promptsPath, promptsJSON, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Error saving prompts: %v", err), http.StatusInternalServerError)
		return
	}

	// Log release publishing
	rh.logger.LogReleasePublish(req.Version, len(index.Prompts), manifestPath)

	WriteJSONOK(w, map[string]any{
		"status":         "published",
		"version":        req.Version,
		"prompt_count":   len(index.Prompts),
		"domains":        len(domainCounts),
		"released_at":    time.Now().UTC(),
		"manifest_path":  manifestPath,
		"prompts_path":   promptsPath,
		"avg_confidence": totalConfidence,
	})
}

// ListReleases lists all published releases
// GET /mcp/prompts/release/list
func (rh *ReleaseHandler) ListReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if releases directory exists
	if _, err := os.Stat(rh.releaseDir); os.IsNotExist(err) {
		WriteJSONOK(w, map[string]any{
			"status":   "ok",
			"releases": []string{},
			"count":    0,
		})
		return
	}

	// List directories in release dir
	entries, err := os.ReadDir(rh.releaseDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading releases: %v", err), http.StatusInternalServerError)
		return
	}

	var releases []string
	for _, entry := range entries {
		if entry.IsDir() {
			releases = append(releases, entry.Name())
		}
	}

	// Log releases listing (use a release version placeholder for logging)
	if len(releases) > 0 {
		rh.logger.LogReleaseGeneration("list", len(releases))
	}

	WriteJSONOK(w, map[string]any{
		"status":   "ok",
		"releases": releases,
		"count":    len(releases),
	})
}
