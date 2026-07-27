# Alert: Governor Feedback Success Rate Degradation

## Meaning
The feedback success rate has fallen below 80%. This indicates that agents are reporting more failures than expected when using prompts recommended by Governor routing.

## Immediate Diagnostics

1. **Check Governor Health**
   ```bash
   curl -s 'http://localhost:8762/mcp/prompts/governor/route?task_type=test&limit=3' | jq '.recommendations'
   ```

2. **Verify Feedback Recording**
   ```bash
   curl -s http://localhost:8762/mcp/metrics | grep prompts_feedback_success_rate
   ```

3. **Check Feedback Counts**
   ```bash
   curl -s http://localhost:8762/mcp/metrics | grep "prompts_feedback_.*_total"
   ```

4. **Review Recent Feedback Logs**
   ```bash
   tail -50 /var/log/prompts-mcp/service.log | grep "feedback_submitted" | jq '.success'
   ```

## Root Cause Analysis

- **Router selecting low-quality prompts**: Governor is recommending prompts that don't work
- **Agents not using prompts correctly**: Agents are misusing recommended prompts
- **Domain-specific degradation**: One domain's prompts are failing while others work
- **Threshold miscalibration**: Success rate calculation is incorrect
- **Feedback loop broken**: Successful prompts aren't getting positive feedback

## Recovery Steps

1. **Identify failing domains**
   ```bash
   curl -s 'http://localhost:8762/mcp/prompts/registry' | jq '.prompts[] | {domain, success_rate}' | sort -k 2
   ```

2. **Check which prompts are failing**
   ```bash
   # Review recent feedback for failures
   tail -100 /var/log/prompts-mcp/service.log | grep 'success: false' | jq '.prompt_id'
   ```

3. **Review the failing prompts**
   - Check if prompts are outdated
   - Verify trigger matches agent task type
   - Review prompt content for accuracy

4. **Improve feedback submission**
   - Ensure agents report results accurately
   - Verify confidence_update values are reasonable
   - Check that feedback reaches the system

5. **Trigger confidence recalculation**
   ```bash
   curl -X POST http://localhost:8762/mcp/prompts/registry/rebuild
   ```

## Monitoring

- Watch feedback success rate recover above 80%
- Verify improved domain scores in next rebuild
- Check that Governor routing improves routing quality

## Escalation

- If success rate remains below 80% for 1 hour: contact team
- If all domains affected equally: check feedback collection system
- If single domain affected: review domain-specific prompts
