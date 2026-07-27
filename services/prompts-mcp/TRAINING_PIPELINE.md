# Prompts-MCP Training Pipeline

**Continuous Prompt Learning from Memory + Selah Generation**

## Architecture

```
Memory System (286 files)
    ↓ [Extraction]
Patterns (67 discovered)
    ↓ [Categorization]
By Domain & Type (success/failure/gotcha/pending)
    ↓ [Selah Generation]
Generated Prompts (YAML)
    ↓ [Import]
prompts-mcp (project scope, confidence 0.65-0.75)
    ↓ [Real Agent Testing]
Feedback Loop (success/failure observations)
    ↓ [Confidence Evolution]
Promotion Eligible (0.8+ confidence, 3+ agents)
    ↓ [Global Scope]
Available to Entire Swarm
```

## Components

### 1. Memory Extractor (`tools/memory_extractor.go`)

Reads 286 memory files in `~/.claude/projects/-home-kntrnjb/memory/`.

**What it finds:**
- `## Accomplished` sections → success patterns
- `## Discoveries` sections → learning patterns
- `## Next Steps` sections → pending patterns
- `⚠️ Gotchas` lines → failure/landmine patterns

**Output:** 67 patterns categorized by:
- Domain: infrastructure (30), general (14), ops (8), identity (7), prompts (5), knowledge (3)
- Type: gotcha (49), pending (18)

**Run:**
```bash
cd ~/Projects/prompts-mcp
go build ./cmd/memory-trainer
./memory-trainer --action extract
```

### 2. Pattern Loader (`cmd/memory-trainer/main.go`)

CLI tool for pattern extraction, database loading, and reporting.

**Modes:**
```bash
# Just report without writing to DB
./memory-trainer --action extract

# Load patterns into Postgres
./memory-trainer --action load --db-pass $POSTGRES_PASSWORD

# Query database and show stats
./memory-trainer --action report
```

**Postgres Target:** Savera container at 10.174.210.22:5432 (with schema from `prompts_training_schema.sql`)

### 3. Selah Prompt Generator (`scripts/selah_local_generate.py`)

Calls Selah (nova-qwen14b) on pve (10.174.210.10:11434) to generate new prompts from extracted patterns.

**Process:**
1. Extracts patterns from memory files
2. Groups by domain (router, go-coding, memory, etc.)
3. Feeds patterns to Selah: "Generate a production prompt for [domain] based on these observations"
4. Collects YAML-formatted output
5. Saves to `~/Projects/prompts-mcp/generated-prompts/`

**Run:**
```bash
python3 scripts/selah_local_generate.py [domain]
# or all domains (default)
python3 scripts/selah_local_generate.py
```

**Output Format (YAML):**
```yaml
id: router-classifier-selah-001
domain: router-prompts
trigger: "Classify user intent and route to appropriate domain"
confidence: 0.72
content: |
  [System prompt for the domain]
  [Multi-line, production-ready instructions]
source: "extracted from Ember patterns"
reasoning: "Why these patterns led to this prompt"
```

### 4. Import Handler (`handlers/handlers.go`)

**Endpoint:** `POST /mcp/prompts/import`

**Request Body:**
```json
{
  "prompts": [
    {
      "id": "router-classifier-selah-001",
      "domain": "router-prompts",
      "trigger": "...",
      "confidence": 0.72,
      "scope": "project",
      "content": "...",
      "agents_tested": [],
      "success_rate": 0.0
    }
  ],
  "source": "selah-generated-2026-07-25",
  "feedback": [
    {
      "prompt_id": "router-classifier-selah-001",
      "agent": "nova",
      "task": "test-intent-routing",
      "success": true,
      "note": "..."
    }
  ]
}
```

**Response:** `202 Accepted`
```json
{
  "status": "ok",
  "prompts_received": 3,
  "feedback_received": 2,
  "source": "selah-generated-2026-07-25"
}
```

### 5. Feedback Loop (`handlers/feedback.go`)

Agents submit observations of prompt effectiveness.

**Confidence Evolution:**
- Each success: +0.05
- Each failure: -0.10
- Bounds: [0.3, 0.95]

**Promotion Eligibility:**
- Confidence ≥ 0.8
- 3+ agents tested
- Scope == "project" (eligible for promotion to global)

**Example Flow:**
```
Initial: 0.70 (router-classifier-selah-001, no agents tested)
  ↓ nova success: 0.75
  ↓ eve success: 0.80 ← PROMOTION ELIGIBLE (0.80 >= 0.8)
  ↓ claw success: 0.85 ← PROMOTION ELIGIBLE (3 agents)
  ↓ selah failure: 0.75
  ↓ → Still high confidence but now multi-agent validated
```

## Full Training Cycle

### Day 1: Extract & Generate
```bash
# Extract patterns from memory
cd ~/Projects/prompts-mcp
./memory-trainer --action extract
# Output: 67 patterns found

# Generate prompts with Selah
python3 scripts/selah_local_generate.py
# Output: 3-6 YAML files in generated-prompts/

# Review generated prompts
ls -lh generated-prompts/*.yaml
cat generated-prompts/router-prompts.yaml
```

### Day 2: Import & Test
```bash
# Start prompts-mcp MCP server (in another terminal)
./bin/prompts-mcp
# Listens on MCP protocol (stdio-based, or :8762 for HTTP bridge)

# Test with one agent
curl -X POST -H "Content-Type: application/json" \
  -d @/tmp/import_selah_prompts.json \
  http://localhost:8762/mcp/prompts/import

# Agents start using prompts from /mcp/prompts/list
```

### Day 3-7: Observation & Feedback
Agents test prompts. For each observation:
```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{
    "prompt_id": "router-classifier-selah-001",
    "agent": "nova",
    "task": "intent-classification",
    "success": true,
    "note": "Correctly classified multi-intent user message"
  }' \
  http://localhost:8762/mcp/prompts/feedback
```

### Week 2: Promotion & Scaling
Check confidence evolution:
```bash
# Query promoted prompts
curl http://localhost:8762/mcp/prompts/list?scope=global
# Only prompts with 0.8+ confidence + 3+ agents appear

# Export for team sharing
curl http://localhost:8762/mcp/prompts/export?format=yaml \
  > shared-prompts-week2.yaml

# Share via GitHub Releases / registry
```

## Integration with Ember Swarm

### Router Integration
Prompts-MCP becomes upstream data source for the Swarm Router.

```go
// In router/router.go
func (r *Router) LoadPrompts() {
  // Query prompts-mcp
  prompts := r.mcp.GetPrompts(domain: "router-prompts", scope: "global")
  
  // Build intent classifier from high-confidence prompts
  for _, p := range prompts {
    if p.Confidence >= 0.8 {
      r.intentClassifier = p.Content
    }
  }
}
```

### Collector Integration
Agents automatically submit feedback to MCP.

```go
// In collector/collector.go
func (c *Collector) RecordObservation(result TaskResult) {
  if result.Success {
    c.mcp.SubmitFeedback(PromptFeedback{
      PromptID: result.PromptUsed,
      Agent: c.AgentName,
      Success: true,
      Note: result.Details,
    })
  }
}
```

### Trinity Integration (Phase 3)
Feedback loop feeds into Trinity RAG for continuous learning.

```python
# Memory extraction
memory_add({
  "id": f"prompt-promotion-{prompt_id}",
  "tag": ["prompts", "promotion", "multi-agent"],
  "content": f"Prompt {prompt_id} promoted to global with {confidence:.2f} confidence after {n_agents} agents tested it"
})

# For future sessions to reference
```

## Troubleshooting

### "Connection timeout to Savera (10.174.210.22:5432)"
- Check if Savera container is running: `ssh pve-twin "lxc-ls"`
- Verify network connectivity: `ssh pve-twin "ip addr show eth0"`
- For now, use local extraction mode (no Postgres)

### "Selah timeout during generation"
- Ollama generation can take 60-120 seconds
- Check Ollama status: `curl http://10.174.210.10:11434/api/tags`
- Consider using smaller model or shorter context: modify `selah_local_generate.py`

### "Prompts not importing"
- Check prompts-mcp is running: `curl http://localhost:8762/mcp/health`
- Verify JSON is valid: `jq . < import_selah_prompts.json`
- Check feedback.jsonl is writable: `ls -la ~/.local/share/ecc-prompts/projects/ember-swarm/`

## Performance Notes

- **Extraction:** 286 files → 67 patterns in <1s
- **Selah generation:** 1 domain → ~60-120s (LLM generation time)
- **Import:** 3 prompts → feedback logged to JSONL instantly
- **Feedback evolution:** 1000 observations → confidence recalculated in <1ms

## Future Enhancements

1. **Automated daily extraction** (cron job at 2 AM)
2. **Selah batch generation** (all domains in parallel)
3. **Confidence drift alerts** (notify if prompt confidence drops >0.1)
4. **Multi-model comparison** (generate with Eve/Claw too, compare outputs)
5. **Feedback aggregation** (weekly reports: "Top prompts this week")
6. **Version pinning** (teams pin prompts to specific versions)
7. **Prompt attribution** (credit Selah + source patterns)

---

**Status:** Phase 3 ready (training pipeline complete, awaiting Postgres + Selah automation)  
**Owner:** Nova (with Selah as generation engine)  
**Last Updated:** 2026-07-25
