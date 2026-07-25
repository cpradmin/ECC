# Prompts Template Index

Reference templates for the Ember Swarm Prompts Library. These files serve as examples and starting points for creating actual prompts.

## Organization by Domain

### [Router Prompts](router-prompts/)
Intent classification and 6-domain routing logic for the Ember Swarm Router.

- **[00-domain-classifier.yaml](router-prompts/00-domain-classifier.yaml)** — Primary example: 6-domain intent classifier with full YAML schema
- Purpose: Route user intent to the correct Swarm specialist
- Confidence: 0.85 (well-tested across 42 real Swarm tasks)

### [Conversation Prompts](conversation-prompts/)
Eve specialist — dialogue, narrative, teaching, explanation, natural language reasoning.

- Purpose: Guide clear, engaging communication and explanation
- Current templates: (to be added in Phase 2)

### [Go Coding Prompts](go-coding-prompts/)
Go specialist — systems programming, infrastructure code, performance-critical components.

- Purpose: Guide efficient, concurrent, production-grade Go development
- Current templates: (to be added in Phase 2)

### [Python Coding Prompts](python-coding-prompts/)
Python specialist — data processing, automation, ML workflows, rapid prototyping.

- Purpose: Guide data-focused, well-tested Python development
- Current templates: (to be added in Phase 2)

### [IaC Prompts](iac-prompts/)
Infrastructure as code specialist — Terraform/Ansible, cloud deployment, configuration management.

- Purpose: Guide safe, idempotent infrastructure automation
- Current templates: (to be added in Phase 2)

### [Memory Prompts](memory-prompts/)
Trinity/RAG specialist — knowledge retrieval, context synthesis, pattern matching, cross-session learning.

- Purpose: Guide effective knowledge management and memory integration
- Current templates: (to be added in Phase 2)

## How to Use These Templates

### For Users

1. Copy a template YAML file to your local `${XDG_DATA_HOME}/ecc-prompts/instincts/personal/<domain>/`
2. Customize the frontmatter and content for your use case
3. Test the prompt with Swarm agents
4. Log feedback to `projects/ember-swarm-v1/feedback.jsonl`
5. Confidence updates automatically based on success/failure

### For Contributors

1. Read [SCHEMA.md](SCHEMA.md) to understand the YAML format
2. Create a new domain template or expand an existing one
3. Include at least 1-2 complete examples (see `router-prompts/00-domain-classifier.yaml`)
4. Ensure your template includes clear trigger conditions and success criteria
5. Submit a PR to the `ember-prompts` branch

## What Makes a Good Template

✅ **Clear trigger condition** — When should this prompt be used?  
✅ **Specific examples** — Show input/output or expected behavior  
✅ **Testable success criteria** — How do we know if this prompt works?  
✅ **Reference to feedback loop** — How confidence should evolve  
✅ **Confidence starting point** — What initial confidence level? (0.3-0.7 for new prompts)  

## Phase 1 Status

✅ Foundation: Directory structure and schema defined  
✅ Router prompt: Complete example (00-domain-classifier.yaml)  
✅ Domain READMEs: Placeholders for all 6 domains  
⏳ Phase 2: MCP service to expose these as queryable resources  
⏳ Phase 3: Feedback loop automation for confidence evolution  
⏳ Phase 4: Team sharing and registry integration  

## Next: Phase 2 (MCP Service)

The `prompts-mcp` Go service will:
- Read these templates from `${XDG_DATA_HOME}/ecc-prompts/`
- Expose `/mcp/prompts/*` endpoints for search, get, feedback, export/import
- Integrate with mcpproxy for Swarm access
- Auto-update confidence scores based on logged feedback

See `../../config.json` for MCP endpoint definitions.
