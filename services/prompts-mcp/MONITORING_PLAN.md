# Prompts-MCP Monitoring & NOC Dashboard Plan

**Goal:** Full visibility into Registry, Governor routing, confidence evolution, and daily pipeline health. Integrate with AWX NOC dashboard.

---

## Phase 1: Structured Logging (Week 1)

### 1a. Application Logging

**Prompts-MCP service logs:**
```go
// Add structured logging to handlers
import "log/slog"

// In each endpoint, log:
slog.Info("registry_query", 
  slog.String("domain", domain),
  slog.Float64("min_confidence", float64(minConfidence)),
  slog.Int("result_count", len(filtered)))

slog.Info("governor_route_query",
  slog.String("task_type", taskType),
  slog.Int("recommendations", len(recommendations)),
  slog.String("routing_session", routingSession))

slog.Info("feedback_recorded",
  slog.String("prompt_id", promptID),
  slog.Bool("success", success),
  slog.Float64("new_confidence", float64(newConfidence)))
```

**Daily pipeline logging:**
```bash
# /usr/local/bin/prompts-mcp-daily.sh
# Each step logs to structured JSON
echo "$(date -u +'%Y-%m-%dT%H:%M:%SZ') INFO step=1 action=extract_patterns count=67" >> "$LOG"
echo "$(date -u +'%Y-%m-%dT%H:%M:%SZ') INFO step=2 action=generate_prompts model=selah status=success" >> "$LOG"
```

**Locations:**
- Service logs: `/var/log/prompts-mcp/service.log` (JSON format)
- Pipeline logs: `/var/log/prompts-mcp/daily-*.log` (existing)
- Feedback: `/var/log/prompts-mcp/feedback.log` (JSON: timestamp, prompt_id, success, confidence_delta)
- Governor: `/var/log/prompts-mcp/governor.log` (JSON: query, session, recommendations)

### 1b. Metrics Export

**Create metrics endpoint:**
```
GET /mcp/prompts/metrics (Prometheus format)

# HELP prompts_registry_total Total prompts in registry
# TYPE prompts_registry_total gauge
prompts_registry_total 2

# HELP prompts_avg_confidence Average confidence score
# TYPE prompts_avg_confidence gauge
prompts_avg_confidence 0.71

# HELP prompts_promoted_count Total promotions (≥0.8 confidence)
# TYPE prompts_promoted_count counter
prompts_promoted_count 0

# HELP prompts_feedback_success_total Successful feedback records
# TYPE prompts_feedback_success_total counter
prompts_feedback_success_total 2

# HELP prompts_feedback_failure_total Failed feedback records
# TYPE prompts_feedback_failure_total counter
prompts_feedback_failure_total 0

# HELP prompts_governor_queries_total Governor route queries
# TYPE prompts_governor_queries_total counter
prompts_governor_queries_total 5

# HELP prompts_token_savings_percent Token savings from smart-context usage
# TYPE prompts_token_savings_percent gauge
prompts_token_savings_percent 89

# HELP prompts_daily_pipeline_duration_seconds Last pipeline execution time
# TYPE prompts_daily_pipeline_duration_seconds gauge
prompts_daily_pipeline_duration_seconds 45.3
```

**Add metrics handler** to prompts-mcp:
- `/mcp/prompts/metrics` — Prometheus-compatible format
- Scraped by Grafana every 60s
- Aggregates: registry stats, feedback counts, governor queries, pipeline health

---

## Phase 2: Log Aggregation (Week 1-2)

### 2a. Loki Integration

**Push logs from prompts-mcp to Loki:**

```bash
# Install promtail on nobara-pc (or forward logs via syslog)
curl -fL https://github.com/grafana/loki/releases/download/v2.9.0/promtail-linux-amd64.zip -o promtail.zip
unzip promtail.zip
sudo mv promtail /usr/local/bin/

# Configure promtail (/etc/promtail/config.yaml)
scrape_configs:
  - job_name: prompts-mcp-service
    static_configs:
      - targets:
          - localhost
        labels:
          job: prompts-mcp
          service: prompts-mcp-api
    file_sd_configs:
      - files:
          - /var/log/prompts-mcp/service.log
        refresh_interval: 5s

  - job_name: prompts-mcp-pipeline
    static_configs:
      - targets:
          - localhost
        labels:
          job: prompts-mcp-pipeline
          service: daily-training
    file_sd_configs:
      - files:
          - /var/log/prompts-mcp/daily-*.log
        refresh_interval: 5s

  - job_name: prompts-mcp-governor
    static_configs:
      - targets:
          - localhost
        labels:
          job: prompts-mcp-governor
          service: governor-routing
    file_sd_configs:
      - files:
          - /var/log/prompts-mcp/governor.log
        refresh_interval: 5s

clients:
  - url: http://loki:3100/loki/api/v1/push
```

**Systemd service for promtail:**
```ini
# /etc/systemd/system/promtail.service
[Unit]
Description=Promtail log aggregator
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/promtail -config.file=/etc/promtail/config.yaml
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

---

## Phase 3: Grafana Dashboard (Week 2)

### 3a. Dashboard: Registry Health

**Name:** "Prompts Registry Overview"
**Refresh:** 60s

**Panels:**

1. **Registry Stats (Stat)**
   - Total prompts
   - Avg confidence (gauge, color: red <0.7, yellow 0.7-0.8, green ≥0.8)
   - Domains
   - Promoted count (≥0.8)

2. **Confidence Distribution (Histogram)**
   - X: Confidence buckets (0.3, 0.5, 0.7, 0.8, 0.9, 1.0)
   - Y: Prompt count
   - Query: Loki logs, extract `new_confidence` from feedback

3. **Feedback Success Rate (Gauge)**
   - Success %: `(success_count / total_count) * 100`
   - Query: Count success=true vs total from governor.log

4. **Confidence Evolution Trend (Time Series)**
   - Y: Avg confidence over time
   - X: Time (hourly aggregation)
   - Query: Prometheus `prompts_avg_confidence` every 60s

5. **Domains Heatmap (Heatmap)**
   - X: Domains (router, go-coding, memory, etc.)
   - Y: Confidence buckets
   - Z: Prompt count per domain-confidence combo
   - Query: Loki, group by domain + confidence

### 3b. Dashboard: Governor Routing

**Name:** "Governor Intelligence"
**Refresh:** 30s

**Panels:**

1. **Routing Requests (Counter)**
   - Total queries per task type
   - Query: Prometheus `prompts_governor_queries_total`

2. **Recommendation Quality (Time Series)**
   - Success rate of recommended prompts
   - Query: Loki, filter `routing_session` in both query and feedback logs

3. **High-Confidence Domains (Table)**
   - Domain | Avg Confidence | Success Rate | Prompt Count
   - Query: Loki, group by domain, calculate stats

4. **Feedback Loop Latency (Time Series)**
   - Time between query → feedback (in seconds)
   - Query: Loki, correlate routing_session timestamps

5. **Agent Coverage (Bar Chart)**
   - Agents tested per domain
   - Query: Loki, extract from `agents_involved` field

### 3c. Dashboard: Daily Pipeline

**Name:** "Training Pipeline Health"
**Refresh:** 5m (runs at 02:00 UTC, next ~24h)

**Panels:**

1. **Pipeline Steps Status (Status)**
   - Step 1: Extract (rows/sec)
   - Step 2: Generate (prompts/min)
   - Step 3: Import (success/fail)
   - Step 4: Rebuild Registry (prompts indexed)
   - Step 5: Export Trinity (facts accumulated)
   - Query: Loki, regex match step logs

2. **Pipeline Duration (Gauge)**
   - Last run time (seconds)
   - Query: Prometheus `prompts_daily_pipeline_duration_seconds`

3. **Pattern Quality (Time Series)**
   - Patterns extracted per run
   - Prompts generated per run
   - Trinity facts accumulated
   - Query: Loki, extract counts from step logs

4. **Error Rate (Gauge)**
   - % of pipeline steps that failed
   - Query: Count ERROR in logs / total steps

5. **Trinity Facts Trend (Time Series)**
   - Cumulative Trinity facts over time
   - Query: Loki, sum `trinity_facts_exported` per run

---

## Phase 4: AWX NOC Integration (Week 2-3)

### 4a. Monitoring Jobs

**AWX Monitoring Template:** "Prompts-MCP Health Check"

**Job 1: Registry Metrics**
```yaml
# Playbook: check_registry_health.yml
---
- hosts: localhost
  gather_facts: no
  tasks:
    - name: Query Registry Metrics
      uri:
        url: "http://localhost:8762/mcp/prompts/metrics"
        method: GET
      register: metrics

    - name: Extract Key Metrics
      set_fact:
        registry_total: "{{ metrics.json['prompts_registry_total'] }}"
        avg_confidence: "{{ metrics.json['prompts_avg_confidence'] }}"
        promoted_count: "{{ metrics.json['prompts_promoted_count'] }}"

    - name: Alert if confidence too low
      fail:
        msg: "Registry avg confidence too low: {{ avg_confidence }}"
      when: avg_confidence | float < 0.70

    - name: Log metrics
      debug:
        msg: "Registry: {{ registry_total }} prompts, avg confidence {{ avg_confidence }}, promoted {{ promoted_count }}"
```

**Job 2: Governor Routing Health**
```yaml
# Playbook: check_governor_health.yml
---
- hosts: localhost
  gather_facts: no
  tasks:
    - name: Query Governor Intelligence
      uri:
        url: "http://localhost:8762/mcp/prompts/governor/intelligence"
        method: GET
      register: intelligence

    - name: Check high-confidence domains
      debug:
        msg: "High confidence domains: {{ intelligence.json['high_confidence_domains'] }}"

    - name: Alert if no high-confidence domains
      fail:
        msg: "No high-confidence domains found!"
      when: intelligence.json['high_confidence_domains'] | length == 0
```

**Job 3: Daily Pipeline Success**
```yaml
# Playbook: check_pipeline_health.yml
---
- hosts: localhost
  gather_facts: no
  tasks:
    - name: Check latest pipeline log
      command: tail -20 /var/log/prompts-mcp/daily-*.log
      register: pipeline_log

    - name: Check for errors
      fail:
        msg: "Pipeline had errors: {{ pipeline_log.stdout }}"
      when: "'ERROR' in pipeline_log.stdout or 'failed' in pipeline_log.stdout"

    - name: Extract duration
      shell: "grep 'Complete' /var/log/prompts-mcp/daily-*.log | tail -1"
      register: completion

    - name: Alert if took too long
      fail:
        msg: "Pipeline took >5 min: {{ completion.stdout }}"
      when: completion.stdout.find("duration_seconds") > 0 and completion.stdout.split('=')[1] | int > 300
```

### 4b. Alert Rules (AWX Job Templates)

**Template 1: Low Confidence Alert**
- Trigger: avg_confidence < 0.70
- Action: Slack notification to #prompts-mcp-alerts
- Action: Create incident in Grafana

**Template 2: No High-Confidence Domains**
- Trigger: high_confidence_domains empty
- Action: Notify ops team (page on-call)
- Action: Suggest manual review of prompts

**Template 3: Pipeline Failure**
- Trigger: Daily pipeline ERROR or timeout
- Action: Run remediation playbook
- Action: Create incident

**Template 4: Governor Routing Degradation**
- Trigger: feedback_success_rate < 0.80
- Action: Notify team
- Action: Run diagnostics

### 4c. NOC Dashboard Integration

**AWX Dashboard Widget: Prompts-MCP Status**

```
┌─ Prompts-MCP Health ─────────────────┐
│ Status: HEALTHY                      │
├──────────────────────────────────────┤
│ Registry:                            │
│   ├─ Prompts: 2                      │
│   ├─ Avg Confidence: 0.71 ⚠️         │
│   └─ High-Confidence: 0 🔴           │
│                                      │
│ Governor:                            │
│   ├─ Routing Requests: 5             │
│   ├─ Success Rate: 100%              │
│   └─ Avg Latency: 45ms               │
│                                      │
│ Pipeline (Last Run):                 │
│   ├─ Duration: 45s                   │
│   ├─ Status: SUCCESS                 │
│   └─ Prompts Generated: 3            │
│                                      │
│ ⚠️  Alert: Avg confidence approaching 0.70 threshold
│    (Goal: ≥0.80 for routing decisions)
│                                      │
│ [View Dashboard] [View Logs] [Silence]
└──────────────────────────────────────┘
```

---

## Phase 5: Thresholds & Alerting (Week 3)

### Alert Matrix

| Metric | Threshold | Severity | Action |
|--------|-----------|----------|--------|
| Avg Confidence | < 0.70 | WARNING | Notify team |
| Avg Confidence | < 0.50 | CRITICAL | Page on-call |
| High-Confidence Domains | 0 | CRITICAL | Escalate |
| Pipeline Success Rate | < 90% | WARNING | Review logs |
| Pipeline Duration | > 5 min | WARNING | Investigate |
| Governor Success Rate | < 80% | WARNING | Check feedback |
| Trinity Facts/Day | 0 | ERROR | Pipeline failed |

### Recovery Procedures

**Low Confidence Recovery:**
1. Check feedback logs for failures
2. Identify failing prompts
3. Run diagnostics: are agents testing?
4. Manual confidence adjustment if needed

**No High-Confidence Domains:**
1. Wait for daily pipeline to run (→ generates new prompts)
2. Manually test prompts to boost confidence
3. Consider lowering threshold temporarily

**Pipeline Failure:**
1. Check AWX logs
2. Verify Selah is reachable (10.174.210.10:11434)
3. Check memory extraction (patterns file)
4. Re-run pipeline manually

---

## Implementation Checklist

### Week 1: Logging Foundation
- [ ] Add structured logging to prompts-mcp handlers
- [ ] Create metrics endpoint (`/mcp/prompts/metrics`)
- [ ] Set up log directories and rotation
- [ ] Install promtail on nobara-pc
- [ ] Verify logs flowing to Loki

### Week 2: Dashboards
- [ ] Create "Registry Health" dashboard in Grafana
- [ ] Create "Governor Intelligence" dashboard
- [ ] Create "Pipeline Health" dashboard
- [ ] Test dashboard queries
- [ ] Add to NOC dashboard layout

### Week 3: AWX Integration & Alerting
- [ ] Deploy monitoring playbooks to AWX
- [ ] Create alert job templates
- [ ] Configure Slack/email notifications
- [ ] Test alert scenarios
- [ ] Document runbook for each alert

### Continuous
- [ ] Monitor confidence evolution (should trend up with feedback)
- [ ] Watch feedback success rate (should stay >80%)
- [ ] Verify daily pipeline runs every day at 02:00 UTC
- [ ] Quarterly review of alert thresholds

---

## Endpoints to Add

```go
// metrics.go - new file
func (h *Handler) GetMetrics(w http.ResponseWriter, r *http.Request) {
  // Return Prometheus-format metrics
  // Registry: total, avg_confidence, promoted_count
  // Feedback: success_count, failure_count
  // Governor: query_count, routing_sessions
  // Pipeline: last_duration, last_run_timestamp
}
```

Wire in main.go:
```go
mux.HandleFunc(MCPPath+"/prompts/metrics", h.GetMetrics)
```

---

## Network Topology

```
prompts-mcp (localhost:8762)
  ├─ Logs → Promtail → Loki (3100)
  ├─ Metrics → Prometheus scraper (9090)
  └─ Status checks ← AWX (192.168.2.10:8080)
      ├─ Health check job (daily)
      ├─ Governor intelligence (hourly)
      └─ Pipeline monitoring (02:00 UTC + 5m after)

Grafana (via mcpproxy)
  ├─ Loki datasource (logs)
  ├─ Prometheus datasource (metrics)
  └─ Dashboards: Registry, Governor, Pipeline

NOC Dashboard (AWX)
  ├─ Prompts-MCP status widget
  ├─ Active alerts
  └─ Recent incidents
```

---

## Success Criteria

**Week 1:**
- ✅ All logs flowing to Loki
- ✅ Metrics endpoint returning data
- ✅ Prometheus scraping successfully

**Week 2:**
- ✅ 3 Grafana dashboards live
- ✅ Confidence trend visible
- ✅ Feedback success rate tracked

**Week 3:**
- ✅ AWX monitoring jobs running
- ✅ Alerts configured and tested
- ✅ NOC dashboard integrated
- ✅ Team can troubleshoot via dashboards

---

## Future Enhancements

- Distributed tracing (OpenTelemetry) for request flows
- Custom alerts based on Trinity pattern anomalies
- Automated remediation (adjust thresholds, re-run pipeline)
- Cost analysis (token savings impact on API spend)
- Per-domain confidence tracking (specialize dashboards)
