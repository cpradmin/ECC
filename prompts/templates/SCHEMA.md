# Prompt YAML Schema

All prompts follow this YAML frontmatter + Markdown content format:

## Frontmatter

```yaml
---
id: unique-identifier-lowercase-hyphenated
trigger: "plain english description of when to use this prompt"
confidence: 0.7                 # 0.3-0.95, evolves with feedback
domain: domain-name             # router, conversation, go-coding, etc.
source: session-observation     # how was this prompt discovered/created
scope: project                  # "project" (Swarm-specific) or "global"
project_id: ember-swarm-v1      # if scope is project
agents_tested: [nova, eve, claw]  # which agents have successfully used this
success_rate: 0.92              # percentage of successful applications
feedback_count: 42              # number of feedback events logged
last_updated: "2026-07-25"      # ISO 8601 date of last update
---
```

## Content

The Markdown content below the frontmatter is the actual prompt text. Include:

1. **Title** — Clear, descriptive heading
2. **Purpose** — What problem does this prompt solve?
3. **Context** — When/how is this prompt applied?
4. **Instructions** — The actual prompt to use (can include placeholders)
5. **Examples** — Sample inputs and expected outputs
6. **Notes** — Edge cases, limitations, related prompts

## Naming Convention

Files should be named:
```
NN-prompt-name.yaml
```

Where:
- `NN` is a two-digit sequence number (00, 01, 02, etc.)
- `prompt-name` is a lowercase-hyphenated short description
- Domain folders auto-organize by this naming

## Example

See `templates/router-prompts/00-domain-classifier.yaml` for a complete example.

## Storage Locations

**In repository** (ECC fork): 
- Templates and examples in `prompts/templates/` directory

**In user's home** (runtime):
- Personal project-scoped: `${XDG_DATA_HOME}/ecc-prompts/projects/ember-swarm-v1/`
- Global reusable: `${XDG_DATA_HOME}/ecc-prompts/instincts/personal/` (or `inherited/`)

## Confidence Evolution

Prompts start at 0.3-0.7 (tentative) and evolve:
- Each **success** → +0.05 (capped at 0.95)
- Each **failure** → -0.1 (floor at 0.3)
- **Promotion**: when avg confidence ≥0.8 AND tested by 3+ agents

The feedback loop (in `projects/ember-swarm/feedback.jsonl`) automatically tracks these updates.
