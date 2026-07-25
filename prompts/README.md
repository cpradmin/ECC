# Ember Swarm Prompts Library

A cross-harness prompt library built on ECC's continuous-learning system. Prompts are treated as instincts with confidence scoring, domain tagging, and feedback loops.

## Structure

**In this repository** (ECC fork):
```
prompts/
├── templates/                # Reference templates for each domain
│   ├── SCHEMA.md            # YAML frontmatter spec
│   ├── router-prompts/      # Router specialist templates
│   ├── conversation-prompts/  # Eve specialist templates
│   ├── go-coding-prompts/   # Go specialist templates
│   ├── python-coding-prompts/ # Python specialist templates
│   ├── iac-prompts/         # IaC specialist templates
│   └── memory-prompts/      # Trinity specialist templates
├── projects/
│   └── ember-swarm/         # Project-scoped feedback (reference only)
│       └── feedback.jsonl   # Example feedback log
└── config.json              # Configuration and metadata
```

**In user's home** (runtime):
```
${XDG_DATA_HOME}/ecc-prompts/
├── instincts/
│   ├── personal/              # Team-learned prompts (domain-scoped)
│   │   ├── router-prompts/
│   │   ├── conversation-prompts/
│   │   ├── go-coding-prompts/
│   │   ├── python-coding-prompts/
│   │   ├── iac-prompts/
│   │   └── memory-prompts/
│   └── inherited/             # Imported from team/community
├── evolved/
│   ├── skills/               # Evolved into full ECC skills
│   └── agents/               # Evolved into ECC agents
└── projects/
    └── ember-swarm-v1/
        ├── instincts/
        ├── observations/
        └── feedback.jsonl    # Agent success/failure logs
```

## 6 Swarm Domains

### Router Prompts
Intent classification and 6-domain routing logic. Used by the Ember Swarm Router to understand user intent and dispatch to appropriate specialists.

### Conversation Prompts
Eve specialist — dialogue, narrative, explanation, teaching. Natural language reasoning and communication.

### Go Coding Prompts
Go specialist — systems programming, infrastructure code, performance-critical components, design patterns.

### Python Coding Prompts
Python specialist — data processing, automation, rapid prototyping, machine learning workflows.

### IaC Prompts
Terraform/Ansible specialist — infrastructure as code, cloud deployment, configuration management, orchestration.

### Memory Prompts
RAG/Trinity integration — knowledge retrieval, context synthesis, pattern matching, cross-session learning.

## Prompt Format

Each prompt is an ECC instinct with metadata:

```yaml
---
id: unique-identifier
trigger: "when condition is met"
confidence: 0.8          # 0.3-0.9, evolves based on success/failure
domain: "domain-name"    # router, conversation, go-coding, etc.
source: "origin"         # session-observation, team-training, etc.
scope: project           # project (Swarm-specific) or global
agents_tested: [nova, eve, claw]
success_rate: 0.92
---

# Prompt Title

Your prompt content here...
```

## Confidence Scoring

- **0.3–0.5**: Tentative (suggested, not enforced)
- **0.5–0.7**: Moderate (applied when relevant)
- **0.7–0.9**: Strong (auto-approved for application)
- **0.9+**: Near-certain (core behavior)

Confidence evolves:
- Success → +0.05 (capped at 0.95)
- Failure → -0.1 (floor at 0.3)
- Proven across 3+ agents → promote to global

## Development Workflow

1. **Create** — Add new prompt YAML to domain folder
2. **Test** — Swarm agents use prompt, log results to `projects/ember-swarm/feedback.jsonl`
3. **Evolve** — Confidence updates automatically based on agent feedback
4. **Promote** — High-confidence prompts promoted from project to global scope
5. **Export** — Share via `/instinct-export` command

## Integration with ECC

- Prompts are stored in `${XDG_DATA_HOME}/ecc-prompts/` (mirrors this repository)
- MCP service (`prompts-mcp`) exposes list/get/search/feedback/export/import endpoints
- Commands: `/instinct-status`, `/instinct-export`, `/instinct-import`, `/instinct-promote`
- Hooks capture agent success/failure for feedback loop

## Next Steps

- Phase 1: Template creation for each domain (in progress)
- Phase 2: MCP service implementation
- Phase 3: Feedback loop automation
- Phase 4: Team sharing and registry integration
