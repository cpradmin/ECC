---
name: vip-postgres-ember-savera
description: "🚨 VIP CRITICAL: Postgres Database Location + Connection Details + Training Pipeline Schema"
metadata: 
  node_type: memory
  type: reference
  criticality: CRITICAL
  date: 2026-07-25
  updated: 2026-07-25T20:30:00Z
  owner: Adam Bailey (kntrnjb)
  originSessionId: 9000b0fb-4096-4858-a570-67627000430e
  modified: 2026-07-26T01:25:19.914Z
---

# 🚨 VIP: POSTGRES DATABASE — CRITICAL INFRASTRUCTURE

**DO NOT FORGET THIS INFORMATION**

## Location & Access

| Property | Value |
|----------|-------|
| **Hostname** | `ember-savera` |
| **IP Address** | `192.168.2.110` (Local LAN) |
| **Port** | `5432` |
| **Database** | `savera` |
| **SSH User** | `root` |
| **SSH Auth** | **Passwordless key auth** (no password) |
| **Postgres Auth** | **Peer authentication only** (must use sudo -u postgres locally) |

**NetBird IP:** 100.81.102.183:5432 (does NOT accept direct connections due to pg_hba.conf)

## How to Connect

### Method 1: Via SSH (RECOMMENDED)
```bash
# SSH to ember-savera
ssh root@192.168.2.110

# Once logged in, connect to Postgres locally
sudo -u postgres psql -d savera
```

### Method 2: Via SSH + Direct psql
```bash
ssh root@192.168.2.110 "sudo -u postgres psql -d savera -c 'SELECT version();'"
```

### Method 3: Run SQL script via SSH
```bash
ssh root@192.168.2.110 "sudo -u postgres psql -d savera" < /tmp/script.sql
```

**Do NOT try to connect directly from nobara-pc (192.168.2.175)**
- pg_hba.conf does not allow remote connections
- Use SSH tunneling if needed

## Training Pipeline Schema

### Tables Created (in `prompts_training` schema)

```sql
CREATE SCHEMA prompts_training;

-- 1. patterns
--    Extracted patterns from 286 memory files
--    Columns: id, domain, pattern_name, pattern_text, pattern_type, 
--             confidence, success_rate, source_file, source_section, extracted_at

-- 2. examples
--    Training examples from archive/transcripts
--    Columns: id, pattern_id, example_text, outcome, agent, transcript_source, extracted_at

-- 3. feedback_signals
--    Real agent observations for confidence evolution
--    Columns: id, prompt_id, domain, successes, failures, total_observations, confidence, last_updated

-- 4. generated_prompts
--    Selah-generated system prompts (YAML format)
--    Columns: id, domain, pattern_ids[], generated_prompt_content, generation_model, 
--             generation_confidence, generation_reasoning, ready_for_import, imported_to_prompts_mcp, created_at, imported_at

-- 5. memory_index
--    Tracks which memory files have been processed
--    Columns: id, file_path, file_hash, last_extracted, patterns_found
```

### Current Data (as of 2026-07-25 20:30 UTC)

```
Patterns Extracted:     10
Generated Prompts:      3 (all ready_for_import=true)
Feedback Signals:       3
Domains:                5 (infrastructure, prompts, general, ops, knowledge)
Average Confidence:     0.70
```

### Generated Prompts Ready for Import

| ID | Domain | Confidence | Status |
|----|--------|-----------|--------|
| router-classifier-selah-001 | router-prompts | 0.72 | ✅ Ready |
| go-coding-selah-001 | go-coding-prompts | 0.68 | ✅ Ready |
| memory-trinity-selah-001 | memory-prompts | 0.70 | ✅ Ready |

## Critical Files

### Schema Definition
```
~/Projects/prompts-mcp/prompts_training_schema.sql
```
Already applied to ember-savera. If you need to re-apply:
```bash
ssh root@192.168.2.110 "sudo -u postgres psql -d savera" < prompts_training_schema.sql
```

### Generated Prompts (YAML)
```
~/Projects/prompts-mcp/generated-prompts/router-prompts.yaml
~/Projects/prompts-mcp/generated-prompts/go-coding-prompts.yaml
~/Projects/prompts-mcp/generated-prompts/memory-prompts.yaml
```

### Import Template
```
/tmp/import_selah_prompts.json
```

## Training Pipeline Workflow

```
Step 1: Extract Patterns from Memory Files
  go run ./cmd/memory-trainer --action extract
  
Step 2: Load Patterns into Postgres
  ssh root@192.168.2.110 "sudo -u postgres psql -d savera" < load_patterns.sql
  
Step 3: Generate Prompts with Selah (nova-qwen14b on 10.174.210.10:11434)
  python3 scripts/selah_local_generate.py
  
Step 4: Import Generated Prompts into prompts-mcp
  curl -X POST http://localhost:8762/mcp/prompts/import \
    -H "Content-Type: application/json" \
    -d @/tmp/import_selah_prompts.json
  
Step 5: Agents Test Prompts (feedback loop)
  Real agents submit: prompt_id, agent, task, success, note
  
Step 6: Confidence Evolution
  +0.05 per success, -0.10 per failure, bounds [0.3, 0.95]
  
Step 7: Promotion Check
  0.8+ confidence + 3+ agents tested → eligible for global scope
  
Step 8: Continuous Learning
  Feedback → Postgres → Trinity RAG → Next iteration
```

## Useful Queries

### Check patterns loaded
```sql
SELECT domain, COUNT(*) as pattern_count FROM prompts_training.patterns GROUP BY domain;
```

### Check generated prompts ready for import
```sql
SELECT id, domain, generation_confidence, ready_for_import FROM prompts_training.generated_prompts WHERE ready_for_import = true;
```

### Check feedback signals
```sql
SELECT prompt_id, domain, successes, failures, confidence FROM prompts_training.feedback_signals;
```

### Verify schema exists
```sql
SELECT tablename FROM pg_tables WHERE schemaname = 'prompts_training' ORDER BY tablename;
```

## DO NOT FORGET

1. **SSH to 192.168.2.110 as root** (no password, key auth)
2. **Use `sudo -u postgres`** to connect to Postgres locally
3. **Database:** `savera`
4. **Port:** 5432
5. **Schema:** `prompts_training` (already created)
6. **Prompts:** 3 generated, confidence 0.68-0.72, ready for import

## If Connection Fails

### pg_hba.conf issue?
- Connect via SSH to 192.168.2.110
- Check: `sudo -u postgres psql -c "SHOW hba_file;"`
- Config: `/etc/postgresql/*/main/pg_hba.conf`

### Postgres not running?
- Check: `sudo systemctl status postgresql`
- Start: `sudo systemctl start postgresql`

### Need to reload schema?
- Drop old schema: `DROP SCHEMA IF EXISTS prompts_training CASCADE;`
- Re-apply: `cat prompts_training_schema.sql | sudo -u postgres psql -d savera`

## Related Files & Memory

- [[session-checkpoint-prompts-mcp-phase2-complete]] — Phase 2 (export/import handlers)
- [[session-checkpoint-training-pipeline]] — Training pipeline architecture
- `~/Projects/prompts-mcp/TRAINING_PIPELINE.md` — Complete operations guide
- `~/Projects/prompts-mcp/prompts_training_schema.sql` — Schema definition
- `~/Projects/prompts-mcp/generated-prompts/*.yaml` — 3 production prompts

---

**LAST UPDATED:** 2026-07-25 20:30 UTC  
**STATUS:** ✅ Postgres live, schema applied, data loaded, ready for agent feedback  
**CRITICAL:** Do NOT lose this information — it's the heart of the continuous learning pipeline

**If you forget this, Adam will be mad. SAVE IT.**
