# Alert: CRITICAL Confidence Failure

## Meaning
🚨 **CRITICAL**: The average confidence score has fallen below 0.50 (50%). This is a severe data quality issue indicating systematic prompt failures or major feedback collection problems.

## Immediate Actions (First 5 Minutes)

1. **Page on-call immediately** — do not delay
2. **Acknowledge the PagerDuty alert**
3. **Open this runbook** (you're reading it — good!)

## Emergency Diagnostics

1. **Verify service is running**
   ```bash
   curl -I http://localhost:8762/mcp/health
   ```
   - If 503 or timeout: restart service
   - If 200: service is up, problem is data quality

2. **Check current average confidence**
   ```bash
   curl -s http://localhost:8762/mcp/metrics | grep prompts_all_avg_confidence
   ```

3. **Get prompt distribution**
   ```bash
   curl -s 'http://localhost:8762/mcp/prompts/registry' | jq '[.prompts[] | .confidence] | {min, max, avg: (add/length)}'
   ```

4. **Count recent feedback**
   ```bash
   tail -100 /var/log/prompts-mcp/service.log | grep "feedback_submitted" | wc -l
   ```

## Root Cause Determination

**Most likely causes (in order):**

1. **Massive feedback collection failure**
   - Agents stopped reporting success
   - Feedback submission broken
   - Governor routing disabled

2. **Prompt corruption or deletion**
   - Prompts file corrupted
   - Domain directories missing
   - YAML parsing errors

3. **Threshold miscalibration**
   - Alert threshold set too low (< 0.3 actual value)
   - Metric calculation broken

4. **Cascading failure**
   - Dependency (Trinity, feedback storage) down
   - Disk space exhausted
   - Permissions issue

## Recovery Procedure

### Step 1: Stop the bleeding

```bash
# Suspend auto-feedback collection (if triggering loop)
# (This would be app-level feature flag, not available yet)

# Check for disk issues
df -h ~/.local/share/ecc-prompts/
```

### Step 2: Assess damage

```bash
# Count valid vs invalid prompts
find ~/.local/share/ecc-prompts/instincts -name "*.yaml" | wc -l

# Check for parsing errors
curl -s http://localhost:8762/mcp/prompts/registry | jq '.prompts | length'

# Load all prompts and report any errors
# (Check stderr for "Error parsing prompt file" messages)
```

### Step 3: Isolate the issue

- **If service is down**: restart and monitor health
- **If confidence metric is wrong**: review metric calculation (handlers/metrics.go)
- **If prompts are corrupted**: restore from backup and rebuild
- **If feedback is broken**: check feedback.jsonl and storage

### Step 4: Remediation

**If prompts are corrupted:**
```bash
# Restore from backup
cp ~/.local/share/ecc-prompts/instincts.bak/* ~/.local/share/ecc-prompts/instincts/

# Rebuild registry
curl -X POST http://localhost:8762/mcp/prompts/registry/rebuild

# Verify
curl -s http://localhost:8762/mcp/metrics | grep prompts_all_avg_confidence
```

**If feedback is causing issues:**
```bash
# Archive current feedback
mv ~/.local/share/ecc-prompts/projects/ember-swarm/feedback.jsonl \
   ~/.local/share/ecc-prompts/projects/ember-swarm/feedback.jsonl.$(date +%s)

# Service will create new feedback.jsonl on next submission
# Rebuild index to recalculate confidence from file metadata
curl -X POST http://localhost:8762/mcp/prompts/registry/rebuild
```

**If metric calculation is wrong:**
- Check `handlers/metrics.go` GetMetrics function
- Verify `prompts_all_avg_confidence` computation uses `LoadAll()` not filtered index
- Restart service after fix

### Step 5: Monitor recovery

```bash
# Watch metric in real-time
watch -n 5 'curl -s http://localhost:8762/mcp/metrics | grep prompts_all_avg_confidence'

# Alert should clear once metric > 0.50
```

## Escalation

If after 15 minutes of recovery steps:
- Confidence still < 0.50: escalate to engineering team
- Service remains down: declare incident and page backup on-call
- Unclear root cause: gather logs and contact architect

## Post-Incident

1. **Review feedback patterns** that caused the failure
2. **Update alert threshold** if it was miscalibrated
3. **Implement feedback guardrails** to prevent cascading failures
4. **Document incident** in team knowledge base
5. **Schedule postmortem** within 24 hours

## Prevention

- **Daily backup** of prompt database
- **Feedback validation** before confidence update
- **Gradual rollout** of prompts (don't deploy 100 new prompts at once)
- **Feedback SLA** (minimum confidence submissions per week)
- **Confidence floor** (clamp lowest confidence at 0.3, not below)
