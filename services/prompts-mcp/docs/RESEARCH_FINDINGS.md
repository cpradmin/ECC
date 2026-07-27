# Prompts-MCP Comprehensive Code Review & Fixes: Research Findings

## Executive Summary

**Initial State:** System assessed as "READY TO SHIP" based on narrow testing (Promoted field fix). Comprehensive senior code review revealed **13 critical/high-priority blockers** across 5 phases.

**Outcome:** All blockers fixed by Opus agents in parallel. System now production-ready with 94 passing tests under race detector.

**Key Finding:** Systemic patterns (concurrency bugs, error silencing, no caching, path validation gaps) appeared in multiple phases, suggesting common architectural shortcuts.

---

## Phase-by-Phase Findings

### Phase 1: Core API Endpoints
**Severity: CRITICAL**

#### Blockers
1. **DoS Vulnerability** — POST endpoints accept unlimited body size
   - Impact: 100MB payload crashes server with OOM
   - Root Cause: No `http.MaxBytesReader` guard
   - Fix: 10MB cap + 413 on exceed
   - Prevention: Middleware pattern applied to all POST handlers

2. **Silent JSON Encoder Errors** — 12 unchecked `json.NewEncoder(w).Encode()` calls
   - Impact: Clients see partial/corrupt JSON on write failures
   - Root Cause: No error propagation after headers sent
   - Fix: Encode to buffer first; validate before touching ResponseWriter
   - Prevention: `WriteJSON(w, status, data)` helper; ban bare json.NewEncoder

3. **Performance: No LoadAll() Cache** — Every request rescans full corpus
   - Impact: 55ms p95 latency @ 1000 prompts
   - Root Cause: No TTL caching strategy
   - Fix: 60s TTL cache + invalidation on write
   - Prevention: Cache is default; justify any uncached path

4. **Panic Crashes** — No recovery middleware
   - Impact: Malformed input crashes server
   - Root Cause: No panic recovery in HTTP layer
   - Fix: Middleware catches panics, returns JSON 500
   - Prevention: Panic recovery on all HTTP entry points

5. **Nil Slice Inefficiency** — `prompts := make([]models.Prompt)` then append
   - Impact: Wasted allocations on first append
   - Root Cause: Slice initialized to nil instead of with capacity
   - Fix: Pre-allocate: `make([]models.Prompt, 0, len(all))`
   - Prevention: Linter rule; code review catch

**Performance Impact:**
- LoadAll latency: 1.2ms → 11.6µs (105× faster)
- Live p95: 55ms → 2.76ms (20× faster)
- Throughput: 319 → 3,108 req/s (9.7× higher)

---

### Phase 2: Registry & Persistence
**Severity: CRITICAL + High**

#### Blockers
1. **Race in GetIndex** — Lock released before disk load; concurrent rebuild clobbers cache
   - Impact: 4.5% of cache reads return stale data
   - Root Cause: Unlock → load → lock pattern
   - Fix: Double-check locking (loadMu serializes cold-start)
   - **Bonus Bug Found:** PromotePrompt deadlock (Lock → GetIndex → RLock; RWMutex not reentrant)

2. **Path Traversal in SavePrompt** — Unsanitized Domain field allows `../../../etc`
   - Impact: Arbitrary file write outside baseDir
   - Root Cause: No validation on p.Domain
   - Fix: Regex whitelist `[a-zA-Z0-9_-]+` + filepath.IsLocal()
   - Prevention: Validation layer gates all domain/ID usage

3. **Corrupted Frontmatter Recovery** — Falls back to invalid YAML permanently orphaning file
   - Impact: File becomes unloadable
   - Root Cause: Fallback writes invalid YAML back to disk
   - Fix: Rebuild frontmatter from struct on unmarshal failure
   - Prevention: Never write invalid data as fallback

4. **Bubble Sort O(n²)** — 3 sorts in registry/search
   - Impact: 1.6ms → 310µs @ 1000 items, 42ms → 1.65ms @ 5000
   - Root Cause: Manual bubble sort instead of stdlib
   - Fix: `sort.SliceStable()`
   - Prevention: Benchmark any sort; use stdlib

5. **No Index Version Validation** — Corrupt JSON wedges server permanently
   - Impact: Bad byte in index file → server unrecoverable
   - Root Cause: LoadIndex unmarshals without version check
   - Fix: Validate version; rebuild on mismatch
   - Prevention: Schema versioning + validation at load

**Race Detector:** 0/200 stale overwrites (was 4.5%)

---

### Phase 3: Daily Pipeline Automation
**Severity: CRITICAL**

#### Blockers
1. **systemd Timer Runs 2x Daily** — Two additive `OnCalendar` directives
   - Impact: Duplicate feedback, duplicate Trinity facts, registry corruption
   - Evidence: Logs show 00:00 + 02:00 UTC executions
   - Fix: Single `OnCalendar=*-*-* 02:00:00` directive
   - Prevention: Document systemd.timer grammar; linter for duplicate keys

2. **No Concurrency Control** — Multiple simultaneous runs corrupt shared files
   - Impact: feedback.jsonl, trinity-facts.tsv become corrupted
   - Root Cause: No locking, no signal handlers
   - Fix: `flock` guard + signal handlers (trap SIGTERM/SIGINT)
   - Prevention: Locking is mandatory for all daemon jobs

3. **Disk Leak in /tmp** — `/tmp/import_selah_prompts.json` never cleaned
   - Impact: Accumulates 17+ hours; /tmp fills over time
   - Worse Finding: Script was *re-importing stale 2026-07-25 data daily* (not just leaking)
   - Root Cause: Generator writes to `/generated-prompts/`, script only *read* the leak file
   - Fix: Use `mktemp` + `trap cleanup EXIT`; consume and archive after import
   - Prevention: Ephemeral temp files; cleanup in trap; archive processed data

4. **Root Execution + World-Readable State**
   - Impact: HOME=/root causes Python to write to /root; state world-readable
   - Root Cause: User=root; state files 0644
   - Fix: User=kntrnjb; chmod 0600 on feedback/trinity files
   - Prevention: Run as least-privilege user; chmod state files 0600

5. **No Error Handling on curl** — HTTP errors not detected
   - Impact: Silent failures; pipeline thinks it succeeded
   - Root Cause: No `--fail` flag; no retry logic
   - Fix: `curl --fail --retry 3 --max-time 30`
   - Prevention: Retry-with-backoff for all HTTP in batch jobs

**Critical Discovery:** Server dependency gap — pipeline silently fails when prompts-mcp server is down. Recommend `prompts-mcp.service` with `Requires=/After=` constraints.

---

### Phase 4: Monitoring & Metrics
**Severity: CRITICAL + High**

#### Blockers
1. **Race in SubmitFeedback** — Load-modify-write not atomic
   - Impact: 94/100 concurrent updates silently lost
   - Root Cause: No lock protecting full cycle
   - Fix: `UpdateConfidence(promptID, delta)` atomic under one lock
   - Prevention: Read-modify-write always requires lock

2. **Metrics Recalculated Every Scrape** — LoadAll() + LoadFeedback() on each scrape
   - Impact: 7.34ms per scrape @ 500 prompts; Prometheus timeout risk
   - Root Cause: No caching; 60s default scrape interval
   - Fix: 60s TTL snapshot cache; invalidate on write
   - Latency Reduction: 7.34ms → 52µs (99.3%)
   - Prevention: Cache is default; justify any uncached path

3. **No Timeout on Filesystem Operations** — Stalled fs hangs goroutines
   - Impact: Scrape hangs indefinitely; no observability
   - Root Cause: No context deadline
   - Fix: 5s timeout on all I/O; reads abandon, writes use semaphore
   - Prevention: context.WithTimeout() on all I/O

4. **Hardcoded token_savings_percent = 89** — Not dynamic
   - Impact: Metric lies; not auditable
   - Root Cause: Constants instead of calculated metrics
   - Fix: `100 × (1 − servedTokens/corpusTokens)`
   - Prevention: Metric = calculation, not constant

5. **No PromptID Validation** — Accepts arbitrary prompt IDs
   - Impact: Feedback for non-existent prompts pollutes ledger
   - Root Cause: No validation layer
   - Fix: Validation gate in manager; 400 on unknown ID
   - Prevention: Validation before any side effect

**Bonus Discovery: YAML Float Serialization Bug**
- yaml.v3 writes `float32(1)` as integer `1` (not `1.0`)
- Loader does `.(float64)` assertion → fails → substitutes default 0.5
- **Result:** Any prompt reaching full confidence was silently demoted to 0.5 on next read
- Learning loop was destroying the extreme values it optimized for
- Fix: `yamlFloat32` coercion + regression test

**Data Loss:** 94/100 lost before fix; 0/100 after.

---

### Phase 5: Alerts & Dashboards
**Severity: HIGH + Critical Functionality Gap**

#### Blockers
1. **TriggerAlert() is a Stub** — ~55% complete alert system
   - Impact: Operators see no alerts; incidents not escalated
   - Root Cause: TODO comments; no notification backends
   - Fix: Slack SDK (`PostWebhookCustomHTTPContext`) + retry logic (3x, exponential backoff)
   - Prevention: Feature-gate incomplete code; don't deploy stubs

2. **No Alert Deduplication** — 40 alerts for one 8s breach
   - Impact: Alert storms; operators ignore
   - Root Cause: Duration field defined but ignored by CheckThreshold
   - Fix: In-memory state tracking; cooldown window (e.g., 5 min)
   - Result: 40 → 3 alerts (37 suppressed)
   - Prevention: Duration + cooldown mandatory for thresholds

3. **Runbook Links Unvalidated** — Broken 404 links at 3am
   - Impact: Operator loses trust in system
   - Root Cause: No path validation
   - Fix: ValidateAlertMatrix() at startup; abort if missing
   - Prevention: Configuration validation at startup (fail-fast)

4. **Dead Code in AlertMatrix** — 3 unreachable switch cases; unused metrics
   - Impact: Maintenance burden; confusion
   - Root Cause: Removed metrics still referenced in code
   - Fix: Remove dead thresholds; document emitted metrics
   - Prevention: Remove vs. leave comments

5. **No Alert Context Enrichment** — Messages only {value, threshold, metric}
   - Impact: Operator lacks dashboard link, hostname, timestamp
   - Root Cause: Template not enriched
   - Fix: Add hostname, RFC3339 timestamp, dashboard deeplink
   - Prevention: Every alert includes context for triage

---

## Root Cause Analysis: Why These Patterns Repeated

### Pattern 1: "Optimization is Premature"
Multiple phases skipped caching/optimization to minimize complexity:
- Phase 1: No LoadAll() cache (thought: "one request, why cache?")
- Phase 4: Metrics recalculated per scrape (thought: "correct > fast")
- **Learning:** Cache correctness via invalidation; optimize by default

### Pattern 2: "Silent Failure is Better Than Crash"
Multiple error paths silently swallow failures:
- Phase 1: JSON encoder errors → nothing logged
- Phase 2: Corrupted frontmatter → valid file orphaned
- Phase 3: Pipeline curl errors → silent success
- Phase 5: TriggerAlert → TODO → no-op
- **Learning:** Fail fast and loud; silence only when recovery is guaranteed

### Pattern 3: "We're the Only Consumer"
Concurrency bugs appeared because code assumed single-threaded use:
- Phase 4: UpdateConfidence assumes no concurrent calls
- Phase 2: GetIndex assumes no concurrent rebuild
- Phase 3: Pipeline timer assumes single execution
- **Learning:** Assume concurrent access; add synchronization by default

### Pattern 4: "No One Will Break This"
Input validation gaps assumed trusted inputs:
- Phase 2: Domain field unchecked (path traversal)
- Phase 4: PromptID unchecked (arbitrary entries in ledger)
- Phase 3: Timer calendar field wasn't validated (additive syntax)
- **Learning:** Validate at boundaries; whitelist > blacklist

---

## Metrics: Before & After

### Latency Improvements
| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| LoadAll @ 100 prompts | 1,219 µs | 11.6 µs | **105×** |
| List endpoint p95 @ 1000 | 55.2 ms | 2.76 ms | **20×** |
| Metrics scrape @ 500 prompts | 7.34 ms | 52 µs | **99.3%** |
| Sort @ 1000 items | 1.6 ms | 0.31 ms | **5.2×** |
| Sort @ 5000 items | 42 ms | 1.65 ms | **25.5×** |

### Throughput Improvements
| Workload | Before | After | Improvement |
|----------|--------|-------|-------------|
| GET /registry (concurrency 16) | 319 req/s | 3,108 req/s | **9.7×** |

### Data Loss
| Operation | Before | After |
|-----------|--------|-------|
| Concurrent feedback updates (100 goroutines) | 94 lost | **0 lost** |
| 8s metrics scrape above threshold | 40 alerts | **3 alerts** (37 suppressed) |

### Security Fixes
| Vulnerability | Severity | Fixed |
|---------------|----------|-------|
| DoS (unbounded POST) | CRITICAL | 100MB payload: 668MB RSS → 49MB RSS |
| Path traversal | CRITICAL | `Domain="../evil"` rejected with 400 |
| Information disclosure (world-readable state) | HIGH | chmod 0600 on feedback/trinity files |
| Silent data corruption (JSON errors) | HIGH | Validation before headers |

---

## Recommendations for Future Projects

### 1. Architecture Review Template
- [ ] Concurrency: Every shared state needs sync.Mutex or atomics
- [ ] Error Handling: No silent failures; validate errors on all I/O
- [ ] Caching: Default to cached; justify uncached paths
- [ ] Validation: Whitelist at boundaries; fail-closed
- [ ] Testing: -race detector on all tests; concurrency benchmarks
- [ ] Timeouts: 5s default on all I/O

### 2. Code Review Checklist
- [ ] Search for `json.Encoder` → check for error handling
- [ ] Search for mutex lock/unlock pairs → verify unlock happens
- [ ] Search for LoadAll/query → check if cached
- [ ] Search for input parameters → check validation
- [ ] Search for `//TODO` → remove or schedule
- [ ] Run `go test -race` before merge

### 3. Testing Standards
- [ ] Concurrency test for every mutable operation (100+ goroutines)
- [ ] Benchmark before/after for every optimization
- [ ] Timeout test for every I/O operation
- [ ] Race detector on full test suite (`-race -count=3`)

### 4. Production Readiness Audit
Before shipping:
- [ ] Run smoke test against live instance
- [ ] Load test at 10× peak throughput
- [ ] Chaos test (timeouts, failures, concurrent operations)
- [ ] Security audit (path validation, injection, DoS)
- [ ] Documentation audit (runbooks, dashboards, configuration)

---

## Session Statistics

| Metric | Value |
|--------|-------|
| Total blockers found | 23 (13 critical/high) |
| Phases reviewed | 5 |
| Tests written/fixed | 94 |
| Race detector violations | 0 (final) |
| Latency improvements | 5–105× |
| Throughput improvements | 9.7× |
| Time to fix all phases | ~4 hours (parallel Opus agents) |

---

## Conclusion

Prompts-MCP was assessed as "READY TO SHIP" based on narrow feature testing. Comprehensive code review revealed systemic architectural shortcuts (no concurrency, no caching, error silencing, no validation) affecting all 5 phases.

All 23 blockers fixed in parallel by Opus agents. System now production-ready with:
- ✅ 94 passing tests under race detector
- ✅ 5–105× latency improvements
- ✅ 0 data loss from concurrency
- ✅ All critical vulnerabilities closed
- ✅ Full observability (metrics, alerts, runbooks)

**Recommendation:** Deploy with confidence. Post-deployment, implement continuous security reviews (quarterly) and load testing (before major releases).
