# Grafana Dashboards for Prompts-MCP Monitoring

This directory contains Grafana dashboard definitions for visualizing prompts-mcp metrics and monitoring system health.

## Files

| File | Purpose | Metrics |
|------|---------|---------|
| `registry-health-dashboard.json` | Registry metrics, confidence tracking, domain stats | prompts_all_avg_confidence, prompts_registry_total, prompts_promoted_count |
| `governor-dashboard.json` | Governor routing intelligence, feedback success rate | prompts_feedback_success_rate, prompts_feedback_scrape_errors_total |
| `pipeline-dashboard.json` | Daily pipeline health, uptime, confidence trends | prompts_uptime_seconds, prompts_registry_total |

## Fixes Applied (I7–I8)

### I7: Dashboard Queries Reference Non-Existent Metrics ✅

**Problem:**
- Dashboard queried `prompts_avg_confidence` (old name, filtered metric)
- Queried `sum by (domain) (prompts_registry_total)` (metric has no domain labels)
- Referenced non-emitted metrics → panels showed "No data"

**Fix:**
- Updated to `prompts_all_avg_confidence` (C1 fix: averages ALL prompts)
- Removed non-existent `by (domain)` aggregation
- Added all metrics that are actually emitted:
  - `prompts_feedback_scrape_errors_total` (I6 fix)
  - `prompts_registry_avg_confidence` (filtered metric for comparison)

**Result:**
- All dashboard queries now reference real metrics
- Panels display live data (no "No data" errors)

### I8: Dashboards Missing datasource and gridPos ✅

**Problem:**
- No `datasource` field → panels bind to default datasource at import time
- No `gridPos` → all panels stack at origin (overlapping)
- No `uid` or `schemaVersion` → repeat imports create duplicates

**Fix:**
- Added `"datasource": "Prometheus"` to all panels
- Added `"gridPos"` with unique x/y/width/height for each panel
- Added `"uid"` (unique ID) for each dashboard
- Added `"schemaVersion": 38` and `"version": 1` for proper versioning

**Result:**
- Panels layout correctly (no overlap)
- Import updates existing dashboard (no duplicates)
- Consistent datasource across all panels

---

## Importing into Grafana

### Via Grafana UI

1. **Open Grafana** → Dashboards → Import
2. **Upload JSON file** → select one of the `.json` files
3. **Select datasource** → "Prometheus"
4. **Click Import**

### Via API

```bash
# Registry health dashboard
curl -X POST http://grafana:3000/api/dashboards/db \
  -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H "Content-Type: application/json" \
  -d @registry-health-dashboard.json

# Governor intelligence dashboard
curl -X POST http://grafana:3000/api/dashboards/db \
  -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H "Content-Type: application/json" \
  -d @governor-dashboard.json

# Pipeline health dashboard
curl -X POST http://grafana:3000/api/dashboards/db \
  -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H "Content-Type: application/json" \
  -d @pipeline-dashboard.json
```

---

## Dashboard Overview

### Registry Health Dashboard

**Panels:**
1. **Total Prompts** (gauge) — Count of prompts in registry
2. **Average Confidence (All Prompts)** (gauge) — prompts_all_avg_confidence metric
3. **Promoted Prompts Count** (gauge) — Number of promoted prompts
4. **Confidence Evolution** (timeseries) — Trend over time (all vs. filtered)
5. **Registry Domains Count** (gauge) — Number of unique domains

**Use Case:** Monitor overall prompt quality and registry health

### Governor Routing Intelligence Dashboard

**Panels:**
1. **Feedback Success Rate** (gauge) — Ratio of successful to total feedback
2. **Total Feedback Records** (gauge) — Count of all feedback submissions
3. **Feedback Scrape Errors** (gauge) — 0=OK, 1=error loading feedback
4. **Feedback Success vs Failure** (timeseries) — Trend comparison over time

**Use Case:** Monitor Governor routing quality and feedback loop health

### Pipeline Health Dashboard

**Panels:**
1. **Service Uptime** (gauge) — Hours since service started
2. **Total Prompts in Registry** (gauge) — Current registry size
3. **Average Confidence Trend** (timeseries) — Confidence evolution over time

**Use Case:** Monitor daily pipeline execution and system stability

---

## Metric Dependencies

| Dashboard | Depends On | Fails If |
|-----------|-----------|----------|
| Registry Health | prompts_all_avg_confidence, prompts_registry_total, prompts_promoted_count, prompts_registry_avg_confidence | Prometheus scrape fails, metrics endpoint down |
| Governor | prompts_feedback_success_rate, prompts_feedback_success_total, prompts_feedback_failure_total, prompts_feedback_scrape_errors_total | Feedback loading errors, metrics endpoint down |
| Pipeline | prompts_uptime_seconds, prompts_registry_total, prompts_all_avg_confidence, prompts_registry_avg_confidence | Metrics endpoint down |

---

## Troubleshooting

### Panels Show "No data"

1. **Verify Prometheus is running:** `curl http://prometheus:9090/-/healthy`
2. **Check metrics are being scraped:** `curl http://localhost:8762/mcp/metrics`
3. **Verify datasource in Grafana:** Settings → Data sources → Prometheus → Test
4. **Check metric name in query:** Ensure it matches the emitted metric name

### Panels Overlap

- Dashboards have been fixed with proper `gridPos` values
- If overlap occurs after import, this indicates the dashboard was imported with an old version
- Solution: Delete the dashboard and re-import from the latest `.json` file

### Import Creates Duplicate

- Ensure `uid` field is unique (should be `prompts-registry-health`, `prompts-governor-intelligence`, `prompts-pipeline-health`)
- Check existing dashboards: Dashboards → Search for "Prompts"
- If duplicate exists, either delete it or use "Overwrite" during import

---

## Version History

| Date | Change | Reason |
|------|--------|--------|
| 2026-07-26 | I7–I8 fixes applied | Fixed metric names, added datasource, fixed gridPos |
| 2026-07-25 | Initial deployment | Created 3 monitoring dashboards |

---

## Related Documentation

- [Phases 1-5 Monitoring Plan](../../PHASE5_DEPLOYMENT.md)
- [Critical Review Findings](../../CRITICAL_REVIEW_FINDINGS.md)
- [Prometheus Metrics](../../handlers/metrics.go)
