# Alert: Low Confidence Recovery

## Meaning
The average confidence score of prompts has fallen below 0.70 (70%). This indicates that prompts may be performing poorly or feedback has indicated lower reliability.

## Immediate Diagnostics

1. **Check Grafana Dashboard**
   - Navigate to the Registry Health dashboard
   - Check the "Average Confidence" gauge
   - Look at the confidence evolution chart for trends

2. **Review Recent Logs**
   ```bash
   curl -s 'http://loki:3100/loki/api/v1/query?query={msg="feedback_submitted"}' | jq '.data.result | length'
   ```

3. **Verify Service Health**
   ```bash
   curl http://localhost:8762/mcp/health
   ```

4. **Check Feedback Patterns**
   ```bash
   tail -50 /var/log/prompts-mcp/service.log | grep feedback | jq '.success'
   ```

## Root Cause Analysis

- **Low success rate feedback**: Agents are reporting failures on prompts
- **Insufficient positive feedback**: Too few successes to counterbalance failures
- **Stale prompts**: Prompts haven't been tested or updated recently
- **Domain-specific issue**: Particular domains may have more failures than others

## Recovery Steps

1. **Identify affected prompts**
   ```bash
   curl -s 'http://localhost:8762/mcp/prompts/registry?sort=confidence&limit=10' | jq '.prompts[] | {id, domain, confidence}'
   ```

2. **Review low-confidence prompts**
   - Load the prompt from disk: `~/.local/share/ecc-prompts/instincts/personal/[domain]/[id].yaml`
   - Assess if the trigger or content needs refinement

3. **Gather feedback on affected prompts**
   - Test the prompt with multiple agents
   - Submit positive feedback if prompts work correctly
   - Record reasoning for any negative feedback

4. **Trigger rebuild if changes made**
   ```bash
   curl -X POST http://localhost:8762/mcp/prompts/registry/rebuild
   ```

5. **Monitor recovery**
   - Watch Grafana dashboard for confidence improvement
   - Set a follow-up check for 15 minutes

## Escalation

- If confidence remains below 0.70 after 30 minutes: escalate to team lead
- If there's an outage or service error: check CRITICAL alerts instead
- If prompts are corrupted/missing: contact admin

## Prevention

- Establish feedback submission SLA (minimum N feedback per week per prompt)
- Regular confidence audits (weekly review of bottom 10% confidence)
- Test new prompts thoroughly before deployment
- Document trigger and domain-specific best practices
