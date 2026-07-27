# Code Review Framework: Phases 1-5 Monitoring Architecture

**Date:** 2026-07-26  
**Status:** Comprehensive advanced review in progress  
**Scope:** All monitoring infrastructure (Phases 1-5)  
**Standard:** Production-grade monitoring code (failures alert on-call)

---

## Review Scope (What's Being Reviewed)

### Phase 1: Structured Logging (Foundation)
**Files:**
- `handlers/logger.go` — slog JSON handler, fallback handling, AddSource toggle
- `handlers/metrics.go` — Prometheus metrics endpoint
- `handlers/handlers.go` — handler initialization

**Key Changes:**
- ✅ Fallback warning visible on stderr (Issue #5 fixed)
- ✅ AddSource disabled for performance (Issue #3 fixed)

### Phase 2: Log Aggregation (Pipeline)
**Files:**
- `MONITORING_PLAN.md` — Loki/promtail configuration, log destinations
- `handlers/logger.go` — log file destinations, JSON format

**Key Decisions:**
- Logs → `/var/log/prompts-mcp/service.log` → Promtail → Loki:3100
- JSONL format with structured fields (service, domain, action, status)

### Phase 3: Grafana Dashboards (Visualization)
**Files:**
- `/tmp/registry-health-dashboard.json` — Gauge panels, confidence thresholds
- `/tmp/governor-dashboard.json` — Routing metrics, success rates
- `/tmp/pipeline-dashboard.json` — Pipeline health, duration tracking

**Key Queries:**
- `prompts_registry_total`, `prompts_avg_confidence`, `prompts_promoted_count`
- Time series for confidence evolution, feedback trends
- Heatmaps for domain-confidence distribution

### Phase 4: AWX Integration (Automation)
**Files:**
- `/tmp/check_registry_health.yml` — URI task, metric extraction, thresholds
- `/tmp/check_governor_health.yml` — Routing query validation
- `/tmp/check_pipeline_health.yml` — Pipeline step verification

**Key Jobs:**
- Registry: Query `/mcp/prompts/metrics`, alert if `avg_confidence < 0.70`
- Governor: Query intelligence endpoint, check high-confidence domains
- Pipeline: Tail logs, verify completion, extract duration

### Phase 5: Alerting Framework (Response)
**Files:**
- `handlers/alerts.go` — AlertMatrix (6 thresholds), CheckThreshold logic, recovery procedures
- `handlers/governor.go` — Session/feedback ID generation (crypto/rand)
- `handlers/registry.go` — PromotePrompt durability fix
- `handlers/loader.go` — Promoted field persistence
- `models/prompt.go` — Promoted bool field

**Recent Fixes (Commit 653e978):**
- ✅ CheckThreshold metric-specific comparison logic (was broken for duration/count)
- ✅ Feedback ID generation updated to crypto/rand (consistency with session IDs)

---

## Review Scoring System

| Symbol | Meaning | Action |
|--------|---------|--------|
| ✅ | Verified correct, production-ready | Ship as-is |
| ⚠️ | Plausible concern, needs verification | Document and monitor |
| ❌ | Confirmed bug, must fix before ship | Fix immediately |
| 🔧 | Refactor opportunity, works but suboptimal | Queue for future |

---

## Critical Review Checklist

### Correctness (Must Pass)
- [ ] Alert thresholds match MONITORING_PLAN.md exactly
- [ ] CheckThreshold comparison logic correct for all 6 metrics
- [ ] Promoted field persists through rebuild cycles
- [ ] Session/feedback IDs collision-free and unpredictable
- [ ] Metrics endpoint returns valid Prometheus format
- [ ] Dashboard queries don't timeout or error on live data

### Security (Must Pass)
- [ ] crypto/rand used for IDs (not modulo, not predictable)
- [ ] No hardcoded credentials in dashboards/playbooks
- [ ] No secrets in alert messages or error logs
- [ ] Alert threshold boundary conditions correct (≤ vs <, > vs ≥)

### Reliability (Must Pass)
- [ ] Promoted prompts survive index rebuild
- [ ] Logger fallback is always visible (no silent failures)
- [ ] Feedback persists even if Trinity RAG fails
- [ ] AlertThreshold array never modified after init
- [ ] No unbounded growth (feedback.jsonl, index, etc.)

### Thread Safety (Must Pass)
- [ ] No data races on RegistryHandler.index (RWMutex)
- [ ] No data races on FeedbackManager writes
- [ ] AlertHandler safe for concurrent CheckThreshold calls
- [ ] Governor feedback handler handles concurrent requests

### Performance (Should Pass)
- [ ] AddSource disabled (no file:line logging overhead)
- [ ] Registry RLock for reads (not Lock)
- [ ] LoadAll() not called in hot path
- [ ] Dashboard queries efficient (not full table scans)

### Edge Cases (Should Handle)
- [ ] Log directory doesn't exist → created by MkdirAll ✓
- [ ] Loki unreachable → logs to stderr, continues ✓
- [ ] Promoted field missing in YAML → defaults to false ✓
- [ ] rand.Read() fails → error returned early ✓
- [ ] Feedback.jsonl grows unbounded → **⚠️ No rotation**
- [ ] Dashboard datasource disappears → **⚠️ Fails silently**
- [ ] AlertMatrix has duplicate thresholds → **⚠️ Returns first**

---

## Detailed Review Criteria

### 1. Alert Threshold Correctness

**AlertMatrix (6 thresholds defined):**

| Metric | Threshold | Severity | Comparison | Status |
|--------|-----------|----------|------------|--------|
| `prompts_avg_confidence` | < 0.70 | WARNING | `value < threshold` | ✅ Fixed |
| `prompts_avg_confidence` | < 0.50 | CRITICAL | `value < threshold` | ✅ Fixed |
| `prompts_promoted_count` | 0 | CRITICAL | `value <= 0` | ✅ Fixed |
| `prompts_feedback_success_rate` | < 0.80 | WARNING | `value < threshold` | ✅ Fixed |
| `prompts_daily_pipeline_duration` | > 300s | WARNING | `value > threshold` | ✅ Fixed |
| `prompts_trinity_facts_exported` | 0 | ERROR | `value <= 0` | ✅ Fixed |

**Verification:** CheckThreshold now uses metric-specific logic (switch statement, not blanket `<`)

### 2. Durability & Persistence

**Promoted Prompt Flow:**
```
User POST /registry/promote
  ↓
Check registry entry confidence >= 0.8
  ↓
Load prompt from disk
  ↓
Set Promoted = true
  ↓
SavePrompt(prompt, scope) [writes YAML with "promoted: true"]
  ↓
RebuildIndex() [loads all prompts from disk]
  ↓
Index now contains promoted=true flag
```

**Verification:** Needs test: promote → rebuild → verify promoted persists

### 3. Concurrency Safety

**RegistryHandler.index:**
- Reads: GetIndex(), ListRegistry() → `mu.RLock()`
- Writes: RebuildIndex() → `mu.Lock()`
- **Risk:** Recursive read locks? (Go allows this, OK)
- **Risk:** Lock held during I/O? (No, I/O outside lock)

**FeedbackManager:**
- Writes: SubmitFeedback() appends to feedback.jsonl
- **Risk:** Multiple goroutines appending? (File I/O should be atomic, but test concurrent appends)

**Governor.RecordFeedback:**
- Calls feedbackManager.SubmitFeedback()
- Calls loader.LoadAll() (expensive, but after feedback submitted)
- **Risk:** LoadAll() during concurrent updates? (Snapshot from disk, race possible but data-safe)

### 4. Security Analysis

**Crypto/Rand Usage:**
- `rand.Read(sessionBytes)` → 8 bytes → 32 hex chars
- `rand.Read(feedbackIDBytes)` → 8 bytes → 16 hex chars
- **Entropy:** 64 bits each, sufficient for non-cryptographic uniqueness
- **Collision risk:** (2^64)² = negligible for practical purposes
- **Error handling:** Both check `err != nil` before using bytes ✓

**Secrets:**
- No hardcoded API keys in alerts.go
- No credentials in dashboard JSON ✓
- No passwords in playbook YAML ✓

### 5. Logging & Observability

**Logger Fallback (Issue #5):**
```go
if err != nil {
    fmt.Fprintf(os.Stderr, "⚠️  Logger fallback: Failed to open %s: %v\n", logFile, err)
    f = os.Stderr
}
```
- ✅ Warning visible on stderr
- ✅ Includes filepath and error details
- ✅ Guaranteed to execute before first log

**AddSource Toggle (Issue #3):**
```go
handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
    Level:     slog.LevelInfo,
    AddSource: false,  // ← Disabled for performance
})
```
- ✅ No file:line in every log message
- ✅ ~30% performance improvement expected
- ⚠️ Loss of source location in logs (acceptable trade-off)

### 6. Configuration & Maintainability

**Alert Configuration:**
- AlertMatrix is a global var, immutable after init ✓
- New thresholds require code change (not ideal, but explicit) ⚠️
- Severity levels: WARNING, CRITICAL, ERROR ✓
- Recovery procedures documented in code ✓

**Dashboard Configuration:**
- JSON templates hardcoded in /tmp (manual import step)
- Queries use standard Prometheus metric names ✓
- Thresholds (red/yellow/green) aligned with alerts ✓

**Playbook Configuration:**
- YAML files manual deploy to AWX (no auto-provisioning)
- Alert thresholds match MONITORING_PLAN.md ✓
- Recovery steps documented in playbook comments ✓

### 7. Error Handling Strategy

| Scenario | Handling | Acceptable? |
|----------|----------|------------|
| Log file creation fails | Fallback to stderr + warning | ✅ Yes |
| Loki unreachable | Logs still written locally, cached | ✅ Yes |
| Promoted field missing | Defaults to false | ✅ Yes |
| rand.Read() fails | Return HTTP 500 | ✅ Yes |
| Feedback.jsonl unavailable | Append fails, error to client | ✅ Yes |
| Dashboard query timeout | Panel shows "No data" | ⚠️ Silent, no alert |
| AlertMatrix access race | Read-only after init, no lock | ✅ Yes |

---

## Integration Points (Must Work Together)

```
Logger (Phase 1)
  ↓
Structured JSON output
  ↓
Promtail (Phase 2)
  ↓
Loki log store
  ↓
Grafana (Phase 3)
  ↓
Live dashboards
  ↓
AWX jobs (Phase 4)
  ↓
Threshold checks
  ↓
Alert system (Phase 5)
  ↓
Slack/email notifications
```

**Test Plan:**
1. Start prompts-mcp service
2. Submit feedback that lowers confidence below 0.70
3. Verify log entry appears in `/var/log/prompts-mcp/service.log`
4. Verify Loki receives log via promtail
5. Verify Grafana dashboard shows updated confidence
6. Trigger AWX job manually, verify it reads metrics
7. Simulate alert condition, verify CheckThreshold fires

---

## Review Methodology

**Agent Review (Comprehensive):**
- Automated correctness checks
- Security analysis
- Concurrency audit
- Performance profiling
- Edge case coverage

**Expected Output:**
- Confirmed bugs (with reproduction steps)
- Plausible concerns (with risk assessment)
- Code quality observations
- Refactoring opportunities

---

## Known Limitations & TODOs

| Item | Reason | Priority |
|------|--------|----------|
| `CheckThreshold` not wired | Stub framework, awaiting notification channel implementation | P2 |
| No feedback.jsonl rotation | Could grow unbounded | P2 |
| No dashboard caching | Could impact performance under load | P3 |
| Alert deduplication missing | Same alert fires multiple times | P3 |
| No multi-region support | Single Loki instance | P4 |
| Log level hardcoded to Info | Can't adjust verbosity without code change | P4 |

---

## Success Criteria

**This review passes if:**
- ✅ No confirmed correctness bugs
- ✅ Security posture verified
- ✅ Thread safety guaranteed
- ✅ All edge cases handled appropriately
- ✅ Integration points validated
- ⚠️ Plausible concerns documented with mitigation

**Target for Production Ship:**
- Fix all ❌ issues before merge
- Document all ⚠️ concerns in operational runbooks
- Schedule 🔧 refactoring for next quarter

---

**Review Status:** COMPREHENSIVE REVIEW IN PROGRESS  
**Expected Completion:** Within this session  
**Reviewer:** Advanced Code Reviewer Agent (ab50017ae477daf97)
