#!/usr/bin/env bash
# Behavior tests for bin/context-reset's clean-end handoff echo (robots-q5yx).
#
# The defect: the pane's tty was resolved with `tty`, which inspects fd 0. On the
# real path — claude's Bash tool -> `parlay identity --park/--submit` -> here —
# stdin is a pipe, so `tty` answered "not a tty", the target came back empty, and
# the pinned handoff was never echoed anywhere a human could see it.
#
# Reproducing that needs the real shape, not a stubbed one: a process whose `comm`
# is exactly "claude", holding a pty, running context-reset with a NON-tty stdin.
# So the fixture copies a real bash binary to a file named `claude`, gives it a
# controlling terminal via python's pty module, and detaches the chain so the
# script's ancestor walk stops at the fake claude instead of a real one. Every
# assertion reads what the script printed or wrote, never its source.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SELF_DIR/context-reset"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

command -v python3 >/dev/null 2>&1 || { echo "SKIP: python3 not installed (needed to allocate a pty)" >&2; exit 0; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/context-reset-test.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

# --- fixture: a real binary named `claude` ---------------------------------
# A #! script will not do: its `comm` is the interpreter, and the walk in
# context-reset compares `comm` against the literal "claude". macOS also SIGKILLs
# a copy of a SIP-protected system binary, so try each candidate and keep the
# first whose copy actually runs.
FAKE_CLAUDE=""
for cand in "$(command -v bash)" /opt/homebrew/bin/bash /usr/local/bin/bash /bin/bash; do
  [ -n "$cand" ] && [ -x "$cand" ] || continue
  cp "$cand" "$ROOT/bin/claude" 2>/dev/null || continue
  "$ROOT/bin/claude" -c 'exit 7' >/dev/null 2>&1
  [ "$?" = "7" ] && { FAKE_CLAUDE="$cand"; break; }
done
[ -n "$FAKE_CLAUDE" ] || { echo "SKIP: no runnable bash copy could stand in for the claude process" >&2; exit 0; }

AID="cr-test-agent"
AGENT_DIR="$ROOT/agents/$AID"
mkdir -p "$AGENT_DIR"

cat > "$ROOT/ptyrun.py" <<'PY'
import pty, sys
sys.exit(pty.spawn(["bash", "-c", sys.argv[1]]))
PY

# Runs $1 inside a fake claude session that owns a pty, with the invoked
# command's own stdin redirected away from that pty (the real Bash-tool shape).
# The chain is detached twice so the ancestor walk terminates at the fake claude:
#   pty -> [detached] pane shell -> claude -> $1
# The pane shell deliberately outlives claude, exactly as a real pane's shell does.
# Captured pty output (everything the pane would have shown) lands in $2.
run_in_fake_pane() {
  local inner="$1" capture="$2" wait_for="${3:-}"
  local runner="export PATH=$ROOT/bin:\$PATH HOME=$ROOT TMPDIR=$ROOT CLAUDECODE=1 \
PARLAY_AGENT_ID=$AID PARLAY_AGENT_HOME=$ROOT/agents; \
( bash -c 'claude -c \"$inner\" </dev/null; sleep 25' & ) & \
for _ in \$(seq 1 150); do [ -f $ROOT/inner.done ] && break; sleep 0.2; done"
  if [ -n "$wait_for" ]; then
    runner="$runner; for _ in \$(seq 1 $wait_for); do sleep 1; done"
  fi
  rm -f "$ROOT/inner.done"
  python3 "$ROOT/ptyrun.py" "$runner" </dev/null >"$capture" 2>&1
}

pin_handoff() {
  { echo "---"; echo "id: $AID"; echo "---"; echo; echo "> 📎 Handoff: $1 — pinned"; } > "$AGENT_DIR/identity.md"
}

# ── 1. the regression: a non-tty stdin must not hide the pane's tty ──────────
pin_handoff "handoff-abc1"
run_in_fake_pane "$SCRIPT --dry > $ROOT/dry1.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty1.log"
DRY1="$(cat "$ROOT/dry1.out" 2>/dev/null || true)"
if printf '%s' "$DRY1" | grep -q 'could not find claude ancestor'; then
  fail "fixture did not present a claude ancestor: $DRY1"
elif printf '%s' "$DRY1" | grep -q 'handoff echo: handoff-abc1 would be echoed to /dev/'; then
  pass "pinned handoff targets the claude pane's tty even though stdin is not a tty"
else
  fail "expected the pane tty as the echo target, got: $(printf '%s' "$DRY1" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi

# ── 2. a pin that predates this session is not presented as this session's ──
# `identity --complete` never pins, so whatever is on disk is a leftover.
pin_handoff "handoff-stale"
touch -t 200001010000 "$AGENT_DIR/identity.md"
run_in_fake_pane "$SCRIPT --dry > $ROOT/dry2.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty2.log"
DRY2="$(cat "$ROOT/dry2.out" 2>/dev/null || true)"
if printf '%s' "$DRY2" | grep -q 'handoff echo: none (pinned handoff predates this session'; then
  pass "a handoff pinned before this session started is dropped, not echoed"
else
  fail "expected a stale-pin refusal, got: $(printf '%s' "$DRY2" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi

# ── 3. no pin at all ────────────────────────────────────────────────────────
{ echo "---"; echo "id: $AID"; echo "---"; } > "$AGENT_DIR/identity.md"
run_in_fake_pane "$SCRIPT --dry > $ROOT/dry3.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty3.log"
DRY3="$(cat "$ROOT/dry3.out" 2>/dev/null || true)"
if printf '%s' "$DRY3" | grep -q 'handoff echo: none (no pinned handoff'; then
  pass "an identity with no pin reports nothing to echo"
else
  fail "expected a no-pin report, got: $(printf '%s' "$DRY3" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi

# ── 4. the full run really reaches the pane, and does not evaluate the id ───
# Not --dry: the watcher spawns, kills the fake claude, verifies closure and
# writes to the pane device. The pinned id carries a command substitution; if the
# watcher ever evaluated it the sentinel would exist and the banner would differ.
cat > "$ROOT/bin/handoff" <<'STUB'
#!/usr/bin/env bash
[ "${1:-}" = "show" ] && { echo "HANDOFF-BODY-MARKER for ${2:-}"; exit 0; }
exit 1
STUB
chmod +x "$ROOT/bin/handoff"
SENTINEL="$ROOT/injected"
INJ='handoff-x$(touch '"$SENTINEL"')'
pin_handoff "$INJ"
run_in_fake_pane "$SCRIPT > $ROOT/live.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty4.log" 12
PANE="$(cat "$ROOT/pty4.log" 2>/dev/null || true)"
if printf '%s' "$PANE" | grep -q 'HANDOFF-BODY-MARKER'; then
  pass "the watcher echoes the handoff body onto the pane after claude closes"
else
  fail "handoff body never reached the pane; watcher log: $(cat "$ROOT"/reincarnate-*.log 2>/dev/null | tr '\n' '|')"
fi
if [ -e "$SENTINEL" ]; then
  fail "the watcher evaluated a command substitution embedded in the pinned handoff id"
else
  pass "a pinned id containing \$(...) is never evaluated by the watcher"
fi

# ── 5. --help keeps the whole HARD RULE paragraph ───────────────────────────
HELP="$("$SCRIPT" --help 2>&1)"
if printf '%s' "$HELP" | grep -q 'which pins + resets your context in one act'; then
  pass "--help prints the HARD RULE paragraph through to its last sentence"
else
  fail "--help truncated the HARD RULE paragraph; last line was: $(printf '%s' "$HELP" | tail -1)"
fi

[ "$FAILED" = "0" ] && echo "ALL PASS" || echo "SOME TESTS FAILED" >&2
exit "$FAILED"
