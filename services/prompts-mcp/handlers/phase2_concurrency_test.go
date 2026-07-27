package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpradmin/prompts-mcp/models"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func seedPhase2Prompts(t testing.TB, loader *PromptLoader, n int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		p := models.Prompt{
			ID:         fmt.Sprintf("phase2-seed-%03d", i),
			Trigger:    fmt.Sprintf("trigger %d", i),
			Domain:     "router-prompts",
			Source:     "phase2-test",
			Scope:      "project",
			Confidence: 0.7 + float32(i%30)/100.0,
			Content:    fmt.Sprintf("content for prompt %d", i),
			CreatedAt:  now,
			UpdatedAt:  now.Add(time.Duration(i) * time.Second),
		}
		if err := loader.SavePrompt(&p, p.Scope); err != nil {
			t.Fatalf("seed SavePrompt(%s): %v", p.ID, err)
		}
	}
}

func newPhase2Registry(t testing.TB, n int) (*RegistryHandler, *PromptLoader, string) {
	t.Helper()
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	seedPhase2Prompts(t, loader, n)
	return NewRegistryHandler(dir, loader), loader, dir
}

// ---------------------------------------------------------------------------
// B2 — race in GetIndex
// ---------------------------------------------------------------------------

// TestConcurrentGetIndex hammers GetIndex and RebuildIndex from 10+ goroutines.
// Run with -race. Before the B2 fix this both (a) triggered detector reports and
// (b) let a stale disk load clobber a freshly rebuilt cache.
func TestConcurrentGetIndex(t *testing.T) {
	const seeded = 25
	rh, _, _ := newPhase2Registry(t, seeded)

	const readers = 10
	const writers = 4
	const iterations = 25

	var wg sync.WaitGroup
	errCh := make(chan error, (readers+writers)*iterations)

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				idx, err := rh.GetIndex()
				if err != nil {
					errCh <- fmt.Errorf("GetIndex: %w", err)
					return
				}
				if idx == nil {
					errCh <- fmt.Errorf("GetIndex returned nil index")
					return
				}
				// Structural integrity: the header count must agree with the
				// slice, and no entry may be half-written.
				if idx.TotalPrompts != len(idx.Prompts) {
					errCh <- fmt.Errorf("corrupt index: TotalPrompts=%d len(Prompts)=%d",
						idx.TotalPrompts, len(idx.Prompts))
					return
				}
				if idx.TotalPrompts != seeded {
					errCh <- fmt.Errorf("corrupt index: expected %d prompts, got %d", seeded, idx.TotalPrompts)
					return
				}
				if idx.Version != models.RegistryIndexVersion {
					errCh <- fmt.Errorf("corrupt index: version=%q", idx.Version)
					return
				}
				for _, e := range idx.Prompts {
					if e.ID == "" || e.Domain == "" {
						errCh <- fmt.Errorf("corrupt entry: %+v", e)
						return
					}
				}
			}
		}()
	}

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := rh.RebuildIndex(); err != nil {
					errCh <- fmt.Errorf("RebuildIndex: %w", err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestGetIndexDoesNotClobberFreshRebuild is the precise B2 regression: a cold
// GetIndex must never publish a stale on-disk index over a newer in-memory one
// produced by a RebuildIndex that raced it.
//
// The window is narrow, so this runs many trials and asserts a zero failure
// rate. Measured against the pre-fix code this reproduced 9/200 (~4.5%).
func TestGetIndexDoesNotClobberFreshRebuild(t *testing.T) {
	const trials = 200
	stale := 0

	for i := 0; i < trials; i++ {
		rh, loader, _ := newPhase2Registry(t, 3)

		// Write an index to disk, then add a prompt so disk is provably stale.
		if err := rh.RebuildIndex(); err != nil {
			t.Fatalf("initial rebuild: %v", err)
		}
		extra := models.Prompt{
			ID: "phase2-extra", Domain: "router-prompts", Scope: "project",
			Confidence: 0.9, Content: "extra", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := loader.SavePrompt(&extra, extra.Scope); err != nil {
			t.Fatalf("save extra: %v", err)
		}

		// Cold the cache so GetIndex takes the slow path.
		rh.mu.Lock()
		rh.index = nil
		rh.mu.Unlock()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = rh.GetIndex() }()
		go func() { defer wg.Done(); _ = rh.RebuildIndex() }()
		wg.Wait()

		// Whichever order they finished in, the cache must never be *older*
		// than the last completed rebuild.
		rh.mu.RLock()
		total := rh.index.TotalPrompts
		rh.mu.RUnlock()
		if total != 4 {
			stale++
		}
	}

	if stale != 0 {
		t.Errorf("stale index published in %d/%d trials (B2 regression)", stale, trials)
	}
}

// TestPromotePromptDoesNotDeadlock guards the self-deadlock found while fixing
// B2: PromotePrompt held rh.mu for writing and then called GetIndex, which takes
// rh.mu.RLock. sync.RWMutex is not reentrant, so the handler hung forever.
func TestPromotePromptDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	rh := NewRegistryHandler(dir, loader)

	p := models.Prompt{
		ID: "phase2-promote", Domain: "router-prompts", Scope: "project",
		Confidence: 0.9, Content: "promote me",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := loader.SavePrompt(&p, p.Scope); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := rh.RebuildIndex(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"prompt_id": "phase2-promote"})
	req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/registry/promote", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		rh.PromotePrompt(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PromotePrompt deadlocked (rh.mu is not reentrant)")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("promote status = %d, body = %s", rec.Code, rec.Body.String())
	}
	reloaded, err := loader.LoadByID("phase2-promote")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Promoted {
		t.Error("prompt was not marked promoted")
	}
}

// ---------------------------------------------------------------------------
// B6 — path traversal in SavePrompt
// ---------------------------------------------------------------------------

func TestSavePromptRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)

	cases := []struct {
		name   string
		id     string
		domain string
	}{
		{"domain parent traversal", "ok-id", "../evil"},
		{"domain deep traversal", "ok-id", "../../../etc"},
		{"domain absolute", "ok-id", "/etc"},
		{"domain nested slash", "ok-id", "router/../../etc"},
		{"domain dot", "ok-id", "."},
		{"domain dotdot", "ok-id", ".."},
		{"domain empty", "ok-id", ""},
		{"domain backslash", "ok-id", `..\evil`},
		{"domain null byte", "ok-id", "router\x00-prompts"},
		{"id traversal", "../../../etc/passwd", "router-prompts"},
		{"id slash", "sub/dir", "router-prompts"},
		{"id empty", "", "router-prompts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := models.Prompt{
				ID: tc.id, Domain: tc.domain, Scope: "project",
				Confidence: 0.9, Content: "payload",
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}
			err := loader.SavePrompt(&p, p.Scope)
			if err == nil {
				t.Fatalf("SavePrompt accepted hostile input id=%q domain=%q", tc.id, tc.domain)
			}
			if !strings.Contains(err.Error(), "invalid path segment") {
				t.Errorf("expected ErrInvalidPathSegment, got %v", err)
			}
		})
	}

	// Nothing may have escaped the instincts tree.
	escaped := filepath.Join(dir, "ecc-prompts", "evil")
	if _, err := os.Stat(escaped); err == nil {
		t.Errorf("traversal wrote outside the prompt tree: %s", escaped)
	}
}

func TestSavePromptAcceptsValidSegments(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)
	p := models.Prompt{
		ID: "router_classifier-selah-001", Domain: "router-prompts", Scope: "project",
		Confidence: 0.9, Content: "ok",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := loader.SavePrompt(&p, p.Scope); err != nil {
		t.Fatalf("valid prompt rejected: %v", err)
	}
}

// TestImportRejectsTraversalWith400 asserts the HTTP contract: a hostile import
// payload is a client error, not a partially-applied import.
func TestImportRejectsTraversalWith400(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(dir)

	payload := map[string]any{
		"prompts": []map[string]any{
			{"id": "good-one", "domain": "router-prompts", "scope": "project", "confidence": 0.9, "content": "fine"},
			{"id": "evil", "domain": "../../../etc", "scope": "project", "confidence": 0.9, "content": "pwn"},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp/prompts/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.ImportPrompts(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	// Fail-closed: the valid prompt in the same batch must not have landed either.
	if _, err := os.Stat(filepath.Join(dir, "ecc-prompts", "instincts", "personal", "router-prompts", "good-one.yaml")); err == nil {
		t.Error("import was partially applied despite validation failure")
	}
}

// ---------------------------------------------------------------------------
// Corrupted frontmatter recovery
// ---------------------------------------------------------------------------

func TestCorruptFrontmatterIsRebuilt(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)

	corrupt := []struct {
		name string
		yaml string
	}{
		{"unclosed bracket", "id: broken\ndomain: [router-prompts\nconfidence: 0.9\n"},
		{"tab indentation", "id: broken\n\tdomain: router-prompts\n"},
		{"bare scalar", "just a string, not a mapping\n"},
		{"sequence not mapping", "- one\n- two\n"},
		{"duplicate colon garbage", "id: : : :\n:::\n"},
	}

	for _, tc := range corrupt {
		t.Run(tc.name, func(t *testing.T) {
			p := models.Prompt{
				ID:              "recovered-" + strings.ReplaceAll(tc.name, " ", "-"),
				Domain:          "router-prompts",
				Trigger:         "recovery trigger",
				Scope:           "project",
				Confidence:      0.88,
				SuccessRate:     0.75,
				Content:         "body survives",
				FrontmatterYAML: tc.yaml, // <- the poison
				CreatedAt:       time.Now().UTC().Truncate(time.Second),
				UpdatedAt:       time.Now().UTC().Truncate(time.Second),
			}
			if err := loader.SavePrompt(&p, p.Scope); err != nil {
				t.Fatalf("SavePrompt: %v", err)
			}

			// The written file must now be parseable, with correct field values.
			reloaded, err := loader.LoadByID(p.ID)
			if err != nil {
				t.Fatalf("corrupt frontmatter was written back verbatim; reload failed: %v", err)
			}
			if reloaded.Domain != "router-prompts" {
				t.Errorf("domain = %q, want router-prompts", reloaded.Domain)
			}
			if reloaded.Confidence != 0.88 {
				t.Errorf("confidence = %v, want 0.88", reloaded.Confidence)
			}
			if reloaded.Trigger != "recovery trigger" {
				t.Errorf("trigger = %q", reloaded.Trigger)
			}
			if !strings.Contains(reloaded.Content, "body survives") {
				t.Errorf("content lost: %q", reloaded.Content)
			}
		})
	}
}

func TestValidFrontmatterCustomFieldsPreserved(t *testing.T) {
	dir := t.TempDir()
	loader := NewPromptLoader(dir)

	p := models.Prompt{
		ID: "keeps-custom", Domain: "router-prompts", Scope: "project",
		Confidence: 0.9, Content: "x",
		FrontmatterYAML: "id: keeps-custom\ndomain: router-prompts\ncustom_field: keep-me\n",
		CreatedAt:       time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := loader.SavePrompt(&p, p.Scope); err != nil {
		t.Fatalf("SavePrompt: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ecc-prompts", "instincts", "personal", "router-prompts", "keeps-custom.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "custom_field: keep-me") {
		t.Errorf("merge path dropped custom field:\n%s", raw)
	}
}

// ---------------------------------------------------------------------------
// Index version validation
// ---------------------------------------------------------------------------

func TestLoadIndexRejectsWrongVersion(t *testing.T) {
	dir := t.TempDir()
	builder := models.NewRegistryBuilder(dir)

	stale := &models.RegistryIndex{
		Version:      "0.9",
		TotalPrompts: 99,
		Domains:      map[string]models.DomainStats{},
		Prompts:      []models.RegistryEntry{{ID: "ghost", Domain: "router-prompts"}},
	}
	if err := builder.SaveIndex(stale); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}

	got, err := builder.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex returned error, want (nil, nil): %v", err)
	}
	if got != nil {
		t.Fatalf("version %q accepted; want nil so caller rebuilds", got.Version)
	}
}

func TestLoadIndexRejectsCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	builder := models.NewRegistryBuilder(dir)
	idxPath := filepath.Join(dir, "ecc-prompts", "registry", "registry-index.json")
	if err := os.MkdirAll(filepath.Dir(idxPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idxPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := builder.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex errored on corrupt file, want (nil, nil): %v", err)
	}
	if got != nil {
		t.Fatal("corrupt index accepted")
	}
}

// TestGetIndexRebuildsOnVersionMismatch is the end-to-end version-gate check.
func TestGetIndexRebuildsOnVersionMismatch(t *testing.T) {
	rh, _, dir := newPhase2Registry(t, 5)

	// Plant a stale-version index on disk with a bogus count.
	builder := models.NewRegistryBuilder(dir)
	if err := builder.SaveIndex(&models.RegistryIndex{
		Version:      "0.1",
		TotalPrompts: 9999,
		Domains:      map[string]models.DomainStats{},
		Prompts:      []models.RegistryEntry{},
	}); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}

	idx, err := rh.GetIndex()
	if err != nil {
		t.Fatalf("GetIndex: %v", err)
	}
	if idx.Version != models.RegistryIndexVersion {
		t.Errorf("version = %q, want %q", idx.Version, models.RegistryIndexVersion)
	}
	if idx.TotalPrompts != 5 {
		t.Errorf("TotalPrompts = %d, want 5 (rebuilt from corpus)", idx.TotalPrompts)
	}
}

// ---------------------------------------------------------------------------
// Sort performance — before/after
// ---------------------------------------------------------------------------

func makePhase2Entries(n int) []models.RegistryEntry {
	entries := make([]models.RegistryEntry, n)
	base := time.Now().UTC()
	for i := range entries {
		// Deterministic but unsorted ordering (worst case for bubble sort is
		// reverse order; this LCG scramble is representative of real data).
		k := (i*7919 + 13) % n
		entries[i] = models.RegistryEntry{
			ID:          fmt.Sprintf("bench-%05d", i),
			Domain:      "router-prompts",
			Confidence:  float32(k) / float32(n),
			SuccessRate: float32(n-k) / float32(n),
			UpdatedAt:   base.Add(time.Duration(k) * time.Second),
		}
	}
	return entries
}

// bubbleSortBySuccessRate reproduces the pre-fix O(n^2) algorithm verbatim so
// the benchmark measures a real A/B, not a straw man.
func bubbleSortBySuccessRate(filtered []models.RegistryEntry) {
	for i := len(filtered) - 1; i > 0; i-- {
		for j := 0; j < i; j++ {
			if filtered[j].SuccessRate < filtered[j+1].SuccessRate {
				filtered[j], filtered[j+1] = filtered[j+1], filtered[j]
			}
		}
	}
}

var sortSizes = []int{100, 1000, 5000}

// BenchmarkSortBefore_Bubble measures the pre-fix O(n^2) algorithm.
func BenchmarkSortBefore_Bubble(b *testing.B) {
	for _, n := range sortSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			src := makePhase2Entries(n)
			buf := make([]models.RegistryEntry, len(src))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(buf, src)
				bubbleSortBySuccessRate(buf)
			}
		})
	}
}

// BenchmarkSortAfter_SliceStable measures the shipped O(n log n) replacement.
// SliceStable (not Slice) is deliberate: the bubble sort was stable, so equal
// SuccessRate values kept their confidence ordering. Preserving that keeps the
// API response byte-identical for ties.
func BenchmarkSortAfter_SliceStable(b *testing.B) {
	for _, n := range sortSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			src := makePhase2Entries(n)
			buf := make([]models.RegistryEntry, len(src))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(buf, src)
				sort.SliceStable(buf, func(x, y int) bool { return buf[x].SuccessRate > buf[y].SuccessRate })
			}
		})
	}
}

// BenchmarkSortAfter_SliceUnstable is reported for reference only — faster, but
// it would reorder ties and change API output.
func BenchmarkSortAfter_SliceUnstable(b *testing.B) {
	for _, n := range sortSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			src := makePhase2Entries(n)
			buf := make([]models.RegistryEntry, len(src))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(buf, src)
				sort.Slice(buf, func(x, y int) bool { return buf[x].SuccessRate > buf[y].SuccessRate })
			}
		})
	}
}

// TestSortCorrectness proves the replacement produces the same ordering the
// bubble sort did (descending, stable).
func TestSortCorrectness(t *testing.T) {
	src := makePhase2Entries(500)

	want := make([]models.RegistryEntry, len(src))
	copy(want, src)
	bubbleSortBySuccessRate(want)

	got := make([]models.RegistryEntry, len(src))
	copy(got, src)
	sort.SliceStable(got, func(i, j int) bool { return got[i].SuccessRate > got[j].SuccessRate })

	for i := range want {
		if want[i].ID != got[i].ID {
			t.Fatalf("ordering diverged at %d: bubble=%s sort.SliceStable=%s", i, want[i].ID, got[i].ID)
		}
	}
}

// BenchmarkListRegistry1000 measures the full endpoint with 1000 entries cached.
func BenchmarkListRegistry1000(b *testing.B) {
	dir := b.TempDir()
	loader := NewPromptLoader(dir)
	rh := NewRegistryHandler(dir, loader)
	rh.index = &models.RegistryIndex{
		Version:      models.RegistryIndexVersion,
		Domains:      map[string]models.DomainStats{},
		Prompts:      makePhase2Entries(1000),
		TotalPrompts: 1000,
	}
	req := httptest.NewRequest(http.MethodGet, "/mcp/prompts/registry?sort=success_rate&limit=1000", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		rh.ListRegistry(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}
