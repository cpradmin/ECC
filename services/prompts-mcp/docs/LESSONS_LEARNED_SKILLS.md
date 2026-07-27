# Prompts-MCP: Lessons Learned & Reusable Patterns

## 1. Concurrency Patterns

### Pattern 1A: Double-Checked Locking for Cache Warm Path
**Problem:** Naive read-lock + cold-path rebuild releases lock before disk I/O, allowing concurrent rebuilds to clobber cache.

**Solution:**
```go
// Fast path: warm cache under read lock
if c := rh.cachedCopy(); c != nil {
    return c, nil
}

// Serialize cold-start with a separate mutex (always acquired BEFORE mu)
rh.loadMu.Lock()
defer rh.loadMu.Unlock()

// Double-check after waiting for lock
if c := rh.cachedCopy(); c != nil {
    return c, nil
}

// Disk I/O happens OUTSIDE every critical section on rh.mu
index, err := rh.builder.LoadIndex()

// Publish, but never overwrite fresher in-memory index
rh.mu.Lock()
if rh.index == nil {
    rh.index = index
}
result := rh.deepCopyIndex(rh.index)
rh.mu.Unlock()
return result, nil
```

**Key:** Two tiers of locking (loadMu for serialization, mu for publishing), disk I/O outside all locks, conditional assignment (only update if still cold).

**When to use:** Cache that can be rebuilt from disk or slow computation; multiple goroutines racing to populate.

---

### Pattern 1B: Atomic Load-Modify-Write Under Single Lock
**Problem:** Confidence updates race: A loads 0.7, B loads 0.7, A updates to 0.8, B updates to 0.85, B's write overwrites A's in filesystem.

**Solution:**
```go
func (pl *PromptLoader) UpdateConfidence(ctx context.Context, promptID string, delta float32) error {
    pl.mu.Lock()
    defer pl.mu.Unlock()
    
    // Load, modify, write all under one lock
    prompt, err := pl.loadByIDLocked(promptID)
    if err != nil { return err }
    
    prompt.Confidence = clamp(prompt.Confidence + delta, 0, 1)
    prompt.UpdatedAt = time.Now()
    
    return pl.savePromptLocked(prompt)
}
```

**Key:** One lock per shared resource (the corpus), entire cycle atomic, bypass caches for consistency.

**When to use:** Read-modify-write on persistent state; lost updates are unacceptable; reads are rare vs writes (don't over-optimize with RWMutex).

---

### Pattern 1C: Collapse Concurrent Reads with Mutex
**Problem:** 50 concurrent Prometheus scrapes each compute full metrics.

**Solution:**
```go
type MetricsCache struct {
    mu sync.Mutex  // NOT RWMutex
    snapshot *Snapshot
    expiry time.Time
}

func (mc *MetricsCache) GetOrCompute() *Snapshot {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    
    if time.Now().Before(mc.expiry) && mc.snapshot != nil {
        return mc.snapshot  // Return live pointer (safe under lock)
    }
    
    // Compute once; other waiters block and reuse
    mc.snapshot = mc.compute()
    mc.expiry = time.Now().Add(60 * time.Second)
    return mc.snapshot
}
```

**Key:** Use Mutex (not RWMutex) to serialize all readers; waiters queue and reuse result. Cheaper than thundering herd.

**When to use:** Read-heavy workload; computation is expensive but reads are frequent.

---

## 2. Error Handling Patterns

### Pattern 2A: Pre-Check JSON Marshal Errors
**Problem:** `json.NewEncoder(w).Encode()` errors are silently ignored → clients see partial/corrupt JSON.

**Solution:**
```go
func WriteJSON(w http.ResponseWriter, statusCode int, data interface{}) error {
    // Marshal to buffer FIRST (no side effects if it fails)
    var buf bytes.Buffer
    if err := json.NewEncoder(&buf).Encode(data); err != nil {
        return fmt.Errorf("JSON marshal: %w", err)
    }
    
    // Now safe to write headers and body
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    _, err := buf.WriteTo(w)
    
    if err != nil {
        // Write after headers sent: log + panic (abort connection)
        log.Error("JSON write failed", err)
        panic(http.ErrAbortHandler)
    }
    return nil
}
```

**Key:** Encode to buffer first; only touch ResponseWriter if marshal succeeds.

**When to use:** All HTTP JSON responses; any case where side effects must be atomic with computation.

---

### Pattern 2B: Timeout on All Filesystem Operations
**Problem:** Stalled filesystem hangs goroutines forever.

**Solution:**
```go
func (pl *PromptLoader) LoadAllWithTimeout(ctx context.Context, timeout time.Duration) ([]Prompt, error) {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    // For reads: spawn goroutine, select on done
    done := make(chan []Prompt, 1)
    go func() {
        // Not holding resource after function returns; safe to abandon
        prompts, _ := pl.loadFromDirectory(pl.baseDir)
        done <- prompts
    }()
    
    select {
    case prompts := <-done:
        return prompts, nil
    case <-ctx.Done():
        return nil, fmt.Errorf("load timeout")
    }
}
```

**Key:** Reads can abandon (no cleanup needed); writes need semaphore to prevent partial updates.

**When to use:** All I/O operations; default 5s ceiling.

---

## 3. Caching Patterns

### Pattern 3A: TTL Cache with Invalidation
**Problem:** Cache stale after writes; TTL means 60s lag for correctness.

**Solution:**
```go
func (mc *MetricsCache) InvalidateOnWrite() {
    mc.mu.Lock()
    mc.expiry = time.Now()  // Expire immediately
    mc.mu.Unlock()
}

// Called from SubmitFeedback, ImportPrompts, RebuildIndex
func (mh *MetricsHandler) InvalidateMetrics() {
    mh.cache.InvalidateOnWrite()
}
```

**Key:** Invalidate on every write; TTL only bounds drift from out-of-band changes.

**When to use:** Read-heavy with periodic writes; correctness matters on write-to-read.

---

## 4. Validation Patterns

### Pattern 4A: Fail-Closed Path Validation
**Problem:** `Domain="../../../etc"` writes outside baseDir.

**Solution:**
```go
func ValidatePromptPath(domain, id string) error {
    // Whitelist: alphanumeric, dash, underscore only
    if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(domain) {
        return fmt.Errorf("invalid domain: %q", domain)
    }
    if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(id) {
        return fmt.Errorf("invalid id: %q", id)
    }
    
    // Double-check with filepath.IsLocal (Go 1.22+)
    if !filepath.IsLocal(filepath.Join(domain, id)) {
        return fmt.Errorf("path escape attempt")
    }
    
    return nil
}

// Validate batch BEFORE writing anything (fail-closed)
func (mh *MetricsHandler) ImportPrompts(w http.ResponseWriter, r *http.Request) {
    for _, p := range batch {
        if err := ValidatePromptPath(p.Domain, p.ID); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return  // No writes on error path
        }
    }
    // Only write if ALL validate
    for _, p := range batch {
        mh.savePrompt(p)
    }
}
```

**Key:** Validate all inputs before any side effects; fail-closed (prefer rejecting valid input over accepting invalid).

**When to use:** Path-based operations; external input that reaches filesystem/SQL.

---

## 5. Testing Patterns

### Pattern 5A: Race Detector Test for Lost Updates
**Problem:** Concurrent feedback updates silently lose data.

**Solution:**
```go
func TestConcurrentFeedbackNoLostUpdates(t *testing.T) {
    pl := NewPromptLoader(t.TempDir())
    pl.SavePrompt(&Prompt{ID: "p1", Confidence: 0.5})
    
    // Spawn 100 goroutines, each incrementing confidence by 0.01
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            pl.UpdateConfidence(context.Background(), "p1", 0.01)
        }()
    }
    wg.Wait()
    
    // Expected: 0.5 + 1.0 = 1.0 (clamped)
    p, _ := pl.LoadByID("p1")
    if p.Confidence != 1.0 {
        t.Errorf("lost updates: got %.2f, want 1.0", p.Confidence)
    }
}
// Run: go test -race
```

**Key:** Spawn many goroutines doing the same operation; verify deterministic result; run with `-race`.

**When to use:** Any concurrent write operation; before and after race fixes.

---

### Pattern 5B: Benchmark Before/After
**Problem:** "Cache is faster" — by how much?

**Solution:**
```go
func BenchmarkLoadAllCached(b *testing.B) {
    pl := NewPromptLoader(setupCorpus(1000))
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        pl.LoadAll()  // Second call hits cache
    }
}
// go test -bench=BenchmarkLoadAllCached

// BEFORE: 7.34ms per call
// AFTER: 52µs per call (99.3% reduction)
```

**Key:** Measure wall time before/after; include corpus size; state assumptions clearly.

**When to use:** All performance fixes; baseline + fix runs for comparison.

---

## 6. Production Readiness

### Checklist
- [ ] Race detector passes (`go test -race`)
- [ ] All error paths tested
- [ ] Timeouts on all I/O
- [ ] Panic recovery middleware
- [ ] Request body limits
- [ ] Concurrent operation stress-tested (100+ goroutines)
- [ ] Cache invalidation tested
- [ ] Metrics for observability (scrape errors, cache hits, timeouts)
- [ ] Runbook validation at startup
- [ ] Configuration validation (env vars, file permissions)
- [ ] Load test at 10× expected peak throughput
- [ ] Graceful degradation (timeouts → errors, not hangs)

---

## 7. Common Gotchas

### Gotcha 1: sync.RWMutex is Not Reentrant
```go
func (rh *RegistryHandler) PromotePrompt(id string) error {
    rh.mu.Lock()  // <-- DEADLOCK HERE
    defer rh.mu.Unlock()
    
    index, err := rh.GetIndex()  // <-- Tries to RLock/Unlock, but we hold Lock
    // ...
}
// Fix: Add getIndexLocked() for write-locked callers
```

### Gotcha 2: YAML Float Serialization
```go
// yaml.v3 writes float32(1) as integer 1 (not 1.0)
confidence := float32(1.0)
yaml.Marshal(map[string]interface{}{"confidence": confidence})
// Output: confidence: 1  (not 1.0)

// Loader does: confidence, _ := data["confidence"].(float64)
// Assertion fails; substitutes default 0.5
// FIX: Use yamlFloat32 helper or explicit type conversion
```

### Gotcha 3: Go net/http Loopback Latency
```
Sending 110KB response to localhost:
  Go:     410ms (net/http buffer draining)
  Python: 0.4ms (same bytes)
```
**Investigation:** Pre-existing environmental issue (kernel buffer tuning, not app-level).

---

## Recommended Reading

1. **Go Concurrency Patterns:** https://www.youtube.com/watch?v=f6kdp27TYZs
2. **Context Timeouts:** https://go.dev/blog/pipelines
3. **JSON Best Practices:** Encode to buffer before headers (this pattern)
4. **Prometheus Metrics:** https://prometheus.io/docs/practices/
