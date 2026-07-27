# Prompts-MCP: Slow-Acting Areas Analysis

## Areas We FOUND & FIXED ✅

| Area | Issue | Severity | Fixed | Improvement |
|------|-------|----------|-------|------------|
| LoadAll() calls | No caching, O(n) per request | CRITICAL | ✅ 60s TTL | 1.2ms → 11.6µs (105×) |
| Metrics scrape | Recompute every scrape, O(n) | CRITICAL | ✅ 60s TTL | 7.34ms → 52µs (99.3%) |
| Bubble sorts | O(n²) for 3 sort ops | MEDIUM | ✅ sort.Slice | 1.6ms → 310µs (5×) |
| Feedback updates | Race = lost update, retry? | CRITICAL | ✅ Atomic | No retry loop; atomic now |
| JSON encoding | Errors ignored, retry? | CRITICAL | ✅ Pre-check | Encode to buffer; fail clean |
| Panic crashes | Retry whole request? | HIGH | ✅ Recovery | Returns 500, no retry |
| Nil slice append | Wasted allocations | MEDIUM | ✅ Pre-alloc | N/A |

---

## Areas We MAY HAVE MISSED 🔍

### 1. **HTTP Request Parsing Overhead**
**Finding:** Go net/http takes ~400ms to **drain** large responses on loopback
- **Not our bug**, but environmental
- **Impact:** `/prompts/list` and `/prompts/export` could be slow if response > 64KB
- **What we measured:** Listed endpoints (1000 items = 110KB)
- **What we didn't test:** 10,000+ items or streaming responses
- **Recommendation:** Implement response streaming for large payloads

**Slow Acting?** YES — latency scales with response size, not computation

---

### 2. **Request Body Parsing for Large Imports**
**Finding:** We added MaxBytes(10MB) but didn't measure parse time
- **What we fixed:** Reject huge bodies (413 on exceed)
- **What we didn't measure:** Time to parse 10MB of JSON
- **Suspected bottleneck:** json.Decoder scanning 10MB line-by-line
- **Recommendation:** Stream decoder with batch processing

**Slow Acting?** POSSIBLY — parse time unknown for large imports

---

### 3. **Concurrent Goroutine Lifecycle**
**Finding:** We spawn goroutines in timeout logic (Phase 4) but never benchmarked goroutine spawning cost
- **Pattern:** `go func() { ... }()` for each I/O operation
- **Scale:** At 1000 req/s, this could be 1000 goroutines spawned/second
- **Suspected overhead:** Context switch cost per goroutine
- **Recommendation:** Goroutine pool or worker queue

**Slow Acting?** YES — goroutine creation cost scales with throughput

---

### 4. **Deep Copy Performance (Phase 2)**
**Finding:** We deep copy entire RegistryIndex on every GetIndex() call
- **Size:** 500 prompts → 500 slice + map copies
- **Measured:** We showed this is fast (reads are cached)
- **Unmeasured:** What if copy is called 1000x/sec during peak load?
- **Recommended:** Verify copy cost under sustained load

**Slow Acting?** POSSIBLY — only matters if cache TTL expires frequently

---

### 5. **Feedback JSONL Append-Only Writes**
**Finding:** Multiple concurrent feedback writes append to same file
- **What we fixed:** Added atomic rename for prompt saves
- **What we didn't fix:** Feedback JSONL is O(append), not indexed
- **Scale:** At 10+ feedback/sec, JSONL could become bottleneck
- **Recommended:** Pre-migrate to SQLite (Phase 6.2) sooner

**Slow Acting?** YES — append cost scales linearly with file size

---

### 6. **Memory Allocations Under Load**
**Finding:** We pre-allocate slices (good), but didn't profile allocations
- **What we measured:** Benchmark speedups
- **What we didn't measure:** Heap pressure, GC pauses, memory fragmentation
- **Tool missing:** pprof CPU/memory profiling under sustained load
- **Recommendation:** `go tool pprof` on 10-minute sustained load test

**Slow Acting?** UNKNOWN — need profiling data

---

### 7. **Goroutine Cleanup in Timeout Paths**
**Finding:** Phase 4 timeouts spawn goroutines that may not complete
- **Pattern:** `go func() { ...LoadAll() }()` + select on timeout
- **Risk:** If timeout fires, goroutine keeps running (background task still using CPU)
- **Scale:** At 100 concurrent timeouts, could have 100 orphaned goroutines
- **Recommendation:** Context cancellation to abort spawned work

**Slow Acting?** YES — orphaned goroutines accumulate over time

---

### 8. **Logger Synchronization**
**Finding:** All error logs go through same logger (no buffering)
- **What we fixed:** JSON encoding errors logged
- **What we didn't measure:** Logger contention at high throughput
- **Scale:** At 1000 req/s with errors, logger could be bottleneck
- **Recommendation:** Async logger or log buffer

**Slow Acting?** POSSIBLY — lock contention on logger mutex

---

## 3-6-9 PATTERN ANALYSIS

I'm not familiar with "3-6-9 pattern" in your context. Could mean:

### **Option A: Load Testing Pattern**
- Test at 3× normal load
- Test at 6× normal load
- Test at 9× normal load
- **Status:** We did spot checks; didn't do systematic 3-6-9 scaling

### **Option B: Security/Reliability (Rule of Three)**
- 3 backups
- 6 audits
- 9 checks
- **Status:** We have 1 backup mechanism (JSONL export); 2 audit layers (logs + metrics); unknown # of checks

### **Option C: Performance SLA Pattern**
- P3: 3-second max (background jobs)
- P6: 600ms max (secondary queries)
- P9: 90ms max (critical path)
- **Status:** We have timeouts but no formal SLA tiers

### **Option D: Monitoring Pattern**
- Check every 3 seconds
- Alert after 6 failures
- Escalate after 9 failures
- **Status:** Phase 5 alert has duration field but dedup is hardcoded 5min

---

## RECOMMENDED IMMEDIATE ACTIONS

### 🔴 **Critical (Do Now)**
1. [ ] Profile memory/CPU under 10-minute sustained load test
   - Tool: `go tool pprof http://localhost:8762/debug/pprof/profile?seconds=60`
   - Measure: Heap allocations, goroutine count, GC pauses
   - Success criterion: Heap growth < 100MB, goroutines stable

2. [ ] Test response streaming for large payloads
   - Measure: `/prompts/list?limit=10000` latency
   - Measure: Memory usage during response write
   - Success criterion: Sub-second latency even at 50KB/s network

3. [ ] Context cancellation in timeout paths
   - Review: Phase 4 timeout goroutines
   - Fix: Propagate context.Done() to abort spawned work
   - Test: Verify goroutines exit within 100ms of timeout

---

### 🟡 **High (Do This Week)**
4. [ ] Goroutine pool for I/O operations
   - Current: Spawn goroutine per request (risky at high scale)
   - Plan: Worker pool of 100 goroutines (tunable)
   - Benchmark: Throughput improvement from reuse

5. [ ] Async/buffered logging
   - Current: All logs block on logger mutex
   - Plan: Log buffer (10K buffer) + background flush
   - Measure: Logger lock contention reduction

6. [ ] Test feedback JSONL performance
   - Current: Unknown perf at 100+ records/sec
   - Plan: Sustained feedback test @ 10 req/sec for 1 hour
   - Success: No latency degradation over time

---

### 🟢 **Medium (This Month)**
7. [ ] Load test at 3-6-9× normal throughput
   - 3×: 1,000 req/s (spot check we did)
   - 6×: 2,000 req/s
   - 9×: 3,000 req/s
   - Measure: P50, P95, P99 latencies; error rates

8. [ ] Pre-migrate feedback to SQLite
   - Phase 6.2: Unblocks everything else
   - Start before JSONL becomes bottleneck (now is good time)

---

## Performance Testing Checklist

- [ ] CPU profiling under sustained load (go tool pprof)
- [ ] Memory profiling (heap growth, allocation rate)
- [ ] Goroutine profiling (goroutine count over time)
- [ ] Lock contention profiling (mutex waits)
- [ ] Response streaming for large payloads
- [ ] 3-6-9 throughput scaling test
- [ ] Sustained test: 1 hour @ peak throughput
- [ ] Error injection: network timeouts, malformed input, slow storage

---

## WHICH PATTERN DO YOU MEAN?

**Please clarify:**
1. Load testing (3x, 6x, 9x)?
2. Rule of three (3 layers, 6 checks, 9 metrics)?
3. SLA tiers (3s, 600ms, 90ms)?
4. Alert escalation (3 fails, 6 fails, 9 fails)?
5. Something else?

Once you clarify, I can analyze if we're following it + recommend fixes.
