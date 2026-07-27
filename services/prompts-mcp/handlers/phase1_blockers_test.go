package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// seedPromptCorpus writes n valid prompt YAML files under a temp data home and
// returns the data home plus a loader pointed at it.
func seedPromptCorpus(t *testing.T, n int) (string, *PromptLoader) {
	t.Helper()

	dataHome := t.TempDir()
	domainDir := filepath.Join(dataHome, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for i := 0; i < n; i++ {
		body := fmt.Sprintf(`---
id: seed-prompt-%03d
domain: router-prompts
trigger: classify incoming request %d
confidence: 0.85
source: test
scope: project
success_rate: 0.9
---
This is the body of seeded prompt %d. %s
`, i, i, i, strings.Repeat("filler text for realistic parse cost. ", 20))

		path := filepath.Join(domainDir, fmt.Sprintf("seed-prompt-%03d.yaml", i))
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatalf("write seed prompt: %v", err)
		}
	}

	return dataHome, NewPromptLoader(dataHome)
}

// failingWriter is an http.ResponseWriter whose Write fails after failAfter
// bytes, simulating a client that disconnects mid-response.
type failingWriter struct {
	header    http.Header
	status    int
	written   int
	failAfter int
	wroteHdr  bool
}

func newFailingWriter(failAfter int) *failingWriter {
	return &failingWriter{header: make(http.Header), failAfter: failAfter}
}

func (f *failingWriter) Header() http.Header { return f.header }

func (f *failingWriter) WriteHeader(code int) {
	f.status = code
	f.wroteHdr = true
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.written+len(p) > f.failAfter {
		n := f.failAfter - f.written
		if n < 0 {
			n = 0
		}
		f.written += n
		return n, errors.New("connection reset by peer")
	}
	f.written += len(p)
	return len(p), nil
}

// ---------------------------------------------------------------------------
// B3: unbounded POST body (DoS)
// ---------------------------------------------------------------------------

// TestB3OversizeBodyRejectedWith413 drives a body larger than the limit through
// the real middleware chain and asserts 413 rather than an OOM or a 400.
func TestB3OversizeBodyRejectedWith413(t *testing.T) {
	dataHome := t.TempDir()
	h := NewHandler(dataHome)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/prompts/feedback", h.SubmitFeedback)
	srv := httptest.NewServer(Chain(mux, Recover, MaxBytes))
	defer srv.Close()

	// Well-formed JSON, just far too big: a single string field padded past
	// the 10 MiB limit. This is the realistic attack — valid JSON, huge body.
	var buf bytes.Buffer
	buf.WriteString(`{"prompt_id":"x","note":"`)
	buf.Write(bytes.Repeat([]byte("A"), int(MaxRequestBodyBytes)+1024))
	buf.WriteString(`"}`)
	oversize := buf.Len()

	resp, err := http.Post(srv.URL+"/mcp/prompts/feedback", "application/json", &buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize body (%d bytes, limit %d): got status %d, want 413",
			oversize, MaxRequestBodyBytes, resp.StatusCode)
	}
}

// TestB3NormalBodyStillAccepted guards against the limit breaking valid traffic.
func TestB3NormalBodyStillAccepted(t *testing.T) {
	dataHome := t.TempDir()
	h := NewHandler(dataHome)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/prompts/feedback", h.SubmitFeedback)
	srv := httptest.NewServer(Chain(mux, Recover, MaxBytes))
	defer srv.Close()

	body := `{"prompt_id":"seed-prompt-001","agent":"test","success":true}`
	resp, err := http.Post(srv.URL+"/mcp/prompts/feedback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		t.Errorf("normal %d-byte body was rejected as too large", len(body))
	}
}

// TestB3MalformedJSONStillReturns400 checks 413 did not swallow the 400 path.
func TestB3MalformedJSONStillReturns400(t *testing.T) {
	dataHome := t.TempDir()
	h := NewHandler(dataHome)

	req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/feedback",
		strings.NewReader(`{"prompt_id": NOT-JSON}`))
	w := httptest.NewRecorder()
	h.SubmitFeedback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON: got %d, want 400", w.Code)
	}
}

// TestB3ImportEndpointBounded confirms the largest endpoint is covered too.
func TestB3ImportEndpointBounded(t *testing.T) {
	dataHome := t.TempDir()
	h := NewHandler(dataHome)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/prompts/import", h.ImportPrompts)
	srv := httptest.NewServer(Chain(mux, Recover, MaxBytes))
	defer srv.Close()

	var buf bytes.Buffer
	buf.WriteString(`{"source":"attack","prompts":[{"id":"a","domain":"d","content":"`)
	buf.Write(bytes.Repeat([]byte("B"), int(MaxRequestBodyBytes)+1024))
	buf.WriteString(`"}]}`)

	resp, err := http.Post(srv.URL+"/mcp/prompts/import", "application/json", &buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize import: got %d, want 413", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// B7: JSON encoder errors ignored
// ---------------------------------------------------------------------------

// TestB7EncodeErrorBecomes500 uses a payload json cannot marshal. Because
// WriteJSON encodes into a buffer first, no header has gone out yet and a clean
// 500 is still possible — the old json.NewEncoder(w).Encode would have emitted
// 200 plus a truncated body.
func TestB7EncodeErrorBecomes500(t *testing.T) {
	before := EncodeFailures()
	w := httptest.NewRecorder()

	// NaN is not representable in JSON.
	err := WriteJSONOK(w, map[string]any{"bad": math.NaN()})
	if err == nil {
		t.Fatal("expected an encode error for NaN payload, got nil")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("encode failure: got status %d, want 500", w.Code)
	}
	if got := EncodeFailures() - before; got != 1 {
		t.Errorf("encode failure counter: got +%d, want +1", got)
	}
	if strings.Contains(w.Body.String(), "NaN") {
		t.Error("half-encoded payload leaked into the response body")
	}
}

// TestB7WriteErrorLogsAndAbortsConnection covers the other half: headers are
// already on the wire when the client vanishes. The only correct action is to
// log and drop the connection, signalled by panic(http.ErrAbortHandler).
func TestB7WriteErrorLogsAndAbortsConnection(t *testing.T) {
	before := EncodeFailures()
	fw := newFailingWriter(10) // fail almost immediately

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		WriteJSONOK(fw, map[string]any{
			"status":  "ok",
			"prompts": []string{"a", "b", "c", "d", "e", "f", "g"},
		})
	}()

	if recovered != http.ErrAbortHandler {
		t.Errorf("write failure: got panic %v, want http.ErrAbortHandler", recovered)
	}
	if got := EncodeFailures() - before; got != 1 {
		t.Errorf("write failure counter: got +%d, want +1", got)
	}
	if !fw.wroteHdr {
		t.Error("expected header to have been written before the failure")
	}
}

// TestB7RecoverPassesThroughAbortHandler verifies the recovery middleware does
// not convert a deliberate abort into a 500 — that would leave the client
// reading a truncated body followed by a second, contradictory response.
func TestB7RecoverPassesThroughAbortHandler(t *testing.T) {
	h := Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}()

	if recovered != http.ErrAbortHandler {
		t.Errorf("Recover swallowed ErrAbortHandler: got %v", recovered)
	}
}

// TestB7SuccessfulWriteSetsContentLength confirms buffering gives us an exact
// Content-Length instead of forcing chunked encoding.
func TestB7SuccessfulWriteSetsContentLength(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteJSONOK(w, map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("WriteJSONOK: %v", err)
	}
	if got := w.Header().Get("Content-Length"); got == "" {
		t.Error("Content-Length not set")
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Panic recovery
// ---------------------------------------------------------------------------

func TestPanicRecoveryReturns500(t *testing.T) {
	before := PanicsRecovered()

	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		var p *models.Prompt
		_ = p.ID // nil dereference, the realistic accident
	})
	srv := httptest.NewServer(Chain(mux, Recover, MaxBytes))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/boom")
	if err != nil {
		t.Fatalf("get: %v", err) // pre-fix this is "EOF": connection dropped
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("panicking handler: got %d, want 500", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Errorf("500 body is not valid JSON: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("500 body: got %v, want status=error", body)
	}
	if got := PanicsRecovered() - before; got != 1 {
		t.Errorf("panic counter: got +%d, want +1", got)
	}
}

// TestPanicRecoveryServerSurvives is the point of the whole exercise: a panic
// must not take down subsequent requests.
func TestPanicRecoveryServerSurvives(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("deliberate")
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		WriteJSONOK(w, map[string]string{"status": "ok"})
	})
	srv := httptest.NewServer(Chain(mux, Recover, MaxBytes))
	defer srv.Close()

	for i := 0; i < 5; i++ {
		resp, err := http.Get(srv.URL + "/boom")
		if err != nil {
			t.Fatalf("boom %d: %v", i, err)
		}
		resp.Body.Close()
	}

	resp, err := http.Get(srv.URL + "/ok")
	if err != nil {
		t.Fatalf("healthy request after panics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after 5 panics: got %d, want 200", resp.StatusCode)
	}
}

// TestPanicRecoveryConcurrent ensures the recovery path is itself race-free.
func TestPanicRecoveryConcurrent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("concurrent boom")
	})
	srv := httptest.NewServer(Chain(mux, Recover, MaxBytes))
	defer srv.Close()

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/boom")
			if err != nil {
				t.Errorf("concurrent get: %v", err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusInternalServerError {
				t.Errorf("got %d, want 500", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// LoadAll caching
// ---------------------------------------------------------------------------

func TestCacheHitOnRepeatedLoadAll(t *testing.T) {
	_, loader := seedPromptCorpus(t, 50)

	first, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(first) != 50 {
		t.Fatalf("seeded 50 prompts, loaded %d", len(first))
	}

	const repeats = 100
	for i := 0; i < repeats; i++ {
		if _, err := loader.LoadAll(); err != nil {
			t.Fatalf("LoadAll %d: %v", i, err)
		}
	}

	st := loader.CacheStats()
	if st.Misses != 1 {
		t.Errorf("misses: got %d, want 1 (only the cold load)", st.Misses)
	}
	if st.Hits != repeats {
		t.Errorf("hits: got %d, want %d", st.Hits, repeats)
	}
	if st.HitRate < 0.99 {
		t.Errorf("hit rate: got %.4f, want >= 0.99", st.HitRate)
	}
}

// TestCacheServesDerivedReads proves the win reaches the endpoints, not just
// LoadAll: LoadByID/Search/LoadByDomain all funnel through the same cache.
func TestCacheServesDerivedReads(t *testing.T) {
	_, loader := seedPromptCorpus(t, 20)

	if _, err := loader.LoadByID("seed-prompt-001"); err != nil {
		t.Fatalf("LoadByID: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := loader.LoadByID("seed-prompt-001"); err != nil {
			t.Fatalf("LoadByID: %v", err)
		}
		if _, err := loader.Search("prompt", ""); err != nil {
			t.Fatalf("Search: %v", err)
		}
		if _, err := loader.LoadByDomain("router-prompts"); err != nil {
			t.Fatalf("LoadByDomain: %v", err)
		}
	}

	st := loader.CacheStats()
	if st.Misses != 1 {
		t.Errorf("derived reads caused %d disk loads, want 1", st.Misses)
	}
}

// TestCacheExpiresAfterTTL verifies staleness is actually bounded.
func TestCacheExpiresAfterTTL(t *testing.T) {
	_, loader := seedPromptCorpus(t, 5)
	loader.SetCacheTTL(50 * time.Millisecond)

	if _, err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got := loader.CacheStats().Misses; got != 1 {
		t.Fatalf("within TTL: got %d misses, want 1", got)
	}

	time.Sleep(80 * time.Millisecond)

	if _, err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll after TTL: %v", err)
	}
	if got := loader.CacheStats().Misses; got != 2 {
		t.Errorf("after TTL expiry: got %d misses, want 2", got)
	}
}

// TestCacheInvalidatedOnWrite is the correctness guard on the whole scheme:
// a caller must never read back a stale version of its own write.
func TestCacheInvalidatedOnWrite(t *testing.T) {
	_, loader := seedPromptCorpus(t, 5)

	if _, err := loader.LoadAll(); err != nil {
		t.Fatalf("warm cache: %v", err)
	}

	now := time.Now().UTC()
	p := &models.Prompt{
		ID:         "brand-new-prompt",
		Domain:     "router-prompts",
		Content:    "freshly written",
		Confidence: 0.9,
		Scope:      "project",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := loader.SavePrompt(p, "project"); err != nil {
		t.Fatalf("SavePrompt: %v", err)
	}

	got, err := loader.LoadByID("brand-new-prompt")
	if err != nil {
		t.Fatalf("read-your-writes violated: %v", err)
	}
	if got.Content != "freshly written" {
		t.Errorf("content: got %q, want %q", got.Content, "freshly written")
	}
	if loader.CacheStats().Evictions == 0 {
		t.Error("SavePrompt did not evict the cache")
	}
}

// TestCacheReturnsIndependentCopies makes sure one caller mutating its result
// cannot corrupt the shared entry for everyone else.
func TestCacheReturnsIndependentCopies(t *testing.T) {
	_, loader := seedPromptCorpus(t, 10)

	a, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	a[0].ID = "MUTATED"

	b, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if b[0].ID == "MUTATED" {
		t.Error("caller mutation leaked into the cached entry")
	}
}

// TestCacheConcurrentReads exercises the RWMutex under -race.
func TestCacheConcurrentReads(t *testing.T) {
	_, loader := seedPromptCorpus(t, 30)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := loader.LoadAll(); err != nil {
				t.Errorf("concurrent LoadAll: %v", err)
			}
			if i%10 == 0 {
				loader.InvalidateCache()
			}
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Nil slice preallocation
// ---------------------------------------------------------------------------

// TestListPromptsEmptyResultIsArrayNotNull is the user-visible half of the
// preallocation fix: a nil slice marshals to null and breaks clients that
// expect to iterate.
func TestListPromptsEmptyResultIsArrayNotNull(t *testing.T) {
	dataHome, _ := seedPromptCorpus(t, 3)
	h := NewHandler(dataHome)

	req := httptest.NewRequest(http.MethodGet, "/mcp/prompts/list?domain=does-not-exist", nil)
	w := httptest.NewRecorder()
	h.ListPrompts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), `"prompts":null`) {
		t.Error(`empty result serialised as "prompts":null, want []`)
	}

	var resp struct {
		Count   int             `json:"count"`
		Prompts []models.Prompt `json:"prompts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Prompts == nil {
		t.Error("prompts decoded as nil, want empty array")
	}
	if resp.Count != 0 {
		t.Errorf("count: got %d, want 0", resp.Count)
	}
}

func TestListPromptsReturnsSeededPrompts(t *testing.T) {
	dataHome, _ := seedPromptCorpus(t, 12)
	h := NewHandler(dataHome)

	req := httptest.NewRequest(http.MethodGet, "/mcp/prompts/list", nil)
	w := httptest.NewRecorder()
	h.ListPrompts(w, req)

	var resp struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 12 {
		t.Errorf("count: got %d, want 12", resp.Count)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks — before/after evidence for the caching + preallocation fixes
// ---------------------------------------------------------------------------

func BenchmarkLoadAllCached(b *testing.B) {
	loader := benchLoader(b, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := loader.LoadAll(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadAllUncached(b *testing.B) {
	loader := benchLoader(b, 100)
	loader.SetCacheTTL(0) // disable cache: this is the pre-fix behaviour
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := loader.LoadAll(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListPromptsCached(b *testing.B) {
	dataHome := benchDataHome(b, 100)
	h := NewHandler(dataHome)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		h.ListPrompts(w, httptest.NewRequest(http.MethodGet, "/mcp/prompts/list", nil))
	}
}

func BenchmarkListPromptsUncached(b *testing.B) {
	dataHome := benchDataHome(b, 100)
	h := NewHandler(dataHome)
	h.loader.SetCacheTTL(0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		h.ListPrompts(w, httptest.NewRequest(http.MethodGet, "/mcp/prompts/list", nil))
	}
}

func benchDataHome(b *testing.B, n int) string {
	b.Helper()
	dataHome := b.TempDir()
	domainDir := filepath.Join(dataHome, "ecc-prompts", "instincts", "personal", "router-prompts")
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(`---
id: bench-prompt-%03d
domain: router-prompts
trigger: bench %d
confidence: 0.85
scope: project
---
%s
`, i, i, strings.Repeat("bench body content. ", 40))
		if err := os.WriteFile(filepath.Join(domainDir, fmt.Sprintf("bench-%03d.yaml", i)), []byte(body), 0644); err != nil {
			b.Fatal(err)
		}
	}
	return dataHome
}

func benchLoader(b *testing.B, n int) *PromptLoader {
	return NewPromptLoader(benchDataHome(b, n))
}
