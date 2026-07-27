# Governor Routing Integration — Phase 5

**Objective:** Wire high-confidence prompts into Governor layer for intelligent specialist routing.

## Vision

```
Agent Task
  ↓ Governor queries registry
Registry returns ≥0.8 confidence prompts
  ↓ Ranked by domain + confidence
Governor routes to specialist
  ↓ Specialist executes with prompt
Agent provides feedback (success/failure)
  ↓ Feedback updates prompt confidence
Registry confidence evolves
  ↓ Trinity learns routing patterns
Governor improves routing confidence
  ↓ Next task routes better
```

## Current State

**Registry (Phase 4):** 
- 2 prompts indexed (avg confidence 0.71)
- Discoverable via search
- Versioned and publishable

**Confidence Evolution:**
- Formula: +0.05 success, -0.10 failure
- Bounds: [0.3, 0.95]
- Promotion threshold: ≥0.8 (eligible)

**Trinity:**
- Accumulating feedback facts
- Receiving promotion events
- Ready for pattern learning

## Phase 5 Implementation

### 1. Governor Query Endpoint
**GET `/mcp/prompts/governor/route?task_type=routing&domain=router`**

Request prompts for a specific task, Governor chooses specialist.

**Query params:**
- `task_type` — type of task (routing, coding, analysis, etc.)
- `domain` — preferred domain (optional)
- `min_confidence` — minimum confidence (default: 0.8)
- `limit` — max suggestions (default: 3)

**Response:**
```json
{
  "status": "ok",
  "task_type": "routing",
  "recommendations": [
    {
      "prompt_id": "router-classifier-selah-001",
      "domain": "router-prompts",
      "confidence": 0.72,
      "success_rate": 0.92,
      "reason": "High success rate on routing tasks (0.92), confidence 0.72"
    }
  ],
  "routing_session": "gov-sess-12345"
}
```

### 2. Routing Feedback Endpoint
**POST `/mcp/prompts/governor/feedback`**

Governor reports on prompt performance after using it.

**Body:**
```json
{
  "routing_session": "gov-sess-12345",
  "prompt_id": "router-classifier-selah-001",
  "task_type": "routing",
  "success": true,
  "reasoning": "Correctly routed request to Domain X",
  "agents_involved": ["governor", "specialist-router"]
}
```

**Response:**
```json
{
  "status": "feedback_recorded",
  "prompt_id": "router-classifier-selah-001",
  "new_confidence": 0.77,
  "confidence_delta": +0.05,
  "routing_improvement": "+2.1%"
}
```

### 3. Routing Intelligence Endpoint
**GET `/mcp/prompts/governor/intelligence`**

Governor queries learned patterns from Trinity.

**Response:**
```json
{
  "status": "ok",
  "task_types": ["routing", "coding", "analysis"],
  "domain_patterns": {
    "router-prompts": {
      "success_rate": 0.92,
      "avg_confidence": 0.72,
      "agents_using": ["nova", "eve"],
      "learned_at": "2026-07-27T02:00:00Z"
    }
  },
  "high_confidence_domains": ["router-prompts"],
  "low_confidence_domains": []
}
```

## Data Flow

```
Daily Pipeline (02:00 UTC)
  ├─ Prompts generated + imported
  ├─ Registry rebuilt (≥0.7 confidence indexed)
  ├─ Trinity facts accumulated
  └─ Trinity facts exported

Agent Task Execution
  ├─ Governor queries /governor/route
  ├─ Registry returns ≥0.8 confidence prompts
  ├─ Governor routes to specialist with prompt
  ├─ Specialist executes
  ├─ Feedback: success/failure
  └─ Governor POSTs /governor/feedback

Confidence Evolution
  ├─ Feedback updates prompt confidence (+0.05 or -0.10)
  ├─ Promotion check: ≥0.8 → registry status active
  ├─ Trinity learns: "prompt X successful on task Y"
  └─ Next query uses improved confidence

Trinity Pattern Learning
  ├─ Collects: promotion events + feedback signals
  ├─ Analyzes: which prompts work for which domains
  ├─ Scores: routing confidence per domain
  └─ Feeds: /governor/intelligence endpoint
```

## Success Metrics

**Short-term (Week 1):**
- ✅ Governor query endpoint working
- ✅ Routing feedback recorded
- ✅ Confidence updates flowing
- ✅ Trinity receiving signals

**Medium-term (Week 2-3):**
- Trinity pattern analysis complete
- Governor routing confidence >0.85 on high-frequency tasks
- Feedback loop demonstrating improvement (confidence trending up)
- Multiple prompts at ≥0.8 threshold

**Long-term (Month 1+):**
- Autonomous routing (Governor makes decisions without human intervention)
- Domain-specific specialists hot-loaded based on task type
- Feedback loop tight (<30 min from task → improvement)
- New task types automatically discoverable

## Integration Points

**1. Registry ← Confidence Updates**
- Feedback endpoint updates prompt confidence
- Registry rebuilds (or incremental updates)
- Next query reflects improved scores

**2. Trinity ← Learning Signals**
- Promotion events tagged with domain
- Feedback facts tagged with success/failure
- Trinity RAG indexes by domain + confidence

**3. Governor ← Routing Recommendations**
- Governor queries high-confidence prompts
- Gets ranked list by success_rate + confidence
- Chooses specialist based on task type
- Reports results back for feedback

## Risk Mitigation

**Low confidence prompts (0.3–0.79):**
- Still available for query
- Marked as "experimental"
- Governor can request if no high-confidence options
- Feedback helps them improve

**Feedback loop lag:**
- Daily pipeline runs at 02:00 UTC
- Trinity updates after each run
- Governor queries within hours (not minutes)
- Acceptable for batch learning, needs async updates for real-time

**Trinity not ready:**
- Phase 5 works without Trinity (uses confidence scores alone)
- Trinity integration enhances (adds domain patterns)
- Graceful fallback: confidence-ranked recommendations

## Phase 5 Milestones

**Phase 5a: Governor Query + Feedback** (2 hours)
- Query endpoint
- Feedback endpoint
- Confidence updates
- Test with 2 prompts

**Phase 5b: Trinity Intelligence** (1 hour)
- Query learned patterns
- Domain analysis
- Routing insights

**Phase 5c: Autonomous Routing** (4+ hours)
- Governor makes decisions autonomously
- Real-time specialist selection
- Feedback loop closed

## Deployment

When ready:
1. Governor component queries `/mcp/prompts/governor/route`
2. Registry returns best prompts (≥0.8 confidence)
3. Governor selects specialist + prompt
4. After execution, POST `/mcp/prompts/governor/feedback`
5. Confidence updates flow back
6. Trinity learns patterns
7. Next query uses improved data

---

**This closes the loop:** Agents learn → Prompts improve → Governor routes better → Tasks execute faster.
