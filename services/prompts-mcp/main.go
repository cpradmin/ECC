package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/cpradmin/prompts-mcp/handlers"
	"github.com/joho/godotenv"
)

const (
	DefaultAddr = ":8762"
	MCPPath     = "/mcp"
)

func main() {
	// Load environment
	_ = godotenv.Load()
	addr := os.Getenv("PROMPTS_MCP_ADDR")
	if addr == "" {
		addr = DefaultAddr
	}

	// Fail fast on a broken alert matrix (blocker 3).
	//
	// A missing runbook or a threshold on a metric nobody emits is a silent,
	// permanent hole in the on-call path: the alert fires, the responder clicks
	// the link, and lands on a 404 at 3am. Validate before we serve traffic so
	// the failure surfaces at deploy time, not during an incident.
	repoRoot := os.Getenv("PROMPTS_MCP_REPO_ROOT")
	if repoRoot == "" {
		if wd, err := os.Getwd(); err == nil {
			repoRoot = wd
		}
	}
	if err := handlers.ValidateAlertMatrix(repoRoot); err != nil {
		log.Fatalf("startup aborted: %v\n(set PROMPTS_MCP_REPO_ROOT if runbooks live outside the working directory)", err)
	}
	log.Printf("alert matrix validated: %d thresholds, all runbooks present (root=%s)", len(handlers.AlertMatrix), repoRoot)

	// Initialize handlers
	h := handlers.NewHandler(os.Getenv("XDG_DATA_HOME"))

	// Register routes
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc(MCPPath+"/health", healthHandler)

	// Metrics (Prometheus format)
	mux.HandleFunc(MCPPath+"/metrics", h.GetMetrics)

	// Prompts API
	mux.HandleFunc(MCPPath+"/prompts/list", h.ListPrompts)
	mux.HandleFunc(MCPPath+"/prompts/get", h.GetPrompt)
	mux.HandleFunc(MCPPath+"/prompts/search", h.SearchPrompts)
	mux.HandleFunc(MCPPath+"/prompts/feedback", h.SubmitFeedback)
	mux.HandleFunc(MCPPath+"/prompts/export", h.ExportPrompts)
	mux.HandleFunc(MCPPath+"/prompts/export-trinity", h.ExportTrinityFacts)
	mux.HandleFunc(MCPPath+"/prompts/import", h.ImportPrompts)

	// Registry API
	mux.HandleFunc(MCPPath+"/prompts/registry", h.ListRegistry)
	mux.HandleFunc(MCPPath+"/prompts/registry/search", h.SearchRegistry)
	mux.HandleFunc(MCPPath+"/prompts/registry/stats", h.GetRegistryStats)
	mux.HandleFunc(MCPPath+"/prompts/registry/rebuild", h.RebuildRegistry)
	mux.HandleFunc(MCPPath+"/prompts/registry/promote", h.PromotePrompt)

	// Release API (GitHub Releases)
	mux.HandleFunc(MCPPath+"/prompts/release/generate", h.GenerateRelease)
	mux.HandleFunc(MCPPath+"/prompts/release/publish", h.PublishRelease)
	mux.HandleFunc(MCPPath+"/prompts/release/list", h.ListReleases)

	// Governor API (Phase 5: Routing Integration)
	mux.HandleFunc(MCPPath+"/prompts/governor/route", h.QueryRoute)
	mux.HandleFunc(MCPPath+"/prompts/governor/feedback", h.RecordFeedback)
	mux.HandleFunc(MCPPath+"/prompts/governor/intelligence", h.GetRoutingIntelligence)

	// Middleware chain, outermost first.
	//
	// Recover must be outermost so it also catches panics raised inside MaxBytes
	// and inside the mux's own routing. MaxBytes sits above every route so no
	// endpoint can be registered without body limiting — putting it on
	// individual POST handlers would make it a thing you have to remember.
	handler := handlers.Chain(mux,
		handlers.Recover,  // 500 instead of a dropped connection
		handlers.MaxBytes, // B3: cap request bodies at 10 MiB
	)

	// Create server
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
		// Bound header size too: MaxBytesReader only covers the body.
		MaxHeaderBytes: 1 << 20, // 1 MiB
	}

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	log.Printf("prompts-mcp listening on %s%s", addr, MCPPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

// healthHandler responds with server health status
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handlers.WriteJSONOK(w, map[string]string{
		"status": "healthy",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}
