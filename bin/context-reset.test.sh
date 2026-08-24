#!/usr/bin/env bash
# Behavior tests for bin/context-reset's clean-end handoff echo (robots-q5yx).
#
# The original defect: the pane's tty was resolved with `tty`, which inspects fd 0.
# On the real path — claude's Bash tool -> `parlay identity --park/--submit` -> here
# — stdin is a pipe, so `tty` answered "not a tty", the target came back empty, and
# the pinned handoff was never echoed anywhere a human could see it.
#
# Reproducing that needs the real shape, not a stubbed one: a process whose `comm`
# is exactly "claude", holding a pty, running context-reset with a NON-tty stdin.
# So the fixture copies a real bash binary to a file named `claude`, gives it a
# controlling terminal via python's pty module, and detaches the chain so the
# script's ancestor walk stops at the fake claude instead of a real one. Every
# assertion reads what the script printed, what the watcher logged, or what landed
# on the pane device — never the script's source.
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

# `handoff show <known-id>` yields a body; every other id fails with no output, so
# the degraded path (a bead the store cannot produce) is exercisable too.
cat > "$ROOT/bin/handoff" <<'STUB'
#!/usr/bin/env bash
if [ "${1:-}" = "show" ] && [ "${2:-}" = "handoff-live1" ]; then
  echo "HANDOFF-BODY-MARKER for $2"
  exit 0
fi
exit 1
STUB
chmod +x "$ROOT/bin/handoff"

# Runs $1 (a shell script path is written for it) inside a fake claude session that
# owns a pty, with the invoked command's own stdin redirected away from that pty —
# the real Bash-tool shape. The chain is detached twice so the ancestor walk
# terminates at the fake claude:
#   pty -> [detached] pane shell -> claude -> $1
# The pane shell deliberately outlives claude, exactly as a real pane's shell does.
# $3 is an optional regex; the pane stays up until the detached watcher logs it (so
# a full non-dry run finishes as soon as the echo has happened, not on a fixed
# timer). Captured pty output — everything the pane would have shown — lands in $2.
run_in_fake_pane() {
  local inner="$1" capture="$2" until_re="${3:-}"
  rm -f "$ROOT/inner.done"
  printf '%s\n' "$inner" > "$ROOT/inner.sh"
  printf '%s' "$until_re" > "$ROOT/until.re"
  cat > "$ROOT/pane.sh" <<PANE
export PATH=$ROOT/bin:\$PATH HOME=$ROOT TMPDIR=$ROOT CLAUDECODE=1
export PARLAY_AGENT_ID=$AID PARLAY_AGENT_HOME=$ROOT/agents
( bash -c 'claude $ROOT/inner.sh </dev/null; sleep 25' & ) &
_re=\$(cat $ROOT/until.re)
for _ in \$(seq 1 200); do
  [ -f $ROOT/inner.done ] && break
  if [ -n "\$_re" ] && grep -qs "\$_re" $ROOT/reincarnate-*.log; then sleep 0.3; break; fi
  sleep 0.2
done
PANE
  python3 "$ROOT/ptyrun.py" "bash $ROOT/pane.sh" </dev/null >"$capture" 2>&1
}

pin_handoff() {
  { echo "---"; echo "id: $AID"; echo "---"; echo; echo "> 📎 Handoff: $1 — pinned"; } > "$AGENT_DIR/identity.md"
}

watcher_logs() { cat "$ROOT"/reincarnate-*.log 2>/dev/null; }

# ── 1. the regression: a non-tty stdin must not hide the pane's tty ──────────
pin_handoff "handoff-abc1"
run_in_fake_pane "$SCRIPT --dry > $ROOT/dry1.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty1.log"
DRY1="$(cat "$ROOT/dry1.out" 2>/dev/null || true)"
if printf '%s' "$DRY1" | grep -q 'could not find claude ancestor'; then
  fail "fixture did not present a claude ancestor: $DRY1"
elif printf '%s' "$DRY1" | grep -q 'handoff echo: handoff-abc1 would be echoed to /dev/'; then
  pass "a scraped handoff targets the claude pane's tty even though stdin is not a tty"
else
  fail "expected the pane tty as the echo target, got: $(printf '%s' "$DRY1" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi
if printf '%s' "$DRY1" | grep -q 'last known — scraped from identity.md'; then
  pass "a scraped pointer is reported as last-known, not as pinned by this run"
else
  fail "a scraped pointer was not labelled last-known: $(printf '%s' "$DRY1" | grep 'handoff echo' || echo '<none>')"
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

# ── 4. --handoff <id> outranks the file and is not treated as last-known ────
# The caller that just pinned passes the id, so a stale-looking identity.md (here:
# a DIFFERENT, ancient pointer) must not influence the answer at all.
pin_handoff "handoff-stale"
touch -t 200001010000 "$AGENT_DIR/identity.md"
run_in_fake_pane "$SCRIPT --handoff handoff-live1 --dry > $ROOT/dry4.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty4.log"
DRY4="$(cat "$ROOT/dry4.out" 2>/dev/null || true)"
if printf '%s' "$DRY4" | grep -q 'handoff echo: handoff-live1 would be echoed to /dev/.* (id passed by the caller)'; then
  pass "--handoff wins over identity.md and skips the staleness guard"
else
  fail "expected the caller-supplied id, got: $(printf '%s' "$DRY4" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi

# ── 5. a pointer that is not a bead id is treated as unpinned ───────────────
# The marker line is hand-editable and `handoff show` is a subprocess, so a value
# carrying shell syntax must never be forwarded. The sentinel proves nothing
# evaluated it on the way through, either.
SENTINEL="$ROOT/injected"
pin_handoff 'handoff-x$(touch '"$SENTINEL"')'
run_in_fake_pane "$SCRIPT --dry > $ROOT/dry5.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty5.log"
DRY5="$(cat "$ROOT/dry5.out" 2>/dev/null || true)"
if printf '%s' "$DRY5" | grep -q 'handoff echo: none (handoff pointer is not a bead id'; then
  pass "a pointer carrying shell syntax is rejected as unpinned"
else
  fail "expected a not-a-bead-id report, got: $(printf '%s' "$DRY5" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi
if [ -e "$SENTINEL" ]; then
  fail "a command substitution in the handoff pointer was evaluated"
else
  pass "a command substitution in the handoff pointer is never evaluated"
fi

# ── 6. the full run really reaches the pane ─────────────────────────────────
# Not --dry: the watcher spawns, kills the fake claude, verifies closure, and
# writes to the pane device.
{ echo "---"; echo "id: $AID"; echo "---"; } > "$AGENT_DIR/identity.md"
run_in_fake_pane "$SCRIPT --handoff handoff-live1 > $ROOT/live1.out 2>&1" "$ROOT/pty6.log" \
  'handoff echo: handoff-live1 written'
PANE6="$(cat "$ROOT/pty6.log" 2>/dev/null || true)"
if printf '%s' "$PANE6" | grep -q 'HANDOFF-BODY-MARKER'; then
  pass "the watcher echoes the handoff body onto the pane after claude closes"
else
  fail "handoff body never reached the pane; watcher log: $(watcher_logs | tr '\n' '|')"
fi
if printf '%s' "$PANE6" | grep -q 'session ended, full handoff below'; then
  pass "a caller-supplied handoff is announced as this session's"
else
  fail "expected the pinned-by-this-session banner on the pane"
fi

# ── 7. an unreadable body degrades to the pointer and says so in the log ────
# handoff-gone9 makes the stub exit non-zero with no output — the store-error /
# deleted-bead / handoff-not-on-PATH class. The id must still reach the pane, and
# the failure must leave a trace naming it instead of vanishing.
run_in_fake_pane "$SCRIPT --handoff handoff-gone9 > $ROOT/live2.out 2>&1" "$ROOT/pty7.log" \
  'handoff echo: pointer for handoff-gone9'
PANE7="$(cat "$ROOT/pty7.log" 2>/dev/null || true)"
if printf '%s' "$PANE7" | grep -q 'run .handoff show handoff-gone9.'; then
  pass "an unavailable handoff body still puts the id on the pane"
else
  fail "the pointer fallback never reached the pane; watcher log: $(watcher_logs | tr '\n' '|')"
fi
if watcher_logs | grep -q "handoff echo DEGRADED: 'handoff show handoff-gone9' produced no output"; then
  pass "a failed handoff lookup is logged, naming the handoff it could not surface"
else
  fail "a failed handoff lookup left no diagnostic; watcher log: $(watcher_logs | tr '\n' '|')"
fi

# ── 8. --help keeps the whole HARD RULE paragraph ───────────────────────────
HELP="$("$SCRIPT" --help 2>&1)"
if printf '%s' "$HELP" | grep -q 'which pins + resets your context in one act'; then
  pass "--help prints the HARD RULE paragraph through to its last sentence"
else
  fail "--help truncated the HARD RULE paragraph; last line was: $(printf '%s' "$HELP" | tail -1)"
fi

[ "$FAILED" = "0" ] && echo "ALL PASS" || echo "SOME TESTS FAILED" >&2
exit "$FAILED"
