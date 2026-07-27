# Phase 5: Monitoring Deployment Guide

**Status:** Bug fixes complete ✅ | Infrastructure ready for deployment | Alerting thresholds ready

---

## Service Health

- ✅ **prompts-mcp** running on `localhost:8762`
- ✅ **Metrics endpoint** (`/mcp/metrics`) returning Prometheus format
- ✅ **All 14 API endpoints** wired and operational
- ✅ **4 critical bugs fixed** (session IDs, logger fallback, AddSource, promotion durability)

---

## Part 1: Import Grafana Dashboards

**Location:** `/tmp/*.json` (3 dashboard templates)

### Via Grafana UI
1. Navigate to **Dashboards → Import**
2. Upload each file:
   - `registry-health-dashboard.json`
   - `governor-dashboard.json`
   - `pipeline-dashboard.json`
3. Select **Prometheus** as datasource for each
4. Save with tags: `prompts-mcp`, `monitoring`

### Via Grafana API
```bash
curl -X POST https://grafana.d3its.us/api/dashboards/db \
  -H "Authorization: Bearer $GRAFANA_API_KEY" \
  -H "Content-Type: application/json" \
  -d @/tmp/registry-health-dashboard.json
```

**Result:** 3 dashboards live, querying `prompts_*` metrics every 60s

---

## Part 2: Deploy AWX Job Templates

**Location:** `/tmp/check_*.yml` (3 monitoring playbooks)

### Via AWX UI
1. Navigate to **Templates → Create Job Template**
2. For each playbook:
   - **Name:** Check Registry Health / Governor Intelligence / Pipeline Health
   - **Project:** Select or create "prompts-mcp-monitoring"
   - **Playbook:** Select `check_registry_health.yml` (etc.)
   - **Inventory:** localhost
   - **Credentials:** Machine (local)
   - **Extra Variables:** (leave blank)
3. Save and test each template

### Via AWX API
```bash
# First, get project ID
PROJECT_ID=$(curl -s -H "Authorization: Bearer $AWX_TOKEN" \
  https://awx.d3its.us/api/v2/projects/?name=prompts-mcp-monitoring \
  | jq -r '.results[0].id')

# Create template
curl -X POST https://awx.d3its.us/api/v2/job_templates/ \
  -H "Authorization: Bearer $AWX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Prompts-MCP Registry Health Check",
    "project": '${PROJECT_ID}',
    "playbook": "check_registry_health.yml",
    "inventory": 1,
    "ask_variables_on_launch": false
  }'
```

### Scheduling (Optional)
- **Registry Health Check:** Hourly (`:00`)
- **Governor Intelligence:** Every 30 min (`:00`, `:30`)
- **Pipeline Health Check:** 02:05 UTC daily (after pipeline runs at 02:00)

**Result:** 3 job templates live, can be triggered manually or on schedule

---

## Part 3: Configure Alerting

### Alert Matrix (from MONITORING_PLAN.md)

| Metric | Threshold | Severity | Action |
|--------|-----------|----------|--------|
| `prompts_avg_confidence` | < 0.70 | WARNING | Notify team |
| `prompts_avg_confidence` | < 0.50 | CRITICAL | Page on-call |
| `prompts_promoted_count` | 0 | CRITICAL | Escalate |
| `prompts_feedback_success_total` | (ratio) < 80% | WARNING | Check feedback |
| `prompts_daily_pipeline_duration_seconds` | > 300 (5 min) | WARNING | Investigate |
| Trinity facts/day | 0 | ERROR | Pipeline failed |

### Grafana Alert Rules
1. Navigate to **Alerting → Alert Rules**
2. Create rule: "Low Registry Confidence"
   ```
   prompts_avg_confidence < 0.70
   For: 5m
   Labels: severity=warning, team=prompts-mcp
   Annotation: "Registry confidence {{ $value }} is below 0.70 threshold"
   ```
3. Repeat for CRITICAL threshold (< 0.50)

### Notification Channels
1. **Slack:** Configure in **Alertmanager → Contact Points**
   - Channel: `#prompts-mcp-alerts`
   - Mention: `@prompts-team` on CRITICAL
2. **Email:** On-call rotation (configure per team policy)

### Runbooks
- **Low Confidence:** Check feedback logs, identify failing prompts, run diagnostics
- **No High-Confidence Domains:** Wait for pipeline, manually test prompts
- **Pipeline Failure:** Check AWX logs, verify Selah (10.174.210.10:11434), re-run
- **Governor Degradation:** Analyze feedback success rate, check routing logic

---

## Part 4: End-to-End Test

### 1. Verify Data Flow
```bash
# Test metrics endpoint
curl http://localhost:8762/mcp/metrics | grep prompts_

# Check for Loki ingestion (if promtail running)
curl http://loki:3100/api/prom/label/job/values
```

### 2. Trigger Monitoring Jobs
```bash
# Via AWX CLI (if installed)
awx job_templates launch 123 --monitor

# Via curl
curl -X POST https://awx.d3its.us/api/v2/job_templates/123/launch/ \
  -H "Authorization: Bearer $AWX_TOKEN"
```

### 3. Verify Dashboard Queries
- Open **Registry Health** dashboard
- Panels should show:
  - Total Prompts: 2
  - Average Confidence: 0.71 (yellow threshold)
  - Prompts by Confidence: pie chart
  - Feedback Success Rate: (from logs)

### 4. Test Alert Trigger
```bash
# Manually lower confidence to test alert
# (This is for testing; revert after)
curl -X POST http://localhost:8762/mcp/prompts/feedback \
  -H "Content-Type: application/json" \
  -d '{
    "prompt_id": "existing-prompt-id",
    "success": false,
    "confidence_update": -0.25
  }'

# Check if alert fires in Grafana
# Expected: avg_confidence drops below 0.70, alert triggers
```

---

## Deployment Checklist

### Prerequisites
- [ ] Grafana running (https://grafana.d3its.us or local)
- [ ] AWX running (https://awx.d3its.us or local)
- [ ] Loki running (optional; logs aggregation)
- [ ] Prometheus scraping `localhost:8762/mcp/metrics`

### Grafana
- [ ] 3 dashboards imported
- [ ] Prometheus datasource configured
- [ ] Dashboards showing live metrics
- [ ] Time range set to last 24h

### AWX
- [ ] 3 job templates created
- [ ] Each template tested manually
- [ ] Schedules configured (optional)
- [ ] Credentials validated

### Alerting
- [ ] Alert rules created (2 confidence thresholds + custom)
- [ ] Slack/email contact points configured
- [ ] Notification routing tested
- [ ] Runbooks published

### Monitoring
- [ ] Log aggregation pipeline running (optional Loki/promtail)
- [ ] Metrics endpoint returning data
- [ ] Dashboard queries reflecting live state
- [ ] On-call handoff aware of new dashboards

---

## Maintenance

**Weekly:**
- Review alert trends in Grafana
- Check for stale or low-confidence prompts
- Verify pipeline ran successfully

**Monthly:**
- Audit alert thresholds (adjust if needed)
- Update runbooks with lessons learned
- Review feedback success rate trends

**Quarterly:**
- Confidence threshold review (lower from 0.8 if needed)
- Alert fatigue audit (too many false positives?)
- Capacity planning (are we generating enough high-confidence prompts?)

---

## Related Docs

- `MONITORING_PLAN.md` — Full Phase 1-5 architecture (baseline)
- `CONTRIB.md` — Registry API and phases completed
- `/tmp/check_*.yml` — Playbook sources
- `/tmp/*-dashboard.json` — Dashboard definitions

---

**Last Updated:** 2026-07-26  
**Status:** Phase 5 deployment guide live — ready for infrastructure deployment
