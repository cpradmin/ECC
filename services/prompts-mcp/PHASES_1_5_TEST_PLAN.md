# Phases 1-5 Integration Test Plan

**Objective:** Validate that all monitoring phases work together correctly  
**Status:** Awaiting comprehensive code review completion  
**Test Environment:** localhost:8762 (prompts-mcp service)

---

## Phase 1: Structured Logging ✅

### Unit Test: Logger Initialization
```bash
# Service starts with logger fallback visible (if log dir doesn't exist)
# Expected: "⚠️  Logger fallback" message to stderr if file creation fails
```

### Verification Steps:
```bash
# 1. Service is running
curl -s http://localhost:8762/mcp/health
# Expected: {"status":"healthy",...}

# 2. Check log file exists
ls -la /var/log/prompts-mcp/service.log
# Expected: File exists with JSON entries

# 3. Sample log entry (should be JSON, not have file:line due to AddSource: false)
tail -1 /var/log/prompts-mcp/service.log | jq .
# Expected: {"time":"...", "level":"INFO", "msg":"...", NO "source":"file:line"}
```

### Test: AddSource Performance
```bash
# Performance test: logging with AddSource: false
# Generate 1000 log entries and measure time
time for i in {1..1000}; do
  curl -s -X POST http://localhost:8762/mcp/prompts/feedback \
    -H "Content-Type: application/json" \
    -d '{"prompt_id":"test-$i", "success":true}' > /dev/null
done
# Baseline: ~30% faster with AddSource: false
```

---

## Phase 2: Log Aggregation ✅

### Prerequisite: Loki Running
```bash
# Verify Loki is accessible
curl -s http://loki:3100/ready
# Expected: 200 OK
```

### Test: Promtail Configuration
```bash
# If promtail running, verify it scrapes logs
curl -s http://localhost:9081/api/prom/tail/prompts-mcp-service
# Expected: Recent log entries from prompts-mcp

# Or check Loki directly for prompts-mcp logs
curl -s 'http://loki:3100/loki/api/v1/query?query={job="prompts-mcp"}' | jq .
```

### Test: Structured Fields in Logs
```bash
# Extract a log entry and verify fields
tail -1 /var/log/prompts-mcp/service.log | jq 'keys'
# Expected: ["time", "level", "msg", and other structured fields]
```

---

## Phase 3: Grafana Dashboards ✅

### Prerequisites:
- Grafana running
- Prometheus datasource configured
- Prometheus scraping `localhost:8762/mcp/metrics`

### Test: Dashboard Import
```bash
# 1. Import registry-health-dashboard.json
curl -X POST https://grafana.example.com/api/dashboards/db \
  -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H "Content-Type: application/json" \
  -d @/tmp/registry-health-dashboard.json

# 2. Verify dashboard appears in Grafana UI
# 3. Check panels are rendering data (not showing "No data")
```

### Test: Dashboard Queries
```bash
# Query each metric directly
curl -s 'http://localhost:9090/api/v1/query?query=prompts_registry_total'
# Expected: {result_type: "instant", value: [..., 2]}

curl -s 'http://localhost:9090/api/v1/query?query=prompts_avg_confidence'
# Expected: {result_type: "instant", value: [..., 0.71]}

curl -s 'http://localhost:9090/api/v1/query?query=prompts_promoted_count'
# Expected: {result_type: "instant", value: [..., 0]}
```

### Test: Dashboard Rendering
```bash
# Manually open dashboards in Grafana
# 1. Registry Health → Should show:
#    - Total Prompts gauge (2)
#    - Average Confidence gauge (0.71, yellow threshold)
#    - Prompts by Confidence pie chart

# 2. Governor Intelligence → Should show:
#    - Task types table
#    - Domain patterns
#    - High-confidence domains (none, if avg < 0.8)

# 3. Pipeline Health → Should show:
#    - Pipeline steps status
#    - Duration of last run
#    - Error rate gauge
```

---

## Phase 4: AWX Integration ✅

### Prerequisites:
- AWX running
- Job templates deployed from `/tmp/check_*.yml`
- Credentials configured

### Test: Manual Job Execution
```bash
# 1. Trigger Registry Health Check
awx job_templates launch 123 --monitor

# 2. Verify job output
# Expected output:
#   - Registry metrics extracted
#   - Average confidence value extracted
#   - If < 0.70: alert fired (but won't page, just logs)

# 3. Check job logs
awx jobs show 456 --format json | jq '.extra_vars, .status'
```

### Test: Job Template Scheduling
```bash
# Set up hourly schedule for Registry Health Check
awx schedules create \
  --name "Registry Health Hourly" \
  --rrule "FREQ=HOURLY;INTERVAL=1" \
  --job-template 123

# Verify schedule in AWX UI
# Verify job runs at expected time (check job history)
```

---

## Phase 5: Alerting Framework ✅

### Test 1: CheckThreshold Logic (NEW FIX)
```python
# Unit test: verify CheckThreshold comparison logic

# Test case 1: Confidence metric (lower is worse)
alert = AlertThreshold("prompts_avg_confidence", 0.70, SeverityWarning)
assert CheckThreshold("prompts_avg_confidence", 0.65, SeverityWarning) == True  # 0.65 < 0.70
assert CheckThreshold("prompts_avg_confidence", 0.75, SeverityWarning) == False # 0.75 >= 0.70

# Test case 2: Duration metric (higher is worse) - FIXED
alert = AlertThreshold("prompts_daily_pipeline_duration_seconds", 300, SeverityWarning)
assert CheckThreshold("prompts_daily_pipeline_duration_seconds", 350, SeverityWarning) == True  # 350 > 300
assert CheckThreshold("prompts_daily_pipeline_duration_seconds", 250, SeverityWarning) == False # 250 <= 300

# Test case 3: Count metric (zero is bad) - FIXED
alert = AlertThreshold("prompts_promoted_count", 0, SeverityCritical)
assert CheckThreshold("prompts_promoted_count", 0, SeverityCritical) == True   # 0 <= 0
assert CheckThreshold("prompts_promoted_count", 1, SeverityCritical) == False  # 1 > 0
```

### Test 2: Session ID Generation (NEW FIX)
```bash
# Manual test: Verify session IDs are crypto/rand
for i in {1..5}; do
  curl -s 'http://localhost:8762/mcp/prompts/governor/route?task_type=test' \
    | jq '.routing_session'
done
# Expected output: 5 different routing_session values like "gov-sess-abc123def456..."
# Verify: No pattern, all different, hex-encoded
```

### Test 3: Feedback ID Generation (NEW FIX)
```bash
# Manual test: Verify feedback IDs are crypto/rand
for i in {1..5}; do
  curl -s -X POST http://localhost:8762/mcp/prompts/governor/feedback \
    -H "Content-Type: application/json" \
    -d '{
      "prompt_id": "test-prompt",
      "success": true
    }' | jq '.routing_session'
done
# Expected: 5 different IDs like "gov-feedback-abc123def456..."
```

### Test 4: Promoted Prompt Durability (NEW FIX)
```bash
# Test: Promote prompt and verify it survives rebuild

# Step 1: Get a prompt with confidence >= 0.8
curl -s 'http://localhost:8762/mcp/prompts/registry?min_confidence=0.8' | jq '.prompts[0].id'

# Step 2: Promote it (if no prompts >= 0.8, skip this test)
# Create a feedback that raises confidence
curl -s -X POST http://localhost:8762/mcp/prompts/feedback \
  -H "Content-Type: application/json" \
  -d '{
    "prompt_id": "router-example-1",
    "success": true,
    "confidence_update": 0.15
  }'

# Step 3: Promote the prompt
curl -s -X POST http://localhost:8762/mcp/prompts/registry/promote \
  -H "Content-Type: application/json" \
  -d '{"prompt_id": "router-example-1"}'
# Expected: {"status": "promoted", ...}

# Step 4: Rebuild registry
curl -s -X POST http://localhost:8762/mcp/prompts/registry/rebuild

# Step 5: Verify promoted flag persisted
# Check YAML file directly
grep -i "promoted" ~/.local/share/ecc-prompts/instincts/personal/router-prompts/router-example-1.yaml
# Expected: "promoted: true"
```

### Test 5: Alert Threshold Triggering
```bash
# Manual test: Lower confidence below threshold and trigger alert

# Step 1: Lower confidence
curl -s -X POST http://localhost:8762/mcp/prompts/feedback \
  -H "Content-Type: application/json" \
  -d '{
    "prompt_id": "router-example-1",
    "success": false,
    "confidence_update": -0.25
  }' | jq '.new_confidence'

# Step 2: Check if CheckThreshold would fire
# (Currently not wired to notifications, but would log)
grep "alert_triggered" /var/log/prompts-mcp/service.log
# Expected: If confidence < 0.70, should see log entry
```

---

## Integration Test: End-to-End

### Scenario: Feedback → Logging → Aggregation → Dashboard → Alert

```bash
# 1. Start with baseline metrics
curl -s http://localhost:8762/mcp/metrics | grep prompts_avg_confidence

# 2. Submit negative feedback (multiple times to lower confidence)
for i in {1..3}; do
  curl -s -X POST http://localhost:8762/mcp/prompts/feedback \
    -H "Content-Type: application/json" \
    -d '{"prompt_id": "router-example-1", "success": false}'
done

# 3. Check updated metrics
curl -s http://localhost:8762/mcp/metrics | grep prompts_avg_confidence
# Expected: Lower value

# 4. Verify log entries created
tail -5 /var/log/prompts-mcp/service.log | jq '.msg'
# Expected: feedback_submitted entries

# 5. Verify logs in Loki (if available)
curl -s 'http://loki:3100/loki/api/v1/query?query={msg="feedback_submitted"}' | jq '.data.result | length'
# Expected: > 0 entries

# 6. Check Grafana dashboard update
# Open registry-health-dashboard.json
# Verify "Average Confidence" panel shows new value

# 7. Manually trigger alerting (if wired)
# Query AWX or check logs for alert fired
tail -10 /var/log/prompts-mcp/service.log | grep alert_triggered
# Expected: If avg_confidence < 0.70, alert_triggered log entry
```

---

## Success Criteria

### Phase 1: Structured Logging
- ✅ Service logs JSON to `/var/log/prompts-mcp/service.log`
- ✅ Fallback to stderr is visible (not silent)
- ✅ AddSource disabled (no file:line in logs)

### Phase 2: Log Aggregation
- ✅ Promtail receives logs from service.log
- ✅ Loki stores structured logs
- ✅ Logs queryable by job, domain, action fields

### Phase 3: Grafana Dashboards
- ✅ 3 dashboards import without error
- ✅ All panels query live data (not "No data")
- ✅ Thresholds (red/yellow/green) match alert levels

### Phase 4: AWX Integration
- ✅ 3 job templates execute without error
- ✅ Jobs extract metrics correctly
- ✅ Job output includes alert logic (threshold checks)

### Phase 5: Alerting
- ✅ CheckThreshold uses correct comparison per metric
- ✅ Session IDs are unique and unpredictable
- ✅ Feedback IDs are unique and unpredictable
- ✅ Promoted prompts survive rebuilds
- ✅ AlertMatrix covers all 6 metrics

---

## Known Issues & Workarounds

| Issue | Workaround | Priority |
|-------|-----------|----------|
| CheckThreshold not wired to notifications | Stub framework, logs only | P1 |
| Feedback.jsonl unbounded growth | No rotation strategy | P2 |
| Dashboard datasource failure silent | No alert on dashboard error | P2 |
| AlertMatrix duplicates use first match | Keep AlertMatrix unique | P3 |

---

## Test Execution Checklist

- [ ] Phase 1: Logging test (PASS/FAIL)
- [ ] Phase 2: Log aggregation test (PASS/FAIL)
- [ ] Phase 3: Dashboard rendering test (PASS/FAIL)
- [ ] Phase 4: AWX job execution test (PASS/FAIL)
- [ ] Phase 5: Alert threshold logic test (PASS/FAIL)
- [ ] Integration: End-to-end flow test (PASS/FAIL)
- [ ] Concurrency: Multiple concurrent feedback (PASS/FAIL)
- [ ] Edge case: Log directory doesn't exist (PASS/FAIL)
- [ ] Edge case: rand.Read() fails (PASS/FAIL)
- [ ] Edge case: Promoted prompt rebuild cycle (PASS/FAIL)

---

**Test Status:** AWAITING COMPREHENSIVE CODE REVIEW  
**Expected Completion:** This session  
**Test Runner:** Advanced Code Reviewer Agent + Manual Validation
