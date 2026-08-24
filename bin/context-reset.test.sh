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

# A precondition this harness cannot meet is a SKIP on a developer laptop and an
# ERROR under CI. Exiting 0 with zero assertions is the vacuous-green failure the
# dependency preflight in .github/workflows/ci.yml exists to prevent — and that
# preflight only covers the interpreters on PATH, not whether a copied binary can
# actually execute out of the harness's own mktemp root (a noexec $TMPDIR, or a
# refused copy, skipped the whole file green).
missing_precondition() {
  if [ -n "${CI:-}" ]; then
    printf '::error::bin/context-reset.test.sh could not run: %s\n' "$1" >&2
    printf 'FAIL: harness preconditions unmet under CI — %s\n' "$1" >&2
    exit 1
  fi
  printf 'SKIP: %s\n' "$1" >&2
  exit 0
}

command -v python3 >/dev/null 2>&1 || missing_precondition "python3 not installed (needed to allocate a pty)"

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
[ -n "$FAKE_CLAUDE" ] || missing_precondition "no runnable bash copy could stand in for the claude process (is $ROOT on a noexec mount?)"

AID="cr-test-agent"
AGENT_DIR="$ROOT/agents/$AID"
mkdir -p "$AGENT_DIR"

cat > "$ROOT/ptyrun.py" <<'PY'
import pty, sys
sys.exit(pty.spawn(["bash", "-c", sys.argv[1]]))
PY

# `handoff show <known-id>` yields a body; every other id fails with no output, so
# the degraded path (a bead the store cannot produce) is exercisable too.
# handoff-esc1's body is the hostile case: a bead body is agent-authored free text,
# so it carries an OSC title/clipboard write, an SGR colour run, a cursor-position
# QUERY (whose reply a terminal types back onto its own input), a C1-encoded CSI, and
# ordinary UTF-8 that must survive untouched.
cat > "$ROOT/bin/handoff" <<'STUB'
#!/usr/bin/env bash
if [ "${1:-}" = "show" ]; then
  case "${2:-}" in
    handoff-live1|handoff-live2) ;;
    handoff-esc1)
      printf 'HANDOFF-BODY-MARKER for %s\n' "$2"
      printf '\033]0;PWNED-TITLE\007ESC-OSC-TAIL\n'
      printf '\033[31mESC-CSI-TAIL\033[0m\n'
      printf 'QUERY\033[6nESC-QUERY-TAIL\n'
      printf '\302\233 31mC1-CSI-TAIL\n'
      printf 'kept \302\251 utf8\tand a tab\n'
      exit 0 ;;
    *) exit 1 ;;
  esac
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
  rm -f "$ROOT/inner.done" "$ROOT/guard.fail"
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

# Runs a command with a hard wall-clock bound and no dependency on `timeout` being
# installed. A command that has to be killed reports 137, which is how a hang is
# distinguished from a refusal.
run_bounded() {
  local secs="$1"; shift
  "$@" >"$ROOT/bounded.out" 2>&1 &
  local _p=$! _k rc
  ( sleep "$secs"; kill -9 "$_p" 2>/dev/null ) >/dev/null 2>&1 &
  _k=$!
  wait "$_p"; rc=$?
  kill "$_k" 2>/dev/null; wait "$_k" 2>/dev/null
  return "$rc"
}

# The ancestor walk in context-reset keeps the OUTERMOST process whose `comm` is
# `claude`. Until the fixture chain has finished reparenting, that walk can climb
# past the fixture — so a harness run by hand from inside a real claude session
# could resolve the operator's own process, which a non-dry case would then KILL.
# Every destructive invocation therefore proves its target first: a --dry run in the
# same pane reports the pid the walk resolves, and the real command runs only when
# that pid is this fixture's claude ($$ of the script claude is executing). A
# mismatch records why and aborts the case with nothing killed.
guarded() {
  cat <<GUARD
_resolved=\$("$SCRIPT" --dry 2>/dev/null | sed -n 's/^claude PID *: *//p')
if [ "\$_resolved" != "\$\$" ]; then
  echo "resolved=\$_resolved fixture=\$\$" > $ROOT/guard.fail
  touch $ROOT/inner.done
  exit 1
fi
$1
GUARD
}

guard_held() {
  [ -f "$ROOT/guard.fail" ] || return 0
  fail "$1: the ancestor walk did not resolve the fixture ($(cat "$ROOT/guard.fail")) — no kill was issued"
  return 1
}

pin_handoff() {
  { echo "---"; echo "id: $AID"; echo "---"; echo; echo "> 📎 Handoff: $1 — pinned"; } > "$AGENT_DIR/identity.md"
}

# The same write, emitted as shell for an inner script to run INSIDE the fake pane.
# An env pin is only trusted when identity.md agrees with it and was written after
# this claude process started, which is what the real `identity --park/--submit`
# does microseconds before invoking; a pin written by the harness beforehand looks
# inherited instead.
pin_handoff_inline() {
  printf 'printf "%%s\\n" "---" "id: %s" "---" "" "> 📎 Handoff: %s — pinned" > %s/identity.md' \
    "$AID" "$1" "$AGENT_DIR"
}

watcher_logs() { cat "$ROOT"/reincarnate-*.log 2>/dev/null; }

# ── 1. the regression: a non-tty stdin must not hide the pane's tty ──────────
# The pin is (re)stamped INSIDE the pane, after a pause: `ps -o etime=` reports whole
# seconds, so claude's derived start can land up to a second after its real one, and a
# pointer written just before the fixture launched would read as predating it. The
# pause puts the freshness beyond that resolution instead of leaving it to a race.
pin_handoff "handoff-abc1"
run_in_fake_pane "echo \$\$ > $ROOT/fixture.pid; sleep 2; touch $AGENT_DIR/identity.md; $SCRIPT --dry > $ROOT/dry1.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty1.log"
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
RESOLVED1="$(printf '%s' "$DRY1" | sed -n 's/^claude PID *: *//p')"
if [ -n "$RESOLVED1" ] && [ "$RESOLVED1" = "$(cat "$ROOT/fixture.pid" 2>/dev/null)" ]; then
  pass "the walk resolves the fixture's own claude, not an outer one"
else
  fail "walk resolved pid '$RESOLVED1', fixture was '$(cat "$ROOT/fixture.pid" 2>/dev/null)'"
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
if printf '%s' "$DRY2" | grep -q "handoff 'handoff-stale' will not be echoed"; then
  pass "a stale pin is named when it is dropped, not dropped in silence"
else
  fail "dropping a stale pin left no diagnostic: $DRY2"
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
if printf '%s' "$DRY5" | grep -q "will not be echoed — the pointer in"; then
  pass "an unusable pointer is named when it is dropped, not dropped in silence"
else
  fail "dropping an unusable pointer left no diagnostic: $DRY5"
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
run_in_fake_pane "$(guarded "$SCRIPT --handoff handoff-live1 > $ROOT/live1.out 2>&1")" "$ROOT/pty6.log" \
  'handoff echo: handoff-live1 written'
guard_held "case 6" || true
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
run_in_fake_pane "$(guarded "$SCRIPT --handoff handoff-gone9 > $ROOT/live2.out 2>&1")" "$ROOT/pty7.log" \
  'handoff echo: pointer for handoff-gone9'
guard_held "case 7" || true
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

# ── 8. a drop decided before the watcher spawns still reaches the log ───────
# A live --complete-shaped run (no --handoff, unusable pointer on disk): nothing is
# echoed, and the watcher — the only thing still alive — has to say which id it was.
pin_handoff 'nodashhere'
run_in_fake_pane "$(guarded "$SCRIPT > $ROOT/live3.out 2>&1")" "$ROOT/pty8.log" 'handoff echo SKIPPED'
guard_held "case 8" || true
if watcher_logs | grep -q "handoff echo SKIPPED: 'nodashhere' not surfaced"; then
  pass "a pointer dropped before the spawn is still named in the watcher log"
else
  fail "a parent-side drop left no watcher trace; watcher log: $(watcher_logs | tr '\n' '|')"
fi

# ── 9. a flag given no value refuses instead of spinning ────────────────────
# `shift 2` with one argument left does not shift, so an unguarded loop never ends.
for flag in --handoff --cmd; do
  if run_bounded 10 "$SCRIPT" "$flag"; then
    fail "$flag with no value exited 0 instead of refusing"
  else
    rc=$?
    if [ "$rc" = "2" ] && grep -q "context-reset: $flag needs" "$ROOT/bounded.out"; then
      pass "$flag with no value refuses with a usage error"
    else
      fail "$flag with no value did not refuse cleanly (exit $rc): $(cat "$ROOT/bounded.out")"
    fi
  fi
done

# ── 10. the staleness guard survives a GNU-coreutils stat ───────────────────
# GNU's -f is --file-system: it prints to stdout AND exits non-zero, so a
# `stat -f %m || stat -c %Y` chain captures both answers and every later numeric
# comparison errors out, silently disabling the guard. The stub reproduces that
# shape exactly; the guard must still fire.
cat > "$ROOT/bin/stat" <<'GNUSTAT'
#!/usr/bin/env bash
case "${1:-}" in
  -c) [ "${2:-}" = "%Y" ] || { echo "stat: bad format" >&2; exit 1; }
      shift 2; rc=0
      for f in "$@"; do
        if [ -e "$f" ]; then date -r "$f" +%s; else echo "stat: cannot stat '$f'" >&2; rc=1; fi
      done
      exit "$rc" ;;
  -f) shift; rc=0
      for f in "$@"; do
        if [ -e "$f" ]; then printf '  File: "%s"
    ID: 0 Namelen: 255
' "$f"
        else echo "stat: cannot read file system information for '$f'" >&2; rc=1; fi
      done
      exit "$rc" ;;
esac
exit 1
GNUSTAT
chmod +x "$ROOT/bin/stat"
pin_handoff "handoff-gnustale"
touch -t 200001010000 "$AGENT_DIR/identity.md"
run_in_fake_pane "$SCRIPT --dry > $ROOT/dry10.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty10.log"
DRY10="$(cat "$ROOT/dry10.out" 2>/dev/null || true)"
rm -f "$ROOT/bin/stat"
if printf '%s' "$DRY10" | grep -q 'handoff echo: none (pinned handoff predates this session'; then
  pass "the staleness guard still fires where stat speaks GNU, not BSD"
else
  fail "the staleness guard did not fire under a GNU stat, got: $(printf '%s' "$DRY10" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi

# ── 11. --help follows the header block, not a fixed line count ─────────────
# The extraction used to be a hardcoded line range, so text appended to the header
# silently vanished from --help. Grow the header on a COPY and it must show up.
awk '/^set -uo pipefail$/ && !g { print "# SENTINEL-HEADER-LINE"; g=1 } { print }' \
  "$SCRIPT" > "$ROOT/grown-help"
chmod +x "$ROOT/grown-help"
if "$ROOT/grown-help" --help 2>&1 | grep -q 'SENTINEL-HEADER-LINE'; then
  pass "--help tracks the header block when it grows"
else
  fail "a line appended to the header never reached --help"
fi

# ── 12. PARLAY_PINNED_HANDOFF is the caller's transport, --handoff overrides it ──
# The pinning callers pass the id in the environment, because an older copy of this
# script on PATH ignores an unknown env var but refuses an unknown flag with exit 2 —
# and its caller does not inspect that exit code. So the env var must be honoured with
# the flag's own authority (no staleness guard, announced as this session's), and the
# flag must still win when both are supplied. The real caller writes identity.md's
# pointer immediately before invoking, so the fixture does too — inside the pane, so
# the write post-dates the fake claude exactly as a genuine pin does.
run_in_fake_pane "$(pin_handoff_inline handoff-env1)
PARLAY_PINNED_HANDOFF=handoff-env1 $SCRIPT --dry > $ROOT/dry13.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty13.log"
DRY13="$(cat "$ROOT/dry13.out" 2>/dev/null || true)"
if printf '%s' "$DRY13" | grep -q 'handoff echo: handoff-env1 would be echoed to /dev/.* (id passed by the caller)'; then
  pass "PARLAY_PINNED_HANDOFF outranks identity.md and skips the staleness guard"
else
  fail "expected the env-supplied id, got: $(printf '%s' "$DRY13" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi

pin_handoff "handoff-stale"
touch -t 200001010000 "$AGENT_DIR/identity.md"
run_in_fake_pane "PARLAY_PINNED_HANDOFF=handoff-env1 $SCRIPT --handoff handoff-flag1 --dry > $ROOT/dry14.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty14.log"
DRY14="$(cat "$ROOT/dry14.out" 2>/dev/null || true)"
if printf '%s' "$DRY14" | grep -q 'handoff echo: handoff-flag1 would be echoed'; then
  pass "--handoff overrides PARLAY_PINNED_HANDOFF when both are given"
else
  fail "expected the flag to win over the env var, got: $(printf '%s' "$DRY14" | grep 'handoff echo' || echo "<no handoff echo line>")"
fi

# The whole point of the env transport: an id it cannot use is still refused loudly,
# exactly as the flag is, rather than passed to `handoff show` as garbage.
run_in_fake_pane "PARLAY_PINNED_HANDOFF=nodashhere $SCRIPT --dry > $ROOT/dry15.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty15.log"
DRY15="$(cat "$ROOT/dry15.out" 2>/dev/null || true)"
if printf '%s' "$DRY15" | grep -q "handoff 'nodashhere' will not be echoed — PARLAY_PINNED_HANDOFF is not a bead id"; then
  pass "an unusable PARLAY_PINNED_HANDOFF is named and dropped, not handed on"
else
  fail "an unusable env pin left no diagnostic: $DRY15"
fi

# ── 13. an unknown env var is inert, where an unknown flag is fatal ─────────
# This is the property that makes the env var the safe transport across a version
# skew: run the script with a variable it has never heard of and it must behave
# exactly as it does without one, still exiting 0.
run_in_fake_pane "PARLAY_UNKNOWN_TO_THIS_SCRIPT=x $SCRIPT --dry > $ROOT/dry16.out 2>&1; echo \$? > $ROOT/rc16; touch $ROOT/inner.done" "$ROOT/pty16.log"
if [ "$(cat "$ROOT/rc16" 2>/dev/null)" = "0" ] && grep -q 'handoff echo' "$ROOT/dry16.out" 2>/dev/null; then
  pass "an env var this script does not know is inert, not a refusal"
else
  fail "an unknown env var changed the outcome (exit $(cat "$ROOT/rc16" 2>/dev/null)): $(cat "$ROOT/dry16.out" 2>/dev/null)"
fi
if "$SCRIPT" --unknown-flag-this-script-never-had >/dev/null 2>&1; then
  fail "an unknown FLAG was accepted — the version-skew hazard the env transport avoids"
else
  pass "an unknown flag is still refused, which is why the pin travels in the environment"
fi

# ── 14. the pin is consumed here, never handed down to what this script starts ──
# The env transport reaches every descendant, unlike the argv flag it replaced: the
# detached watcher, the reboot command it evals, and the agent that command relaunches
# would all inherit this session's pin — and the next session's --complete would then
# print its predecessor's handoff under the "pinned by this session" banner. So the
# value has to stop at this process. PARLAY_AGENT_ID is dropped for this case only so
# the watcher's post-reboot channel poll (90s, needs a live server) is skipped.
cat > "$ROOT/bin/record-relaunch" <<RECORDER
#!/usr/bin/env bash
env > $ROOT/relaunch.env
echo "RELAUNCH-RAN"
RECORDER
chmod +x "$ROOT/bin/record-relaunch"
rm -f "$ROOT/relaunch.env"
{ echo "---"; echo "id: $AID"; echo "---"; } > "$AGENT_DIR/identity.md"
run_in_fake_pane "$(guarded "env -u PARLAY_AGENT_ID PARLAY_PINNED_HANDOFF=handoff-live1 PARLAY_PIN_TEST_CONTROL=present $SCRIPT --reboot --cmd record-relaunch > $ROOT/live4.out 2>&1")" \
  "$ROOT/pty17.log" 'RELAUNCH-RAN'
guard_held "case 14" || true
if [ ! -s "$ROOT/relaunch.env" ]; then
  fail "the reboot command never ran, so nothing was proven about what it inherits; watcher log: $(watcher_logs | tr '\n' '|')"
elif ! grep -q '^PARLAY_PIN_TEST_CONTROL=present$' "$ROOT/relaunch.env"; then
  fail "the recorded environment is not the one the invocation was given — the absence check below would be vacuous"
elif grep -q '^PARLAY_PINNED_HANDOFF=' "$ROOT/relaunch.env"; then
  fail "the relaunched session inherited this session's pin: $(grep '^PARLAY_PINNED_HANDOFF=' "$ROOT/relaunch.env")"
else
  pass "neither the watcher nor the command it reboots inherits PARLAY_PINNED_HANDOFF"
fi

# Consuming it must not mean dropping it: the same env pin still has to reach the pane
# on the clean-end path, announced as this session's.
{ echo "---"; echo "id: $AID"; echo "---"; } > "$AGENT_DIR/identity.md"
run_in_fake_pane "$(guarded "$(pin_handoff_inline handoff-live2)
PARLAY_PINNED_HANDOFF=handoff-live2 $SCRIPT > $ROOT/live5.out 2>&1")" \
  "$ROOT/pty18.log" 'handoff echo: handoff-live2 written'
guard_held "case 14b" || true
PANE18="$(cat "$ROOT/pty18.log" 2>/dev/null || true)"
if printf '%s' "$PANE18" | grep -q 'HANDOFF-BODY-MARKER' && printf '%s' "$PANE18" | grep -q 'session ended, full handoff below'; then
  pass "an env-supplied pin still reaches the pane as this session's handoff"
else
  fail "the env pin did not reach the pane; watcher log: $(watcher_logs | tr '\n' '|')"
fi

# ── 15. nothing a bead body says can drive the terminal it lands on ─────────
# A handoff body is agent-authored free text and the pane has just dropped back to a
# shell prompt, so an escape sequence in it would act on a live terminal: an OSC can
# retitle the window or reach the clipboard, and a cursor-position QUERY makes the
# terminal write its answer onto its own INPUT, where the waiting shell reads it as
# typed characters. handoff-esc1's body carries all of those; the pane must receive
# the surrounding text and none of the introducers.
{ echo "---"; echo "id: $AID"; echo "---"; } > "$AGENT_DIR/identity.md"
run_in_fake_pane "$(guarded "$SCRIPT --handoff handoff-esc1 > $ROOT/live6.out 2>&1")" \
  "$ROOT/pty19.log" 'handoff echo: handoff-esc1 written'
guard_held "case 15" || true
if ! LC_ALL=C grep -q 'HANDOFF-BODY-MARKER' "$ROOT/pty19.log" 2>/dev/null; then
  fail "the hostile body never reached the pane, so the filtering assertions would be vacuous; watcher log: $(watcher_logs | tr '\n' '|')"
else
  pass "the hostile handoff body reached the pane"
  LEAKED=""
  LC_ALL=C grep -q "$(printf '\033')" "$ROOT/pty19.log" && LEAKED="$LEAKED ESC"
  LC_ALL=C grep -q "$(printf '\007')" "$ROOT/pty19.log" && LEAKED="$LEAKED BEL"
  LC_ALL=C grep -q "$(printf '\302\233')" "$ROOT/pty19.log" && LEAKED="$LEAKED C1-CSI"
  if [ -n "$LEAKED" ]; then
    fail "control introducers reached the pane device:$LEAKED"
  else
    pass "no ESC, BEL or C1 introducer from the body reaches the terminal"
  fi
  MISSING=""
  for _t in ESC-OSC-TAIL ESC-CSI-TAIL ESC-QUERY-TAIL C1-CSI-TAIL; do
    LC_ALL=C grep -q "$_t" "$ROOT/pty19.log" || MISSING="$MISSING $_t"
  done
  if [ -n "$MISSING" ]; then
    fail "filtering ate readable text alongside the escapes, losing:$MISSING"
  else
    pass "the text around each stripped sequence still reaches the pane"
  fi
  # The tab is matched as whitespace, not as a byte: a pty in its default output mode
  # may expand it. Deleting it — the failure this guards — leaves no gap at all.
  UTF8_TAB_RE="$(printf 'kept \302\251 utf8[[:space:]][[:space:]]*and a tab')"
  if LC_ALL=C grep -qE "$UTF8_TAB_RE" "$ROOT/pty19.log" 2>/dev/null; then
    pass "printable UTF-8 and TAB survive the filter untouched"
  else
    fail "the filter damaged printable UTF-8 or TAB: $(LC_ALL=C grep -a 'utf8' "$ROOT/pty19.log" | cat -v)"
  fi
fi

# ── 16. an env pin this session did not set is demoted, never trusted ───────
# An OLDER copy of this script on PATH does not unset PARLAY_PINNED_HANDOFF, so the
# session it reboots inherits the previous session's pin for its whole life. Trusting
# an ambient value would print a predecessor's handoff under this session's banner, so
# an env pin is believed only when identity.md agrees with it AND was written after
# this claude process started.
run_in_fake_pane "$(pin_handoff_inline handoff-live1)
PARLAY_PINNED_HANDOFF=handoff-env9 $SCRIPT --dry > $ROOT/dry20.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty20.log"
DRY20="$(cat "$ROOT/dry20.out" 2>/dev/null || true)"
if printf '%s' "$DRY20" | grep -q 'handoff echo: handoff-live1 would be echoed to /dev/.* (last known'; then
  pass "an env pin identity.md does not corroborate loses to the file's own pointer"
else
  fail "expected the disk pointer to win over an uncorroborated env pin, got: $(printf '%s' "$DRY20" | grep 'handoff echo' || echo '<no handoff echo line>')"
fi
if printf '%s' "$DRY20" | grep -q "PARLAY_PINNED_HANDOFF='handoff-env9' was not pinned by this session"; then
  pass "the demotion is announced, naming the value that was not trusted"
else
  fail "an env pin was demoted in silence: $DRY20"
fi

# Same value on both sides, but identity.md predates the process: that is exactly the
# inherited shape (the successor's identity.md still holds its predecessor's pointer),
# so it must land on the staleness refusal rather than the caller's banner.
pin_handoff "handoff-env9"
touch -t 200001010000 "$AGENT_DIR/identity.md"
run_in_fake_pane "sleep 2; PARLAY_PINNED_HANDOFF=handoff-env9 $SCRIPT --dry > $ROOT/dry21.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty21.log"
DRY21="$(cat "$ROOT/dry21.out" 2>/dev/null || true)"
if printf '%s' "$DRY21" | grep -q 'handoff echo: none (pinned handoff predates this session'; then
  pass "an env pin matching only a pointer older than this session is refused"
else
  fail "an inherited env pin was echoed as this session's, got: $(printf '%s' "$DRY21" | grep 'handoff echo' || echo '<no handoff echo line>')"
fi

# A --reboot self-reports its handoff decision like every other branch: the pin is
# deliberately not echoed there, and silence would read as the id having been lost.
run_in_fake_pane "$(pin_handoff_inline handoff-live1)
$SCRIPT --reboot --dry > $ROOT/dry22.out 2>&1; touch $ROOT/inner.done" "$ROOT/pty22.log"
DRY22="$(cat "$ROOT/dry22.out" 2>/dev/null || true)"
if printf '%s' "$DRY22" | grep -q 'handoff echo: none — handoff-live1 is deliberately not echoed on a --reboot'; then
  pass "--reboot names the pin it is deliberately not echoing"
else
  fail "the reboot path reported nothing about the pin it holds: $(printf '%s' "$DRY22" | grep 'handoff echo' || echo '<no handoff echo line>')"
fi

[ "$FAILED" = "0" ] && echo "ALL PASS" || echo "SOME TESTS FAILED" >&2
exit "$FAILED"
