# The 3-6-9 Architectural Pattern: A Repeatable Framework

## Core Principle

**All well-designed systems follow natural balance: split modules until each has 1-2 responsibilities, and you'll naturally arrive at groups of 3, 6, 9, or 12.**

This is not Go-specific. It's not even code-specific. It's **universal**.

---

## Why 3-6-9?

### The Mathematics of Balance

```
Why NOT even numbers?
- 2 modules: Binary, simplistic (A/B decision → false dichotomy)
- 4 modules: Clumsy (not divisible by 3; creates asymmetry)
- 8 modules: Too many choices without natural hierarchy

Why 3?
- Thesis + antithesis → synthesis (Hegelian dialectic)
- Three points define a plane (stable)
- Three-body problem: minimum for emergence

Why 6?
- 3 × 2 (pairs of complementary triads)
- Natural hierarchy: 3 pairs within 1 group
- Goldilocks: not too few to be primitive, not too many to be chaotic

Why 9?
- 3 × 3 (fractal structure)
- Tree-like nesting (modules → subsystems → systems)
- Matches human cognitive limit (±3 choices at any level)
```

### Cognitive Load Scaling

```
Scenario: Adding features to a codebase

With 2 modules (even):
- Engineer must understand both A and B
- Adding C forces merger or splitting (no natural boundary)
- Context thrashing

With 6 modules (3-6-9):
- Engineer understands 2-3 modules deeply (their slice)
- Knows 1-2 related modules (neighbors)
- Knows system exists but doesn't need details (distant)
- Cognitive load: manageable, hierarchical

With 12 modules:
- If split 3 groups of 4: clumsy
- If split 2 groups of 6: natural
- Fractal structure maintains clarity
```

---

## The Pattern Recognition Test

To know if a system is well-balanced, ask:

### 1. **River Flow Test** ✅ Smooth?
```
Good flow (no turbulence):
  config → validate → threshold → handler → notify → state
  Each step adds value, no backflows, clear direction

Turbulent (white caps):
  handler ↔ notify (circular dependency)
  notify → state → handler (feedback loop)
  handler imports from 7 modules (high coupling)

Action: If turbulent, split more until smooth.
```

### 2. **Responsibility Test** ✅ One job each?
```
Good:
- alerts_validate.go: validation only
- alerts_notify.go: notification dispatch only
- alerts_message.go: formatting only

Bad:
- alerts_core.go: config + logic + validation + dispatch (4 jobs)
```

### 3. **Ownership Test** ✅ Can 1 engineer own a group?
```
Good (6 modules):
- Engineer A understands all 6 alerts modules deeply
- Can modify any one without consulting others
- Onboarding: 2 days

Bad (2 monolithic modules):
- Engineer A understands alerts_huge but not alerts_tiny_helper
- Changes require cross-team coordination
- Onboarding: 1 week
```

### 4. **Change Propagation Test** ✅ Surgical edits?
```
Good:
- Add PagerDuty backend: touch 2 modules (alerts_notify, alerts_handler)
- Optimize YAML parsing: touch 1 module (loader_parse)
- Add versioning: touch 3 modules (registry_index, registry_load, registry_save)

Bad:
- Add PagerDuty: modify 5+ modules (ripple effect)
- Optimize YAML: affects loader and registry (unrelated systems)
- Add versioning: requires refactoring 8+ modules
```

---

## How to Apply This Pattern

### Step 1: Measure Current State

```bash
# For each module:
lines_of_code=$(wc -l < module.go)
cyclomatic_complexity=$(gocyclo module.go | awk '{sum+=$1} END {print sum}')
cognitive_load=$((lines_of_code * cyclomatic_complexity))

echo "Module: $cognitive_load CLU"
```

**Targets:**
- Ideal: <10K CLU per module
- Acceptable: <15K CLU per module
- Critical: >20K CLU (split immediately)

### Step 2: Identify Monoliths

Modules >20K CLU are candidates for splitting:

```go
// alerts.go: 982 LOC × 85 complexity = 83,470 CLU ❌ SPLIT THIS

// Responsibilities in alerts.go:
// 1. AlertMatrix definition
// 2. Threshold evaluation
// 3. Deduplication state machine
// 4. Slack notification dispatch
// 5. Message formatting
// 6. Alert enrichment
// 7. Recovery logic
// 8. Dashboard integration
// = 8 separate concerns → split into 6 modules
```

### Step 3: Design Natural Boundaries

For each concern, ask: **"Does this module have a single reason to change?"**

```go
// Should alerts_config.go exist?
// YES — if requirements for AlertMatrix change independently of notification logic

// Should alerts_notify.go exist?
// YES — if Slack integration changes without affecting threshold logic

// Should alerts_core.go include both?
// NO — they change for different reasons
```

### Step 4: Calculate New Structure

**Rule: Aim for ~10K CLU per module**

```
alerts.go: 83,470 CLU ÷ 10,000 = 8.3 → split into 6-8 modules
loader.go: 35,460 CLU ÷ 10,000 = 3.5 → split into 3-4 modules
registry.go: 17,220 CLU ÷ 10,000 = 1.7 → split into 1-2 modules (or keep as-is)
```

### Step 5: Validate Flow

```
Create a dependency graph:
- alerts_config → alerts_matrix
- alerts_matrix → alerts_validate
- alerts_validate → alerts_threshold
- alerts_threshold → alerts_handler
- alerts_handler → alerts_notify
- alerts_handler → alerts_state
- alerts_notify → alerts_message

Flow: No cycles? No backflows? Smooth? ✅ GOOD
```

### Step 6: Test Ownership

```
Can 1 engineer own this group?
- Yes → good design
- No → too many modules or too tightly coupled
```

---

## Pattern Application by Domain

### Go Package Organization
```
✅ DO:
handlers/
  └─ alerts/
      ├─ config.go (config + validation)
      ├─ matrix.go (definitions)
      ├─ threshold.go (evaluation logic)
      ├─ handler.go (dispatch)
      ├─ notify.go (backends)
      ├─ message.go (formatting)
      └─ state.go (dedup)

❌ DON'T:
handlers/
  └─ alerts.go (800+ LOC monolith)
```

### Microservices
```
✅ DO:
alerts-service/
  ├─ config-service (load AlertMatrix)
  ├─ threshold-service (evaluate)
  ├─ dispatch-service (route to backends)
  └─ notify-service (Slack/PagerDuty/email)

❌ DON'T:
alerts-service (does everything)
```

### Database Schema
```
✅ DO (6 tables for alerts):
alerts_config
alerts_rules
alerts_thresholds
alerts_state (dedup tracking)
alerts_log (dispatch history)
alerts_enrichment (context)

❌ DON'T:
alerts (one mega-table with 30 columns)
```

### REST API
```
✅ DO (6 endpoints per resource):
GET    /alerts/config           (read config)
POST   /alerts/validate         (validate new rule)
GET    /alerts/thresholds/{id}  (read threshold)
POST   /alerts/check            (evaluate)
POST   /alerts/notify           (trigger)
GET    /alerts/state/{id}       (dedup state)

❌ DON'T:
GET    /alerts (returns everything)
POST   /alerts (accepts mixed payloads)
```

---

## Common Mistakes

### ❌ Mistake 1: "We'll split if it ever gets big"
**Problem:** By the time it's big, it's hard to split (dependencies everywhere)
**Solution:** Preemptively split at >15K CLU

### ❌ Mistake 2: "6 files is too many; let's use 2"
**Problem:** 2 files = binary choice; no room for nuance
**Solution:** 3, 6, 9, 12 are natural rhythms

### ❌ Mistake 3: "Each function should be its own module"
**Problem:** Over-fragmentation; coupling explodes
**Solution:** Target 1-2 responsibilities per module, not per function

### ❌ Mistake 4: "Let's split by layer instead of concern"
**Problem:** Leads to layers doing multiple things
**Example:** `validation.go` (validates alerts, configs, thresholds, messages) = bad
**Solution:** Split by concern: `alerts_validate.go`, `config_validate.go`, etc.

### ❌ Mistake 5: "We'll refactor after launch"
**Problem:** Refactoring gets deprioritized; technical debt compounds
**Solution:** Plan refactoring budget (20% of post-launch time)

---

## Success Metrics

**Before Refactoring:**
```
Max cognitive load: 83,470 CLU (alerts.go)
Team confusion: High (who owns alerts retry logic?)
Maintenance: High (changes cascade)
Test isolation: Poor (must mock entire alerts system)
```

**After Refactoring (3-6-9 pattern):**
```
Max cognitive load: 12,520 CLU (any single module)
Team clarity: Clear ownership (1 engineer per group)
Maintenance: Low (surgical 1-3 module changes)
Test isolation: Excellent (test each module independently)
```

---

## Template: Applying 3-6-9 to Your Codebase

```markdown
## Project: [Your System]

### Current Monoliths
- [module.go]: X LOC × Y complexity = Z CLU ❌

### Proposed Split (to 6 modules)
1. [module_a.go] — responsibility 1
2. [module_b.go] — responsibility 2
3. [module_c.go] — responsibility 3
4. [module_d.go] — responsibility 4
5. [module_e.go] — responsibility 5
6. [module_f.go] — responsibility 6

### Flow Validation
module_a → module_b → module_c → module_d → module_e → module_f
✅ Linear, no cycles, smooth

### Cognitive Load Targets
- [module_a]: 130 LOC × 3 complexity = 390 CLU ✅
- [module_b]: 150 LOC × 5 complexity = 750 CLU ✅
- [module_c]: 160 LOC × 8 complexity = 1,280 CLU ✅
- ... (all <15K CLU)

### Ownership
- Engineer 1: owns modules A-C
- Engineer 2: owns modules D-F
- Onboarding: 2 days per engineer
```

---

## Conclusion

**The 3-6-9 pattern is not arbitrary.** It emerges naturally when you:

1. ✅ Split until each module has 1-2 responsibilities
2. ✅ Test for smooth flow (no turbulence, no cycles)
3. ✅ Measure cognitive load (<10K CLU ideal)
4. ✅ Validate team ownership (1 engineer per group)

**Apply this to any codebase:** microservices, monoliths, Python/Go/Rust, databases, APIs.

**Result:** Systems that flow like rivers, not turbulent seas.
