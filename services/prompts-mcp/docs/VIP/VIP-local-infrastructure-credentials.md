---
name: vip-local-infrastructure-credentials
description: "🚨 LOCAL INFRASTRUCTURE CREDENTIALS — Postgres, Forge, Proxmox SSH Access"
metadata: 
  node_type: memory
  type: reference
  criticality: CRITICAL
  date: 2026-07-25
  owner: Adam Bailey
  originSessionId: 9000b0fb-4096-4858-a570-67627000430e
  modified: 2026-07-26T01:29:03.468Z
---

# 🚨 LOCAL INFRASTRUCTURE — SSH CREDENTIALS

**All passwordless key auth (no passwords needed)**

## Primary Database Server

| Service | Host | IP | SSH User | Auth | Port |
|---------|------|----|---------|----|------|
| **Postgres (savera)** | ember-savera | 192.168.2.110 | root | Passwordless | 22 |

**Connect:** `ssh root@192.168.2.110`
**Then:** `sudo -u postgres psql -d savera`

---

## Backup / Secondary Infrastructure

| Service | Host | IP | SSH User | Auth | Notes |
|---------|------|----|---------|----|-------|
| **Proxmox (home PVE)** | proxmox | 192.168.2.10 | root | Passwordless | Host LAN |
| **Forge (Forgejo)** | ember-forge | 100.64.3.3 | kntrnjb | Passwordless | Nebula IP (old) |

### Proxmox (192.168.2.10)
```bash
ssh root@192.168.2.10
# Web UI: https://192.168.2.10:8006
```

### Forge (ember-forge)
```bash
ssh kntrnjb@100.64.3.3
# Note: 100.64.3.3 is Nebula, not NetBird
# Use 100.81.x.x for current overlay
```

---

## Database Details

**Host:** ember-savera (192.168.2.110)
**Postgres Database:** savera
**Schema:** prompts_training (applied 2026-07-25)
**Status:** ✅ Operational, 10 patterns + 3 prompts loaded

**Tables:**
- `prompts_training.patterns` (10 rows)
- `prompts_training.generated_prompts` (3 rows, ready_for_import=true)
- `prompts_training.feedback_signals` (3 rows)
- `prompts_training.examples` (0 rows)
- `prompts_training.memory_index` (0 rows)

---

## Selah's Training Database

**Location:** Check Selah's local DB on pop-os-two or 100.81.109.186
**Access:** Via Ollama at 10.174.210.10:11434
**Training Data:** Distilled observations from agent feedback loop

**Commands:**
```bash
# Connect to Selah system
ssh pop-os-two  # or 100.81.109.186

# Check Selah training/distillation DB
ls ~/.selah/training*.db 2>/dev/null
sqlite3 ~/.selah/training.db ".tables"

# Query training observations
sqlite3 ~/.selah/training.db "SELECT * FROM observations LIMIT 10;"
```

---

## CRITICAL: NetBird API Access

| Service | Endpoint | User | Token | Status |
|---------|----------|------|-------|--------|
| **NetBird API** | https://api.netbird.io | nova | `nbp_7uru9BNy41MZMH9DPYlLnh4HYAieYn44d1GU` | ✅ ACTIVE |

**Usage:**
```bash
# Auth header
curl -H "Authorization: Bearer nbp_7uru9BNy41MZMH9DPYlLnh4HYAieYn44d1GU" \
  https://api.netbird.io/api/peers
```

**This controls:** All overlay peers (100.81.x.x range), user access, device enrollment

---

## CRITICAL: FDOT D3 Proxmox (10.175.110.20)

| Service | Endpoint | User | Password | Status |
|---------|----------|------|----------|--------|
| **D3 PVE** | https://10.175.110.20:8006/ | root | (see pass-cli) | ✅ ACTIVE |

**Access:**
- Web UI: https://10.175.110.20:8006/
- SSH: ssh root@10.175.110.20
- Network: FDOT D3 LAN (10.175.x.x range)
- Note: This is the D3 infrastructure PVE host

**This controls:** D3 VMs, containers, CCTV infrastructure, ITS systems

---

## Remember

1. **Postgres:** 192.168.2.110 → root SSH → sudo -u postgres psql -d savera
2. **Proxmox:** 192.168.2.10 → root SSH
3. **Forge:** 100.64.3.3 (Nebula, not current) or use current NetBird IPs
4. **Selah:** Check pop-os-two for training DB distillations
5. **All passwordless:** No authentication needed, key-based SSH

---

**DO NOT LOSE THIS INFORMATION**
