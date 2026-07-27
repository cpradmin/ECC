# Refactoring Playbooks: Scenario B Implementation

This document contains the three playbooks for implementing Scenario B (6-6-6 split) across alerts, loader, and registry.

## Overview

**Timeline:** 12 weeks (3 months, concurrent execution)
**Structure:** Tetrahedron topology (4 vertices: 3 teams + 1 system, 6 edges: module groups)
**Goal:** 42/100 balance → 78/100 balance, with perfect coherence (10/10)

See full playbook details in `PLAYBOOKS_REFACTORING_DETAILED.md`.

### Quick Reference

| Phase | Month | Group | Duration | Milestones |
|-------|-------|-------|----------|-----------|
| 1.1 | 1 | Alerts Design | Week 1-2 | Dependency diagram, test plan, team alignment |
| 1.2 | 1 | Alerts Migration | Week 2-3 | 8 incremental commits, all tests passing |
| 1.3 | 1 | Alerts Testing | Week 3-4 | 60+ unit tests, benchmarks, race detector clean |
| 1.4 | 1 | Alerts Deploy | Week 4+ | Production monitoring, feedback gathering |
| 2.x | 2 | Loader | Weeks 5-8 | Same pattern: design → migrate → test → deploy |
| 3.x | 3 | Registry | Weeks 9-12 | Same pattern: design → migrate → test → deploy |

### Success Metrics (After All Playbooks)

```
Cognitive Load:
  Before: 83K + 35K + 17K = 135K CLU (max module 83K)
  After:  Evenly distributed, max single module 12.5K ✅

Team Metrics:
  Ownership: Clear (Engineer 1: alerts, 2: loader, 3: registry)
  Onboarding: 2 days per engineer per group
  Maintenance: Surgical changes (1-3 modules touched)

Code Quality:
  Tests: 60+ (focused unit tests)
  Race Detector: 0 violations
  Benchmarks: No regression
  Code Review: Easy (165 LOC modules vs 982 LOC monolith)
```

## Playbook 1: Alerts Refactoring (Month 1)

### Phase 1.1: Design & Planning (Week 1-2)

**Goal:** Map alerts.go concerns to 6 modules with clear dependencies

**Modules:**
```
alerts_config.go     (~110 LOC) → Config loading, matrix definitions
alerts_validate.go   (~120 LOC) → Rule validation, schema checks
alerts_threshold.go  (~135 LOC) → Threshold evaluation, comparisons
alerts_handler.go    (~145 LOC) → Handler dispatch, route selection
alerts_notify.go     (~165 LOC) → Notification backends (Slack/PagerDuty)
alerts_message.go    (~167 LOC) → Message formatting, enrichment
alerts_state.go      (~100 LOC) → State machine, dedup, cooldown
```

**Dependency Flow (Linear, No Cycles):**
```
config → validate → threshold → handler → notify → state → message
```

**Tasks:**
- Week 1:
  - Map current alerts.go (982 LOC, 38 functions) to 7 concerns
  - Identify all imports (coupling analysis)
  - Create dependency diagram (verify no cycles)
  - Estimate lines per module

- Week 2:
  - Design test strategy (60+ tests total)
  - Plan concurrency tests (100 goroutine stress tests)
  - Set benchmark targets (before/after comparison)
  - Schedule team alignment meeting

### Phase 1.2: Code Migration (Week 2-3)

**Strategy:** Incremental extraction with micro-commits

**Steps (9 commits total):**

1. Create empty modules + stubs (no breaks)
2. Extract alerts_config → AlertMatrix, EmittedMetrics
3. Extract alerts_validate → ValidateAlertMatrix
4. Extract alerts_threshold → CheckThreshold
5. Extract alerts_handler → AlertHandler struct
6. Extract alerts_state → alertState, cooldown tracking
7. Extract alerts_notify → TriggerAlert, Slack SDK
8. Extract alerts_message → FormatAlertMessage, enrichment
9. Remove old alerts.go (migration complete)

**Commit Pattern:**
```
Each commit must:
✅ Pass all tests
✅ Have no behavior changes
✅ Be reviewable (~100 lines changed)
✅ Include rationale in commit message

Example:
  refactor: extract alerts_notify.go from monolithic alerts.go
  
  Move Slack SDK integration (TriggerAlert, retry logic) to dedicated
  module. No behavior changes; only code reorganization.
  
  - Extract TriggerAlert(), PostWebhook wrapper, retry logic
  - Add alerts_notify_test.go for Slack-specific edge cases
  - Update imports in alerts_handler.go
  - All tests passing, race detector clean
```

### Phase 1.3: Testing & Validation (Week 3-4)

**Unit Tests (~60 total):**
- alerts_config_test.go: 10 tests (config loading, validation)
- alerts_validate_test.go: 8 tests (rule validation)
- alerts_threshold_test.go: 12 tests (all 6 comparison types)
- alerts_handler_test.go: 6 tests (routing logic)
- alerts_notify_test.go: 8 tests (Slack retry, error handling)
- alerts_message_test.go: 6 tests (template rendering)
- alerts_state_test.go: 10 tests (dedup, concurrency)

**Concurrency Tests:**
- CheckThreshold with 100 concurrent goroutines
- State machine concurrent updates (100 goroutines)
- Message formatting under load

**Benchmarks (No Regression):**
- BenchmarkCheckThreshold: 1000 ns/op
- BenchmarkTriggerAlert: 10000 ns/op
- BenchmarkFormatAlertMessage: 2000 ns/op

**Race Detector:**
```
go test -race -count=3 ./handlers/alerts_*_test.go
✅ 0 violations expected
```

### Phase 1.4: Deployment & Monitoring (Week 4+)

**Pre-Deployment:**
- Final integration tests (E2E: low confidence → breach → alert → dedup → Slack)
- Load test (100 req/s for 5 minutes)
- Chaos test (timeouts, backend errors)

**Post-Deployment Monitoring:**
- Alert latency metric (should match baseline)
- Retry count metric
- Dedup suppression rate
- Slack webhook error rate

**Success Criteria:**
- ✅ No functional changes (same behavior)
- ✅ 100% test coverage of all 6 modules
- ✅ Benchmarks show no regression
- ✅ Race detector clean
- ✅ Production monitoring shows no anomalies

---

## Playbook 2: Loader Refactoring (Month 2)

**Same structure as Playbook 1, applied to loader.go (788 LOC, 27 functions)**

**Modules:**
```
loader_core.go         (~95 LOC)   → Entry point, orchestration
loader_scan.go         (~105 LOC)  → Directory scanning, file discovery
loader_parse.go        (~160 LOC)  → YAML parsing, type coercion
loader_deserialize.go  (~110 LOC)  → Object deserialization
loader_serialize.go    (~105 LOC)  → Serialization (for export)
loader_frontmatter.go  (~95 LOC)   → Frontmatter extraction & merging
loader_cache.go        (~115 LOC)  → TTL cache, invalidation
loader_validate.go     (~90 LOC)   → Schema validation
```

**Timeline:**
- Week 5-6: Design & planning
- Week 6-7: Incremental migration (8 commits)
- Week 7-8: Testing, benchmarks, race detector
- Week 8+: Deploy, monitor

---

## Playbook 3: Registry Refactoring (Month 3)

**Same structure, applied to registry.go (615 LOC, 14 functions)**

**Modules:**
```
registry_index.go    (~95 LOC)  → Index building, versioning
registry_load.go     (~95 LOC)  → Load from disk
registry_save.go     (~90 LOC)  → Save to disk
registry_cache.go    (~105 LOC) → Cache layer, deep copy
registry_query.go    (~115 LOC) → Query execution, pagination
registry_filter.go   (~90 LOC)  → Filtering by domain/confidence
registry_sort.go     (~75 LOC)  → Sorting strategies
```

**Timeline:**
- Week 9-10: Design & planning
- Week 10-11: Incremental migration (7 commits)
- Week 11-12: Testing, benchmarks, race detector
- Week 12+: Deploy, monitor

---

## Tetrahedron Architecture

The refactoring naturally forms a **tetrahedron** structure:

```
                  SYSTEM (Apex)
                  /|\
                 / | \
                /  |  \
               /   |   \
              /    |    \
        ALERTS---LOADER---REGISTRY
              \    |    /
               \   |   /
                \  |  /
                 \ | /
              (Center: handlers.go)
```

**4 Vertices:**
1. Alerts Team (6 modules, ~820 LOC)
2. Loader Team (6 modules, ~790 LOC)
3. Registry Team (6 modules, ~615 LOC)
4. System Apex (unified facade)

**6 Edges (Connections):**
1. Alerts ↔ System (alert state notifications)
2. Loader ↔ System (data ingestion)
3. Registry ↔ System (query serving)
4. Alerts ↔ Loader (config parsing)
5. Loader ↔ Registry (index building)
6. Alerts ↔ Registry (context queries)

**Why Tetrahedron?**
- Strongest geometric structure (can't collapse)
- Most efficient load distribution (perfect symmetry)
- Proven in nature (diamonds, methane, pyramids)
- Natural for 3-team organization

---

## Execution Checklist

### Month 1 (Alerts)
```
Week 1-2:
□ Map alerts.go concerns to modules
□ Create dependency diagram (no cycles?)
□ Design test strategy (60+ tests)
□ Get team agreement on split

Week 2-3:
□ Create 7 empty module files
□ 9 incremental commits (each passing tests)
□ Update imports in handlers.go

Week 3-4:
□ Write 60+ unit tests
□ Run benchmarks (compare before/after)
□ Run race detector: go test -race -count=3
□ Code review (no behavior changes)

Week 4+:
□ Merge to main
□ Deploy to production
□ Monitor latency, errors, Slack delivery
□ Gather team feedback
```

### Month 2 (Loader)
```
Same structure: Design → Migrate → Test → Deploy
Expected: Similar outcomes (all tests passing, no regression)
```

### Month 3 (Registry)
```
Same structure: Design → Migrate → Test → Deploy
Expected: Similar outcomes (all tests passing, no regression)
```

### After All Playbooks (Week 13+)
```
□ Review tetrahedron structure (is it sound?)
□ Benchmark overall system (throughput, latency)
□ Review team satisfaction (easier to maintain?)
□ Plan next phase (features, performance, scaling)
□ Update architecture documentation
□ Celebrate! 🎉
```

---

## Risk Mitigation

**Risk:** Migration breaks functionality
**Mitigation:** Incremental commits, each passing all tests

**Risk:** Performance regression
**Mitigation:** Benchmarks before/after, race detector clean

**Risk:** Team resistance (too many modules)
**Mitigation:** Clear ownership model, tetrahedron visualization

**Risk:** Onboarding confusion
**Mitigation:** Each module has 1-2 responsibilities (easy to learn)

---

## Success Metrics (Post-Completion)

### Quantitative
- ✅ Cognitive load: 135K CLU → evenly distributed, max 12.5K
- ✅ Test coverage: 60+ tests per group
- ✅ Race detector: 0 violations
- ✅ Benchmarks: No regression
- ✅ Code review time: 60% faster (165 LOC modules vs 982 LOC monoliths)

### Qualitative
- ✅ Team ownership: Clear (who owns what module)
- ✅ Maintenance: Surgical changes (1-3 modules touched per modification)
- ✅ Scalability: Tetrahedron topology proven at enterprise scale
- ✅ Beauty: Geometric elegance (developers understand the design at a glance)

---

## See Also

- `ARCHITECTURAL_PATTERN_3_6_9.md` — Universal pattern applicable to any codebase
- `MODULE_BALANCE_ANALYSIS.md` — Detailed scenario comparison (A vs B vs C)
- `SESSION_FINDINGS_SUMMARY.md` — Executive summary and key findings
