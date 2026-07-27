# prompts-mcp

**Phase 2: MCP Service for Ember Swarm Prompts Library**

A Go-based MCP (Model Context Protocol) service that exposes version-controlled, confidence-scored prompts to Claude AI agents. Feeds the Ember Swarm with evolved, feedback-driven prompts across 6 domains.

## Overview

The prompts-mcp service is part of the larger **Ember Swarm Prompts Library** system:

- **Phase 1** (✅ Complete): Foundation — YAML schema, 6 domain templates, router example, feedback framework
- **Phase 2** (🔨 In Progress): MCP Service — HTTP endpoints exposing prompts and feedback loops
- **Phase 3**: Automation — Feedback loop integration, confidence evolution, auto-promotion
- **Phase 4**: Team sharing — Registry integration, GitHub Releases

## Features

- **List prompts** — Filter by domain or scope (project/global)
- **Get prompt** — Retrieve a single prompt by ID with full frontmatter
- **Search** — Find prompts by keyword or domain
- **Submit feedback** — Log agent success/failure observations
- **Export** — Download prompts in YAML or JSON format
- **Import** — Upload prompts from external sources

## Architecture

```
Prompts stored in: ${XDG_DATA_HOME}/ecc-prompts/
├── instincts/
│   ├── personal/       # Team-learned prompts (domain-scoped)
│   │   ├── router-prompts/
│   │   ├── conversation-prompts/
│   │   ├── go-coding-prompts/
│   │   ├── python-coding-prompts/
│   │   ├── iac-prompts/
│   │   └── memory-prompts/
│   └── inherited/      # Imported from team/community
├── evolved/
│   ├── skills/         # Evolved into ECC skills
│   └── agents/         # Evolved into ECC agents
└── projects/
    └── ember-swarm-v1/
        ├── instincts/
        ├── observations/
        └── feedback.jsonl
```

## 6 Domains

1. **router-prompts** — Intent classification, 6-way routing logic
2. **conversation-prompts** — Eve specialist (dialogue, teaching)
3. **go-coding-prompts** — Go specialist (systems, infrastructure)
4. **python-coding-prompts** — Python specialist (data, automation)
5. **iac-prompts** — IaC specialist (Terraform, Ansible)
6. **memory-prompts** — Trinity/RAG (knowledge retrieval, context synthesis)

## API Endpoints

All endpoints are served under `/mcp/`.

### Health Check
```
GET /mcp/health
```
Returns server health status.

### List Prompts
```
GET /mcp/prompts/list?domain=go-coding&scope=project
```
Query parameters:
- `domain` (optional) — Filter by domain
- `scope` (optional) — "project" or "global"

### Get Prompt
```
GET /mcp/prompts/get?id=go-classifier-01
```
Query parameters:
- `id` (required) — Prompt ID

### Search Prompts
```
GET /mcp/prompts/search?q=routing&domain=router
```
Query parameters:
- `q` (optional) — Search keyword
- `domain` (optional) — Filter by domain

### Submit Feedback
```
POST /mcp/prompts/feedback
Content-Type: application/json

{
  "prompt_id": "go-classifier-01",
  "agent": "nova",
  "task": "code-review",
  "success": true,
  "note": "Classified Go refactoring task correctly"
}
```

### Export Prompts
```
GET /mcp/prompts/export?format=yaml&domain=router
```
Query parameters:
- `format` (optional) — "yaml" or "json" (default: "yaml")
- `domain` (optional) — Filter by domain

### Import Prompts
```
POST /mcp/prompts/import
Content-Type: multipart/form-data or application/json

[Upload YAML/JSON file or send prompts directly]
```

## Running

### Development
```bash
go run main.go
```
Listens on `:8762` by default.

### With Custom Address
```bash
PROMPTS_MCP_ADDR=:9000 go run main.go
```

### Build
```bash
go build -o prompts-mcp
./prompts-mcp
```

## Configuration

Environment variables:
- `PROMPTS_MCP_ADDR` — Server address (default: `:8762`)
- `XDG_DATA_HOME` — Prompts storage root (default: `~/.local/share`)

## Integration with Ember Swarm

The prompts-mcp service feeds confidence-scored prompts to the Ember Swarm agents:

1. **Router** calls `/mcp/prompts/get` to load domain classifiers
2. **Specialists** request domain-specific prompts at runtime
3. **Collectors** submit feedback via `/mcp/prompts/feedback`
4. **Confidence evolution** runs on feedback: success → +0.05, failure → -0.1
5. **Auto-promotion** at 0.8+ confidence with 3+ agents tested

## Testing

```bash
go test ./...
```

## Next Steps (Phase 2)

- [ ] Load prompts from YAML files in `${XDG_DATA_HOME}/ecc-prompts/`
- [ ] Implement confidence scoring and feedback log parsing
- [ ] Add search indexing (keyword → prompts)
- [ ] Integrate with Ember Swarm Router
- [ ] Add unit tests for each handler
- [ ] Add integration tests with real prompt files
