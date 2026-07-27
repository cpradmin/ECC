# AWX Playbooks for Prompts-MCP Monitoring

This directory contains Ansible playbooks for automated health checks and monitoring via AWX/Ansible Tower.

## Files

| File | Purpose | Frequency |
|------|---------|-----------|
| `check_registry_health.yml` | Check registry metrics, confidence levels, promotion status | Hourly |
| `check_governor_health.yml` | Verify routing intelligence, domain confidence distribution | Hourly |
| `check_pipeline_health.yml` | Monitor daily pipeline runs, check for errors | Hourly |
| `AWX_PLAYBOOK_FIXES.md` | Documentation of I1–I4 fixes applied | Reference |

## Quick Start

### Prerequisites
- AWX/Ansible Tower running
- `prompts-mcp` service accessible on `localhost:8762`
- Ansible 2.9+
- Log directory: `/var/log/prompts-mcp/`

### Manual Testing

```bash
# Test registry health
ansible-playbook check_registry_health.yml

# Test governor routing
ansible-playbook check_governor_health.yml

# Test pipeline health
ansible-playbook check_pipeline_health.yml
```

## Design Decisions

### I1: Availability vs. Data Quality
Each playbook asserts HTTP 200 explicitly:
- Service down → clear network error (availability)
- Service up but data issue → confidence/routing alert (quality)

This prevents on-call from chasing phantom data quality issues during actual outages.

### I2: No gather_facts
Playbooks run with `gather_facts: no` for performance. Critical facts (time, pipeline logs) are gathered explicitly as needed.

### I3: JSON-Based Error Detection
Pipeline errors detected via JSON log level parsing, not substring grep:
- Looks for: `"level":"ERROR"` (exact JSON field)
- Ignores: "failed" in prompt content, "0 failed" counts, etc.

### I4: Fail-Safe Pipeline Check
If pipeline logs are missing entirely → playbook fails immediately with "Daily automation may not be running" message.

This prevents the scenario where a hung pipeline appears healthy because logs were never generated.

## Integration with AWX

### Job Templates

Create 3 job templates in AWX:

**Template 1: Registry Health Check**
- Playbook: `check_registry_health.yml`
- Inventory: localhost
- Credentials: none (runs locally)
- Schedule: Every hour
- On Failure: Notify → Slack/email

**Template 2: Governor Health Check**
- Playbook: `check_governor_health.yml`
- Inventory: localhost
- Schedule: Every hour
- On Failure: Notify → Slack/email

**Template 3: Pipeline Health Check**
- Playbook: `check_pipeline_health.yml`
- Inventory: localhost
- Schedule: Every hour
- On Failure: Notify → Slack/email

### Alerting Rules

| Condition | Alert | Channel |
|-----------|-------|---------|
| Registry check fails (service down) | Network Error | Slack #ops |
| Confidence < 0.70 | WARNING | Slack #prompts-alerts |
| Confidence < 0.50 | CRITICAL | PagerDuty on-call |
| Governor: no high-confidence domains | CRITICAL | PagerDuty on-call |
| Pipeline: no recent logs | ERROR | Slack #ops |
| Pipeline: ERROR in logs | CRITICAL | PagerDuty on-call |

## Troubleshooting

### Registry Check Fails

**"Unable to connect to http://localhost:8762"**
- Verify `prompts-mcp` service is running: `systemctl status prompts-mcp`
- Check port: `netstat -tlnp | grep 8762`
- Check firewall: `firewall-cmd --list-all`

**"Confidence too low" (unexpected)**
- Check actual metrics: `curl http://localhost:8762/mcp/metrics | grep all_avg_confidence`
- Review Loki logs: `curl 'http://loki:3100/loki/api/v1/query?query={service="prompts-mcp"}' | jq`

### Pipeline Check Fails

**"No pipeline logs found"**
- Check if pipeline has run: `ls -la /var/log/prompts-mcp/daily-*.log`
- Check systemd timer: `systemctl status prompts-mcp-daily.timer`
- Check pipeline logs: `tail -50 /var/log/prompts-mcp/service.log | grep pipeline`

**"Pipeline had ERROR entries"**
- Review log: `tail -100 /var/log/prompts-mcp/service.log | jq '.level'`
- Find first ERROR: `grep '"level":"ERROR"' /var/log/prompts-mcp/service.log | head -1`

## Related Documentation

- [Phases 1-5 Monitoring Plan](../../PHASE5_DEPLOYMENT.md)
- [Critical Review Findings](../../CRITICAL_REVIEW_FINDINGS.md)
- [AWX Playbook Fixes](./AWX_PLAYBOOK_FIXES.md)

## Version History

| Date | Change | Reason |
|------|--------|--------|
| 2026-07-26 | I1–I4 fixes applied | Fixed outage detection, undefined facts, false positives, fail-open behavior |
| 2026-07-25 | Initial deployment | Created 3 health check playbooks |
