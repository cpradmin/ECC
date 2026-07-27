# Registry API Design — Phase 4

**Objective:** Make high-confidence prompts discoverable and versioned within the Ember ecosystem.

## Current State (Phase 1-3)

```
~/.local/share/ecc-prompts/
├── instincts/personal/        # filesystem storage (by domain)
│   ├── router-prompts/
│   ├── go-coding-prompts/
│   └── memory-prompts/
└── projects/ember-swarm/       # feedback log
    └── feedback.jsonl
```

**Current Endpoints:**
- GET `/mcp/prompts/list` — list all (by domain/scope)
- GET `/mcp/prompts/get` — fetch single
- GET `/mcp/prompts/search` — text search
- POST `/mcp/prompts/feedback` — log feedback
- GET `/mcp/prompts/export` — export YAML/JSON
- POST `/mcp/prompts/import` — import prompts
- GET `/mcp/prompts/export-trinity` — export Trinity facts

## Phase 4 Design

### Registry Concept

A **registry** is a curated, versioned index of high-confidence prompts (≥0.8) ready for:
- **Discovery** — search across all domains, find what works
- **Versioning** — track prompt evolution, revert if needed
- **Metrics** — success rate, agent coverage, last updated
- **Release** — promote from experimental to stable

### Registry Storage Structure

```
~/.local/share/ecc-prompts/
├── instincts/                  # [existing] prompt files
├── projects/                   # [existing] feedback + metadata
└── registry/                   # [NEW] registry index & metadata
    ├── registry-index.json     # master index: all versioned prompts
    ├── {domain}/
    │   └── versions.json       # per-domain version history
    └── releases/               # [future] GitHub release metadata
        └── v0.1.0/
            └── manifest.json
```

### Registry Index Structure

**File:** `~/.local/share/ecc-prompts/registry/registry-index.json`

```json
{
  "version": "1.0",
  "generated_at": "2026-07-27T02:00:00Z",
  "total_prompts": 15,
  "domains": {
    "router-prompts": {
      "count": 5,
      "latest_version": "0.2.0",
      "average_confidence": 0.78,
      "latest_success_rate": 0.92
    },
    "go-coding-prompts": {
      "count": 4,
      "latest_version": "0.1.5",
      "average_confidence": 0.71,
      "latest_success_rate": 0.88
    }
  },
  "prompts": [
    {
      "id": "router-classifier-selah-001",
      "domain": "router-prompts",
      "current_version": "0.2.0",
      "confidence": 0.72,
      "success_rate": 0.92,
      "agents_tested": ["nova", "eve"],
      "created_at": "2026-07-25T12:00:00Z",
      "updated_at": "2026-07-27T02:00:00Z",
      "versions": [
        "0.1.0",
        "0.1.5",
        "0.2.0"
      ],
      "registry_status": "active"  # active, experimental, deprecated
    }
  ]
}
```

### New Registry Endpoints

#### 1. GET `/mcp/prompts/registry`
List all registry entries (paginated).

**Query params:**
- `domain` — filter by domain
- `min_confidence` — minimum confidence (default: 0.8)
- `sort` — sort by: `confidence`, `success_rate`, `updated` (default: `confidence`)
- `limit` — max results (default: 50)
- `offset` — pagination offset (default: 0)

**Response:**
```json
{
  "status": "ok",
  "total": 15,
  "limit": 50,
  "offset": 0,
  "prompts": [
    {
      "id": "...",
      "domain": "...",
      "version": "0.2.0",
      "confidence": 0.72,
      "success_rate": 0.92,
      "agents_tested": [...],
      "updated_at": "..."
    }
  ]
}
```

#### 2. GET `/mcp/prompts/registry/search`
Semantic + keyword search across registry.

**Query params:**
- `query` — search term
- `domain` — filter by domain
- `limit` — max results (default: 20)

**Response:**
```json
{
  "status": "ok",
  "query": "confidence scoring",
  "results": [
    {
      "id": "...",
      "domain": "...",
      "relevance_score": 0.95,
      "confidence": 0.72,
      "snippet": "..."
    }
  ]
}
```

#### 3. GET `/mcp/prompts/registry/{domain}/{version}`
Fetch specific versioned prompt from registry.

**Response:** Full prompt + version metadata

#### 4. POST `/mcp/prompts/registry/promote`
Promote a prompt (≥0.8 confidence) from project scope to registry.

**Body:**
```json
{
  "prompt_id": "router-classifier-selah-001",
  "version": "0.2.0",
  "release_notes": "Improved routing accuracy to 92%"
}
```

**Response:**
```json
{
  "status": "promoted",
  "prompt_id": "router-classifier-selah-001",
  "version": "0.2.0",
  "registry_url": "/mcp/prompts/registry/router-prompts/0.2.0"
}
```

#### 5. GET `/mcp/prompts/registry/stats`
Registry metrics and health.

**Response:**
```json
{
  "status": "ok",
  "total_prompts": 15,
  "domains": 6,
  "average_confidence": 0.76,
  "average_success_rate": 0.89,
  "last_update": "2026-07-27T02:00:00Z",
  "prompts_by_domain": {
    "router-prompts": 5,
    "go-coding-prompts": 4
  }
}
```

## Implementation Plan

### Phase 4a: Registry Index (Week 1)
- [ ] Create registry metadata file structures
- [ ] Implement registry index builder (reads FS, generates index)
- [ ] Add endpoint: GET `/mcp/prompts/registry` (list all)
- [ ] Add endpoint: GET `/mcp/prompts/registry/stats` (metrics)
- [ ] Wire into daily pipeline: rebuild index after import

### Phase 4b: Discovery & Versioning (Week 2)
- [ ] Implement version tracking (semantic versioning)
- [ ] Add endpoint: GET `/mcp/prompts/registry/{domain}/{version}`
- [ ] Add endpoint: GET `/mcp/prompts/registry/search` (keyword + semantic)
- [ ] Add endpoint: POST `/mcp/prompts/registry/promote` (0.8+ → registry)

### Phase 4c: GitHub Releases (Week 3)
- [ ] Export high-confidence registry as GitHub release
- [ ] Create release manifest (version, changelog, prompts)
- [ ] Tag releases semantically (v0.1.0, v0.2.0, etc.)

## Governor Routing Integration (Phase 5)

Once registry is live:
1. Governor queries `/mcp/prompts/registry?domain={task_type}&min_confidence=0.8`
2. Routes high-confidence prompts to specialist domains
3. Feedback loop updates confidence → registry updates automatically
4. Trinity learns promotion patterns → Governor improves routing

## Data Flow

```
Daily Pipeline (02:00 UTC)
  ├─ Extract patterns (Step 1)
  ├─ Generate prompts (Step 2)
  ├─ Import prompts (Step 3)
  │   └─ Logs feedback + promotion facts
  ├─ Export Trinity facts (Step 4)
  └─ [NEW] Rebuild registry index (Step 5)
       ├─ Promotes 0.8+ confidence prompts
       ├─ Updates versions
       └─ Regenerates registry-index.json

Registry Index
  ├─ Queried by: Governor routing, Echo discovery, Eve exploration
  ├─ Updated by: Daily pipeline + manual promote API
  └─ Published to: GitHub releases (Phase 4c)

Trinity
  ├─ Receives: Promotion events + feedback facts
  ├─ Learns: Which prompts work well in which contexts
  └─ Feeds: Gov routing confidence via scorecard
```

## Success Metrics

- [ ] Registry index built daily (0 errors)
- [ ] 10+ prompts in registry with ≥0.8 confidence
- [ ] Registry API queries in <100ms
- [ ] Governor routing uses registry queries (Phase 5)
- [ ] Trinity bulk import from Trinity facts TSV

---

**Next:** Implement Phase 4a (registry index builder + basic list/stats endpoints)
