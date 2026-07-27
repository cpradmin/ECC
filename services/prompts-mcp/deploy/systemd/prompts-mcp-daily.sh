#!/usr/bin/env bash
#
# Daily training pipeline: extract patterns -> generate prompts -> import
#                          -> rebuild registry -> export trinity facts
#
# Hardened rewrite. Fixes applied vs. the original:
#   * flock guard        - concurrent runs can no longer interleave API writes
#   * trap cleanup EXIT  - temp files always removed (was leaking into /tmp)
#   * SIGTERM/SIGINT     - clean unwind when systemd times the unit out
#   * curl --fail/--retry- HTTP errors are detected instead of silently logged
#   * 0600 state perms   - exported facts are no longer world-readable
#   * single log writer  - was double-writing via `>>$LOG` inside `| tee $LOG`
#   * stale-import guard - was re-importing the same 2026-07-25 payload daily
#
set -Eeuo pipefail

# ---------------------------------------------------------------------------
# Configuration (all overridable via the systemd unit's Environment=)
# ---------------------------------------------------------------------------
REPO_DIR="${REPO_DIR:-/home/kntrnjb/Projects/prompts-mcp}"
STATE_DIR="${STATE_DIR:-$REPO_DIR/.state}"
LOG_DIR="${LOG_DIR:-/var/log/prompts-mcp}"
# The lock MUST live at the same path for every caller, otherwise a systemd run
# and a hand-run would take two different locks and happily run concurrently.
# /var/lib/prompts-mcp is the systemd StateDirectory (persistent, owned by the
# service user); the fallbacks only matter on a box where it does not exist.
for _ld in /var/lib/prompts-mcp "${XDG_RUNTIME_DIR:-}" /tmp; do
  [[ -n "$_ld" && -d "$_ld" && -w "$_ld" ]] && { LOCK_DIR="${LOCK_DIR:-$_ld}"; break; }
done
LOCK_DIR="${LOCK_DIR:-/tmp}"
LOCK_FILE="${LOCK_FILE:-$LOCK_DIR/prompts-mcp-daily.lock}"
API_BASE="${API_BASE:-http://localhost:8762}"
MEMORY_DIR="${MEMORY_DIR:-/home/kntrnjb/.claude/projects/-home-kntrnjb/memory}"

# Import payload. Historically /tmp/import_selah_prompts.json, which was both a
# symlink-attack surface (root read a non-root file out of a sticky dir) and a
# correctness bug: the file was never consumed, so every run re-imported it.
IMPORT_FILE="${IMPORT_FILE:-$STATE_DIR/import_selah_prompts.json}"
LEGACY_IMPORT_FILE="${LEGACY_IMPORT_FILE:-/tmp/import_selah_prompts.json}"

# Max wall-clock per HTTP call, and retry policy for transient 5xx / conn reset.
CURL_MAX_TIME="${CURL_MAX_TIME:-30}"
CURL_RETRIES="${CURL_RETRIES:-3}"

# Files created by this script must not be world-readable.
umask 0077

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
mkdir -p "$LOG_DIR" "$STATE_DIR"
chmod 0750 "$STATE_DIR" 2>/dev/null || true
TIMESTAMP="$(date +%Y-%m-%d_%H-%M-%S)"
LOG="$LOG_DIR/daily-$TIMESTAMP.log"

# Single writer: everything (stdout+stderr, this script and all children) goes
# through one tee. The original both appended `>> "$LOG"` per-command AND piped
# the whole block through `tee "$LOG"`, so the same file had two independent
# write offsets and output was corrupted/duplicated.
exec > >(tee -a "$LOG") 2>&1
TEE_PID=$!
chmod 0640 "$LOG" 2>/dev/null || true

log()  { printf '[%s] %s\n' "$(date -Is)" "$*"; }
die()  { printf '[%s] FATAL: %s\n' "$(date -Is)" "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Concurrency guard (flock)
# ---------------------------------------------------------------------------
# Rationale for flock over `systemd-run`: the lock must also protect *manual*
# invocations and any future cron/AWX caller, not just the timer. systemd
# already serialises repeat starts of a Type=oneshot unit, but that guarantee
# evaporates the moment someone runs the script by hand -- which is exactly how
# a second writer would land on the API mid-rebuild. flock is in-band, needs no
# systemd, and releases automatically if the process is SIGKILLed.
#
# FD 200 is held for the lifetime of the script; the kernel drops it on exit.
mkdir -p "$(dirname "$LOCK_FILE")" 2>/dev/null || true
exec 200>"$LOCK_FILE" || die "cannot open lock file $LOCK_FILE"
if ! flock -n 200; then
  log "another pipeline run holds $LOCK_FILE -- exiting without doing work"
  # Exit 0: an overlapping run is expected/benign, not a unit failure.
  exit 0
fi
# Record who holds it, for debugging.
printf '%s pid=%s started=%s\n' "$(hostname)" "$$" "$(date -Is)" >&200 || true

# ---------------------------------------------------------------------------
# Cleanup / signal handling
# ---------------------------------------------------------------------------
# Every temp file is registered here and removed on ANY exit path.
declare -a TMP_FILES=()
CLEANED=0

cleanup() {
  local rc=$?
  [[ $CLEANED -eq 1 ]] && return 0
  CLEANED=1
  set +e
  if ((${#TMP_FILES[@]})); then
    log "cleanup: removing ${#TMP_FILES[@]} temp file(s)"
    rm -f -- "${TMP_FILES[@]}"
  fi
  # Lock is released implicitly when FD 200 closes at process exit.
  log "pipeline exited rc=$rc"
  # Flush the logger: close our end of the pipe and let tee drain before the
  # process dies, otherwise the final lines (including this one) are lost.
  exec 1>&- 2>&-
  [[ -n "${TEE_PID:-}" ]] && wait "$TEE_PID" 2>/dev/null
  return $rc
}

on_signal() {
  local sig="$1" num="$2"
  # Disarm first so a second signal cannot re-enter this handler.
  trap - TERM INT HUP EXIT
  log "received SIG$sig -- aborting, cleaning up"
  # Terminate the supervised child (a hung curl/python) but NOT the process
  # group: `kill -- -$$` also kills the tee logger, which swallowed the cleanup
  # output and raced the temp-file removal.
  kill_child
  cleanup
  exit $((128 + num))
}

# Kill the supervised job and everything it spawned, then WAIT for it to die.
#
# Signalling only $CHILD_PID kills the wrapper subshell but leaves the actual
# curl running. That curl had not yet opened its `-o` output file (the server
# was still thinking), so it recreated the temp file *after* cleanup had
# deleted it -- the leak the test kept reporting. `set -m` puts each background
# job in its own process group so `kill -- -PID` reaches the whole tree.
kill_child() {
  [[ -z "${CHILD_PID:-}" ]] && return 0
  kill -TERM -- -"$CHILD_PID" 2>/dev/null || kill -TERM "$CHILD_PID" 2>/dev/null || true
  # Give it a moment, then escalate; never block cleanup indefinitely.
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    kill -0 "$CHILD_PID" 2>/dev/null || break
    sleep 0.2
  done
  kill -KILL -- -"$CHILD_PID" 2>/dev/null || kill -KILL "$CHILD_PID" 2>/dev/null || true
  wait "$CHILD_PID" 2>/dev/null || true
  CHILD_PID=""
}

# Run an external command so that signals stay responsive and the lock cannot
# outlive us. Two non-obvious requirements, both found by the concurrency test:
#
#   1. `200>&-`  -- children INHERIT the lock file descriptor. A curl that
#      survived the parent kept the flock held, so the next run saw a stale
#      lock and refused to start. Closing FD 200 in the child fixes it.
#
#   2. background + `wait` -- bash does not run traps while a FOREGROUND child
#      is executing; it defers them until the child exits. With --max-time 30
#      that delayed SIGTERM handling by up to 30s and let systemd escalate to
#      SIGKILL, skipping cleanup entirely. `wait` IS interruptible by traps.
#   3. `set -m` -- puts each background job in its own process group so the
#      signal handler can take down curl and any grandchildren, not just the
#      wrapper subshell.
set -m
CHILD_PID=""
supervise() {
  "$@" 200>&- &
  CHILD_PID=$!
  local rc=0
  wait "$CHILD_PID" || rc=$?
  CHILD_PID=""
  return $rc
}

trap cleanup EXIT
trap 'on_signal TERM 15' TERM
trap 'on_signal INT   2' INT
trap 'on_signal HUP   1' HUP
trap 'die "unexpected error at line $LINENO"' ERR

# Usage: mktemp_tracked <outvar> [prefix]
# NOTE: this deliberately assigns through a nameref instead of echoing the path
# for capture via $(...). Command substitution runs in a SUBSHELL, so a
# `TMP_FILES+=(...)` performed inside it is discarded when the subshell exits --
# the registry would stay empty and the EXIT trap would clean up nothing. This
# exact bug leaked 3 temp files per run until the concurrency test caught it.
mktemp_tracked() {
  local -n __out="$1"
  local f
  f="$(mktemp "${STATE_DIR}/.tmp.${2:-pipeline}.XXXXXXXX")" || die "mktemp failed"
  TMP_FILES+=("$f")
  __out="$f"
}

# ---------------------------------------------------------------------------
# HTTP helper: fail loudly, retry transients, bounded time
# ---------------------------------------------------------------------------
api() {
  # usage: api <curl args...>
  curl --silent --show-error \
       --fail-with-body \
       --retry "$CURL_RETRIES" \
       --retry-delay 2 \
       --retry-connrefused \
       --max-time "$CURL_MAX_TIME" \
       --connect-timeout 5 \
       "$@"
}

# ---------------------------------------------------------------------------
log "=== Prompts-MCP Daily Training Pipeline ==="
log "repo=$REPO_DIR api=$API_BASE state=$STATE_DIR log=$LOG"

cd "$REPO_DIR" || die "cannot cd to $REPO_DIR"

# Step 0: the whole pipeline is HTTP-driven; bail early (and loudly) if the
# server is down rather than failing three steps in. The 2026-07-26 run only
# "worked" because the server happened to be up; it is not managed by systemd.
log "Step 0: probing API..."
if ! supervise api --output /dev/null "$API_BASE/healthz"; then
  die "prompts-mcp API unreachable at $API_BASE -- is the server running?"
fi
log "  API reachable"

# Step 1: Extract patterns -----------------------------------------------------
log "Step 1: extracting patterns from memory..."
[[ -d "$MEMORY_DIR" ]] || die "memory dir not found: $MEMORY_DIR"
supervise ./memory-trainer --action extract --memory-dir "$MEMORY_DIR"

# Step 2: Generate prompts with Selah -----------------------------------------
# Best-effort: a stalled local model must not fail the run, but we no longer
# swallow the reason.
log "Step 2: generating prompts with Selah..."
if ! supervise python3 scripts/selah_local_generate.py; then
  log "  WARN: Selah generation failed or timed out (non-fatal, continuing)"
fi

# Step 3: Import ---------------------------------------------------------------
log "Step 3: importing prompts..."
# One-time migration off the world-writable /tmp location.
if [[ ! -f "$IMPORT_FILE" && -f "$LEGACY_IMPORT_FILE" && ! -L "$LEGACY_IMPORT_FILE" ]]; then
  log "  migrating legacy import payload out of /tmp"
  install -m 0600 "$LEGACY_IMPORT_FILE" "$IMPORT_FILE"
  rm -f "$LEGACY_IMPORT_FILE"
fi

if [[ -f "$IMPORT_FILE" ]]; then
  if [[ ! -s "$IMPORT_FILE" ]]; then
    log "  import payload is empty -- skipping"
    rm -f "$IMPORT_FILE"
  else
    mktemp_tracked resp import
    if supervise api -X POST "$API_BASE/mcp/prompts/import" \
           -H 'Content-Type: application/json' \
           --data-binary @"$IMPORT_FILE" -o "$resp"; then
      log "  import response: $(cat "$resp")"
      # CONSUME the payload. The original left it in place, so the same
      # 2026-07-25 batch was re-imported on every single run.
      archive="$STATE_DIR/imported/$(date +%Y%m%d-%H%M%S).json"
      mkdir -p "$STATE_DIR/imported"
      install -m 0600 "$IMPORT_FILE" "$archive"
      rm -f "$IMPORT_FILE"
      log "  payload consumed -> $archive"
    else
      die "import failed (payload retained at $IMPORT_FILE)"
    fi
  fi
else
  log "  no import payload at $IMPORT_FILE -- skipping"
fi

# Step 4: Rebuild registry index ----------------------------------------------
log "Step 4: rebuilding registry index..."
mktemp_tracked resp rebuild
supervise api -X POST "$API_BASE/mcp/prompts/registry/rebuild" -o "$resp"
log "  rebuild response: $(cat "$resp")"

# Step 5: Export Trinity facts -------------------------------------------------
# NOTE: ?clear=true is destructive -- the server os.Remove()s trinity-facts.tsv
# after serving it. Only clear once the export has landed on disk non-empty,
# otherwise a failed transfer silently destroys the facts.
log "Step 5: exporting Trinity facts..."
mktemp_tracked export_tmp trinity
if supervise api -X GET "$API_BASE/mcp/prompts/export-trinity?clear=true" \
       -H 'Accept: text/tab-separated-values' \
       -o "$export_tmp"; then
  if [[ -s "$export_tmp" ]]; then
    dest="$STATE_DIR/trinity-facts-export.tsv"
    install -m 0600 "$export_tmp" "$dest"
    log "  exported $(wc -l < "$dest") fact line(s) -> $dest (0600)"
  else
    log "  export returned no facts (nothing pending)"
  fi
else
  log "  WARN: Trinity export failed (non-fatal); facts remain server-side"
fi

# Step 6: Enforce state file permissions --------------------------------------
# The Go server creates these with os.OpenFile(..., 0644) and export-trinity
# with clear=true DELETES trinity-facts.tsv, so it is recreated 0644 on the
# next write. chmod here is therefore a recurring necessity, not a one-off.
# Durable fix: change the 0644 literals in handlers/feedback.go and
# handlers/trinity.go to 0600.
log "Step 6: tightening state file permissions..."
XDG_DATA="${XDG_DATA_HOME:-$HOME/.local/share}"
for f in "$XDG_DATA/trinity-facts/trinity-facts.tsv" \
         "$XDG_DATA/ecc-prompts/projects/ember-swarm/feedback.jsonl"; do
  if [[ -f "$f" ]]; then
    chmod 0600 "$f" && log "  chmod 0600 $f"
  fi
done

log "=== Complete ==="
