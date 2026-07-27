# Contribution Status — Ember Swarm Ecosystem

**Last updated:** 2026-07-26  
**Scope:** prompts-mcp, ECC (fork), smart-context-mcp, and related components  

---

## Active Projects

### 1. prompts-mcp

**Repository:** `/home/kntrnjb/Projects/prompts-mcp`  
**Status:** ✅ Development  
**Remote:** ❌ NONE  
**Commits (this session):** 7

| Commit | Date | Feature |
|--------|------|---------|
| 6e1f685 | 2026-07-26 | Phase 3: Trinity RAG integration |
| c1c429a | 2026-07-26 | Phase 3: Prompt persistence (SavePrompt) |
| 7123fbb | 2026-07-25 | VIP infrastructure documentation |
| 3a86dec | 2026-07-25 | Training pipeline: extraction → generation → import |
| 2728ec7 | 2026-07-25 | Phase 2: ExportPrompts/ImportPrompts endpoints |
| d015da0 | — | Feedback loop: confidence evolution + promotion logic |
| f4fbc9e | — | Phase 2: MCP service scaffolding + loader |

**What's Shipped:**
- ✅ Phase 1: 7 endpoints (ListPrompts, GetPrompt, SearchPrompts, SubmitFeedback, ExportPrompts, ImportPrompts, Health)
- ✅ Phase 2: Export/import handlers, feedback loop, confidence evolution
- ✅ Phase 3: Daily automation (systemd timer), prompt persistence, Trinity RAG logging

**What's Pending:**
- ⏳ Phase 4: GitHub Releases, registry API
- ⏳ Trinity bulk import (ExportForTrinity hook)
- ⏳ Governor routing integration (Phase 5)

**Decision:** Keep local for now. Evaluate push to Forge or GitHub when Phase 4 completes.

---

### 2. ECC Fork (Ember Swarm Prompts Library)

**Repository:** `/home/kntrnjb/Projects/ECC`  
**Status:** ✅ Phase 1 complete  
**Remote:** ✅ Configured (upstream: cpradmin/ECC)  
**Last commit:** 9ba9d33b (prompts Phase 1 foundation)

**Contribution Tracking:**
```
ECC upstream at: https://github.com/cpradmin/ECC
Fork scope:      Prompts library (Ember Swarm specific)
Branches:        Phase 1 ✅, Phase 2 ✅, Phase 3 Preview ✅
```

**Phase 1 Status:** ✅ Complete
- 6 domain templates (router, conversation, go-coding, python-coding, iac, memory)
- Foundation with Selah integration
- Ready for Phase 2 expansion

**What's Ready for Upstream:**
- Prompts library foundation (6 templates)
- Selah generation pipeline integration
- Training data from 286 memory files

**Decision:** Hold for upstream until Phase 4 (registry API) completes. Upstream prefers bundled, tested contributions over incremental.

---

### 3. smart-context-mcp (Fork)

**Repository:** `/tmp/smart-context-mcp`  
**Status:** ✅ Cloned, up-to-date with cpradmin fork  
**Fork Origin:** https://github.com/cpradmin/smart-context-mcp  
**Upstream:** https://github.com/Arrayo/smart-context-mcp  
**Branch:** main (clean, no uncommitted changes)  
**Version:** v1.20.0  
**Last Sync:** 2026-07-25 (fresh clone)

**Also Installed (Global npm):**
- ✅ Running via mcpproxy (subprocess PID 663282)
- ✅ Local Claude Code sessions (devctx in ~/.mcp.json)
- ✅ Remote agents (Eve, Claw, Selah via mcpproxy:8080)

**How We're Using It:**
- ✅ Local Claude Code sessions (devctx in ~/.mcp.json)
- ✅ Remote agents via mcpproxy (Eve, Claw, Selah)
- ✅ Exploration tool for Phase 4 (prompts-mcp registry API design)
- ✅ Research for token savings measurement (89% baseline)

**Potential Contributions Back to cpradmin/smart-context-mcp:**

1. **Custom Playbook: prompts-mcp Exploration**
   ```yaml
   name: explore-prompts-api
   steps:
     - smart_context(task="registry_architecture")
     - smart_search("export import")
     - smart_read(file="handlers/handlers.go", mode="signatures")
   ```
   Status: 📋 Design doc ready

2. **Trinity-aware Semantic Re-ranker**
   - Extend smart_search to use Trinity facts for ranking
   - Re-rank by "prompt promotions in Trinity"
   - Status: 📋 Prototype idea, needs design

3. **Governor Routing Integration**
   - Wire prompts to Heart Governance layer
   - Custom playbook for routing optimization
   - Status: 🚀 Phase 5 feature (future)

4. **Prompt-specific Checkpoint Playbook**
   - Integrate with prompts-mcp feedback loop
   - Checkpoint per-prompt effectiveness
   - Status: 📋 Roadmap item

**Decision:** 
- Use cpradmin/smart-context-mcp as-is for now (no local modifications)
- Track fork for potential contributions after Phase 4 ships
- Upstream PRs only if changes would benefit broader smart-context community (not prompts-mcp specific)
- Coordinate with cpradmin before submitting PRs (not competing with upstream)

---

## Contribution Workflow

### Local Development (prompts-mcp)

```bash
cd /home/kntrnjb/Projects/prompts-mcp

# Commit structure:
# - Feature: new endpoint, handler, tool
# - Fix: bug in existing code
# - Docs: memory, README, TRAINING_PIPELINE.md
# - Test: test suite updates

git log --oneline          # See all commits
git show <commit>          # Review specific commit
```

### ECC Fork Sync

```bash
cd /home/kntrnjb/Projects/ECC

# Check upstream
git remote -v              # Show configured remotes
git fetch upstream         # Update from cpradmin/ECC
git log master..upstream/master  # See what's new

# Push to fork
git push origin <branch>   # Push to our fork (cpradmin/smart-context-mcp fork)
```

### smart-context-mcp Usage

```bash
# Already installed globally
smart-context-status       # Check index health
smart-context-doctor       # Diagnose issues

# mcpproxy is running it
systemctl --user status mcpproxy  # Verify subprocess

# Logs
journalctl --user -u mcpproxy -f  # Watch mcpproxy logs
```

---

## Phases & Commitments

### Phase 1: Complete ✅
**prompts-mcp endpoints** — ListPrompts, GetPrompt, SearchPrompts, SubmitFeedback, ExportPrompts, ImportPrompts, Health  
**Commits:** f4fbc9e  
**Status:** Shipped, stable

### Phase 2: Complete ✅
**Export/import handlers + feedback loop** — Full confidence evolution, promotion logic  
**Commits:** 2728ec7, d015da0  
**Status:** Shipped, tested (10/10 tests passing)

### Phase 3: Complete ✅
**Daily automation + Trinity integration** — Systemd timer, prompt persistence, feedback logging  
**Commits:** 3a86dec, 7123fbb, c1c429a, 6e1f685  
**Status:** Live, running daily at 02:00 UTC

### Phase 4a: Registry API Basics ✅ IN PROGRESS
**Registry index + discovery** — List, filter, stats endpoints live  
**Commits:** 7e8d80d (Trinity export), 0ad1e85 (Registry API)  
**Status:** Shipped, tested (registry endpoints live with 2 prompts indexed)

**Endpoints:**
- GET `/mcp/prompts/registry` — list all prompts (paginated, filterable)
- GET `/mcp/prompts/registry/stats` — registry metrics
- POST `/mcp/prompts/registry/rebuild` — rebuild index

### Phase 4b: Roadmap 📋
**Semantic search + promote endpoint** — Search across registry, promote 0.8+ → registry  
**Estimated:** 1-2 commits  
**Status:** Next

### Phase 4c: GitHub Releases 📋
**Release automation** — Export registry as GitHub releases  
**Estimated:** 2-3 commits  
**Status:** After Phase 4b

### Phase 5: Future 🚀
**Governor routing integration** — Wire prompts into Heart Governance layer  
**Build go-context-mcp** (if desired, not required)

---

## Tracking

### What's Tracked
✅ Commits per phase (git log)  
✅ Endpoints implemented (handlers/handlers.go)  
✅ Tests passing (handlers/*_test.go)  
✅ Feedback loop (handlers/feedback.go)  
✅ Daily pipeline (systemd timers + logs)  
✅ Trinity integration (handlers/trinity.go)  

### What's NOT Tracked Yet
❌ Issue tracking (no GitHub issues)  
❌ PR tracking (local, no PRs)  
❌ Contribution timeline (no release schedule)  
❌ Upstream sync status (ECC only)  

### Suggested Additions
- [ ] CHANGELOG.md (per phase summaries)
- [ ] ROADMAP.md (detailed Phase 4-5 plans)
- [ ] PERFORMANCE.md (metrics from daily runs)
- [ ] DEPLOYMENT.md (installation + ops runbook)

---

## Decision Log

| Date | Decision | Rationale | Status |
|------|----------|-----------|--------|
| 2026-07-26 | Keep prompts-mcp local | Phase not complete, need design feedback | ✅ Approved |
| 2026-07-26 | Add smart-context-mcp to mcpproxy | Remote agents need token savings | ✅ Implemented |
| 2026-07-26 | Use Node.js not Go | v1.20.0 proven, 89% savings, 30min to ship | ✅ Approved |
| 2026-07-25 | Wire Trinity facts (not bulk import yet) | Accumulate first, import when ready | ✅ Approved |
| 2026-07-25 | Phase 3 automation via systemd | Reliable, low-overhead, no agent polling | ✅ Implemented |

---

## Upstreams & Forks

| Project | Fork | Upstream | Location | Sync Status |
|---------|------|----------|----------|-------------|
| prompts-mcp | ⏳ TBD | None yet | /home/kntrnjb/Projects/prompts-mcp | Local only for now |
| ECC | ✅ Yes | cpradmin/ECC | /home/kntrnjb/Projects/ECC | Fetch upstream regularly |
| smart-context-mcp | ✅ Yes | Arrayo/smart-context-mcp | /tmp/smart-context-mcp | Up-to-date (cpradmin fork) |

---

## Next Contribution Checkpoints

1. **Phase 4 midpoint** (registry API design complete)
   - Review prompts-mcp for upstream readiness
   - Decide: Forge, GitHub, or internal-only?
   - **smart-context-mcp:** Finalize 4 playbook ideas, coordinate with cpradmin

2. **Phase 4 completion** (GitHub Releases shipped)
   - Tag prompts-mcp version (v0.4.0?)
   - Update CHANGELOG
   - Decide on ECC fork merge strategy
   - **smart-context-mcp:** Submit initial PR (Trinity-aware re-ranker?) if approved

3. **Phase 5 decision** (Governor routing design)
   - If building go-context-mcp: upstream consideration
   - If Trinity-tight: contribute Trinity-integrated features back to smart-context-mcp
   - Decide: Governor routing open-source or proprietary?

---

## Metrics

### prompts-mcp Stats
- **Total commits:** 9 (this session)
- **Lines added (this session):** ~1100 (Trinity + Registry Phase 4a)
- **Endpoints:** 10 (7 Phase 1-3 + 3 Registry)
- **Test coverage:** 10/10 passing
- **Daily pipeline runs:** 1+ (active, 5 steps)
- **Prompts in production:** 3 (avg confidence 0.71)
- **Feedback signals:** 2+ (Trinity facts accumulating)
- **Registry indexed:** 2 prompts

### Development Velocity
- **Phase 1→2:** ~1 week (scaffolding + endpoints)
- **Phase 2→3:** ~1 day (automated pipeline + Trinity)
- **Phase 3→4a:** ~1 hour (registry API basics)
- **Phase 4b (est):** ~30 min (semantic search + promote)
- **Phase 4c (est):** ~1-2 hours (GitHub Releases)

---

## Questions

1. Should prompts-mcp be public or internal?
2. When to push Phase 4 to ECC fork?
3. Build go-context-mcp or keep Node.js?
4. Open-source Governor routing or keep internal?
5. Who should be listed as contributors in CONTRIBUTORS.md?

---

**Ready to push? Discuss with team before external commit.**
