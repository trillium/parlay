#!/usr/bin/env bash
# Regression harness for bin/parlay-preflight. A check that cannot fail is not a
# check, so this builds deliberately BROKEN environments and asserts what the command
# is for: it exits 1 when anything is blocking; it names EVERY broken prerequisite in
# ONE run (not the first, not one per invocation); every FAIL carries its exact `fix:`
# command; and the summary's own count equals the FAIL lines printed, so the report
# cannot claim a number it did not produce.
#
# No production state is touched: every case runs against a synthetic checkout under a
# mktemp root, with HOME / PARLAY_* redirected into it, and the one real port it
# occupies is a kernel-chosen free port bound by a process it kills on exit.
# Usage: bin/parlay-preflight.test.sh [-v]
set -uo pipefail

# ── Self-isolation: run under a scrubbed, allowlisted environment ────────────
# Same preamble as tools/monitor/parlay-monitor.test.sh. A developer's PATH can shadow
# real system binaries with shims that leak into every subprocess; PATH lookup is
# order-sensitive, so the system dirs go FIRST. Shell-startup hook carriers are
# cleared so a subprocess cannot inherit an injected function.
__pre_sys="/usr/bin:/bin:/usr/sbin:/sbin"
export PATH="${__pre_sys}:${PATH}"
unset __pre_sys BASH_ENV ENV PROMPT_COMMAND 2>/dev/null || true
unset PARLAY_SERVER PARLAY_STATE_HOME PARLAY_AGENT_HOME PARLAY_EVAL_ENGINE_URL \
      PARLAY_RELAY_RUNTIME PARLAY_SERVER_ADDR 2>/dev/null || true
# `${!BASH_FUNC_*}` names any exported-function env entry (BASH_FUNC_x%%).
for __pre_f in ${!BASH_FUNC_*}; do unset "$__pre_f" 2>/dev/null || true; done
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${HERE}/parlay-preflight"
BASH_BIN="$(command -v bash)"

VERBOSE=0
[ "${1:-}" = "-v" ] && VERBOSE=1

pass=0; fail=0
ok()   { pass=$((pass + 1)); echo "  ok   — $1"; }
bad()  { fail=$((fail + 1)); echo "  FAIL — $1"; [ -n "${2:-}" ] && echo "         $2"; return 0; }
die()  { echo "parlay-preflight.test: $*" >&2; exit 2; }

[ -f "$SCRIPT" ] || die "missing $SCRIPT"
# Refuse to run on a machine where a PATH shim (or a broken install) has taken
# python3: every case below allocates or occupies a port with it, and a
# vacuous green from a shim is worse than a red harness. Pinning the resolved
# path also keeps the shim out of the subprocesses.
PY="$(command -v python3 2>/dev/null)" || die "python3 is required (picks a free port and occupies one)"
"$PY" -c 'pass' >/dev/null 2>&1 || die "python3 ($PY) is on PATH but does not run"
BASH_BIN="$(command -v bash)"
[ -n "$BASH_BIN" ] || die "bash is required"

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-preflight-test.XXXXXX")" || die "mktemp failed"
LISTENER_PID=""
cleanup() {
  [ -n "$LISTENER_PID" ] && kill "$LISTENER_PID" 2>/dev/null
  chmod -R u+w "$ROOT" 2>/dev/null
  case "$ROOT" in */parlay-preflight-test.*) rm -rf "$ROOT" ;; esac
}
trap cleanup EXIT

free_port() {
  "$PY" -c 'import socket
s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

# ── The assertions every case shares ─────────────────────────────────────────
# Assert the summary's failure count matches the FAIL lines actually printed.
summary_matches_printed() {
  local out="$1" printed declared
  printed=$(printf '%s\n' "$out" | grep -c '^  FAIL  ')
  declared=$(printf '%s\n' "$out" | sed -n 's/.*, \([0-9][0-9]*\) failure(s)\.$/\1/p')
  [ -n "$declared" ] || { echo "         no summary failure count in the output"; return 1; }
  [ "$printed" = "$declared" ] || {
    echo "         summary says $declared failure(s) but $printed FAIL line(s) were printed"; return 1; }
  return 0
}

# Assert every FAIL is immediately followed by its remedy.
every_fail_has_a_fix() {
  local out="$1" line expecting=0 unexplicated=0
  while IFS= read -r line; do
    if [ "$expecting" = 1 ]; then
      case "$line" in
        "        fix: "*) expecting=0; continue ;;
        *) unexplicated=$((unexplicated + 1)); expecting=0 ;;
      esac
    fi
    case "$line" in "  FAIL  "*) expecting=1 ;; esac
  done <<<"$out"
  if [ "$expecting" = 1 ] || [ "$unexplicated" -gt 0 ]; then
    echo "         $unexplicated FAIL(s) with no 'fix:' line"; return 1
  fi
  return 0
}

# Assert the output NAMES each of the given things (substring match).
names_all() { # names_all <out> <what> <needle>...
  local out="$1" what="$2"; shift 2
  local missing=""
  for needle in "$@"; do
    case "$out" in *"$needle"*) ;; *) missing="$missing '$needle'" ;; esac
  done
  [ -z "$missing" ] || { echo "         $what did not name:$missing"; return 1; }
  return 0
}

echo "parlay-preflight.test.sh — proving the preflight can fail"
echo "  preflight: $SCRIPT"
echo "  scratch:   $ROOT"

# ── Case A: a healthy machine says so, and exits 0 ───────────────────────────
# The toolchain stubs are deliberate: the CI shell job has no Go, and a case that
# only passes with Go installed would be red for the wrong reason there. Case C and
# the operator's own run cover the real tools.
REPO="$ROOT/repo"
mkdir -p "$REPO/bin" "$REPO/tools/cli" "$REPO/tools/relay" \
         "$REPO/packages/go-server" "$REPO/packages/spawn-profiles"
cp "$SCRIPT" "$REPO/bin/parlay-preflight"
: > "$REPO/bin/parlay"
for m in tools/cli tools/relay packages/go-server packages/spawn-profiles; do
  printf 'module example.invalid/%s\n\ngo 1.26.5\n' "$m" > "$REPO/$m/go.mod"
done
# make_tool <dir> <name> <what it prints> — a tool that is present and runs.
make_tool() { mkdir -p "$1"; printf '#!/bin/sh\necho "%s"\nexit 0\n' "$3" > "$1/$2"; chmod +x "$1/$2"; }
STUB_OK="$ROOT/stub-ok"
make_tool "$STUB_OK" go   "go version go1.26.5 darwin/arm64"
make_tool "$STUB_OK" git  "git version 2.55.0"
make_tool "$STUB_OK" curl "curl 8.7.1 (x86_64-apple-darwin)"
make_tool "$STUB_OK" bun  "1.3.14"
mkdir -p "$ROOT/home" "$ROOT/state" "$ROOT/relay"
A_PORT="$(free_port)"; A_ENGINE="$(free_port)"
out="$(env PATH="$STUB_OK:$PATH" HOME="$ROOT/home" PARLAY_STATE_HOME="$ROOT/state" \
        PARLAY_RELAY_RUNTIME="$ROOT/relay" PARLAY_SERVER="http://127.0.0.1:$A_PORT" \
        PARLAY_EVAL_ENGINE_URL="http://127.0.0.1:$A_ENGINE" GOTOOLCHAIN=auto \
        "$BASH_BIN" "$REPO/bin/parlay-preflight" 2>&1)"; rc=$?
[ "$VERBOSE" = 1 ] && printf '%s\n' "$out"
[ "$rc" = 0 ] && ok "A: healthy checkout exits 0" || bad "A: healthy checkout exits 0" "exit was $rc"
case "$out" in
  *", 0 failure(s)."*) ok "A: reports zero failures" ;;
  *) bad "A: reports zero failures" "$(printf '%s\n' "$out" | tail -2 | head -1)" ;;
esac
summary_matches_printed "$out" && ok "A: summary count matches printed FAIL lines" \
  || bad "A: summary count matches printed FAIL lines"
names_all "$out" "A" "Ready. Start the server" && ok "A: states what to do next" \
  || bad "A: states what to do next"

# ── Case A2: the version floor is a real comparison, not a label ─────────────
# GOTOOLCHAIN decides blocker vs warning, so both halves are asserted: local cannot
# fetch the newer toolchain (FAIL), auto can (WARN, and it does not block).
STUB_OLD="$ROOT/stub-old"
make_tool "$STUB_OLD" go "go version go1.19.4 darwin/arm64"
old_env() {
  env PATH="$STUB_OLD:$STUB_OK:$PATH" HOME="$ROOT/home" PARLAY_STATE_HOME="$ROOT/state" \
      PARLAY_RELAY_RUNTIME="$ROOT/relay" PARLAY_SERVER="http://127.0.0.1:$A_PORT" \
      GOTOOLCHAIN="$1" "$BASH_BIN" "$REPO/bin/parlay-preflight" 2>&1
}
out="$(old_env local)"; rc=$?
names_all "$out" "A2(local)" "FAIL  go" "1.19.4" "1.26.5" "unset GOTOOLCHAIN" \
  && ok "A2: go below the floor with GOTOOLCHAIN=local is a named FAIL with the toolchain remedy" \
  || bad "A2: go below the floor with GOTOOLCHAIN=local is a named FAIL with the toolchain remedy"
[ "$rc" = 1 ] && ok "A2: that case exits 1" || bad "A2: that case exits 1" "exit was $rc"
out="$(old_env auto)"; rc=$?
case "$out" in
  *"warn  go"*) ok "A2: the same Go with GOTOOLCHAIN=auto is a WARN, not a FAIL" ;;
  *) bad "A2: the same Go with GOTOOLCHAIN=auto is a WARN, not a FAIL" ;;
esac
[ "$rc" = 0 ] && ok "A2: and it does not block" || bad "A2: and it does not block" "exit was $rc"

# ── Case A3: a live parlay instance is reported up, and the guidance follows ─
# The health surface has two states and must tell the truth in both: a port that only accepts a connection is case B's FAIL; this one ANSWERS /health the way parlay does.
A3_PORT="$(free_port)"; mkdir -p "$ROOT/docroot"; printf '{"ok":true}' > "$ROOT/docroot/health"
( cd "$ROOT/docroot" && exec "$PY" -m http.server "$A3_PORT" --bind 127.0.0.1 ) >/dev/null 2>&1 &
LISTENER_PID=$!
for _ in $(seq 1 50); do (exec 3<>"/dev/tcp/127.0.0.1/$A3_PORT") 2>/dev/null && break; /bin/sleep 0.1; done
out="$(env PATH="$STUB_OK:$PATH" HOME="$ROOT/home" PARLAY_STATE_HOME="$ROOT/state" \
        PARLAY_RELAY_RUNTIME="$ROOT/relay" PARLAY_SERVER="http://127.0.0.1:$A3_PORT" \
        PARLAY_EVAL_ENGINE_URL="http://127.0.0.1:$A_ENGINE" "$BASH_BIN" "$REPO/bin/parlay-preflight" 2>&1)"; rc=$?
[ "$rc" = 0 ] && ok "A3: a parlay instance answering /health exits 0" || bad "A3: a parlay instance answering /health exits 0" "exit was $rc"
names_all "$out" "A3" "already answers parlay's /health" "answers on 127.0.0.1:$A3_PORT" "nothing to start" \
  && ok "A3: reports it up, and does not tell you to start one" \
  || bad "A3: reports it up, and does not tell you to start one"
kill "$LISTENER_PID" 2>/dev/null; LISTENER_PID=""

# ── Case B: many things broken at once, all named in ONE run ─────────────────
# go and git are PRESENT BUT UNUSABLE (stubs that exit non-zero) rather than absent:
# `command -v` finds them, so a preflight that only checks existence would call this
# machine healthy.
STUB="$ROOT/stub"; mkdir -p "$STUB"
printf '#!/bin/sh\necho "go: broken installation" >&2\nexit 1\n' > "$STUB/go"
printf '#!/bin/sh\necho "git: broken installation" >&2\nexit 1\n' > "$STUB/git"
chmod +x "$STUB/go" "$STUB/git"

mkdir -p "$ROOT/ro" "$ROOT/ro/home" && chmod 500 "$ROOT/ro" "$ROOT/ro/home"
B_PORT="$(free_port)"
"$PY" -c 'import socket,sys,time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen(4); time.sleep(120)' "$B_PORT" &
LISTENER_PID=$!
for _ in $(seq 1 50); do
  (exec 3<>"/dev/tcp/127.0.0.1/$B_PORT") 2>/dev/null && break
  /bin/sleep 0.1
done

out="$(env PATH="$STUB:/usr/bin:/bin" HOME="$ROOT/ro/home" \
        PARLAY_STATE_HOME="$ROOT/ro/state" PARLAY_RELAY_RUNTIME="$ROOT/ro/parlay" \
        PARLAY_SERVER="http://127.0.0.1:$B_PORT" \
        "$BASH_BIN" "$REPO/bin/parlay-preflight" 2>&1)"; rc=$?
printf '\n--- preflight against a deliberately broken environment ---\n%s\n---\n\n' "$out"
[ "$rc" = 1 ] && ok "B: exits 1 when blockers exist" || bad "B: exits 1 when blockers exist" "exit was $rc"
names_all "$out" "B" \
  "FAIL  go" "FAIL  git" "FAIL  HOME" "FAIL  CLI state" "FAIL  relay runtime" "FAIL  server port" \
  && ok "B: names ALL six blockers in one run" \
  || bad "B: names ALL six blockers in one run"
# Exactly six: a preflight that invents failures is as useless as one that
# misses them, and "everything is broken" is the easiest state to over-report.
B_FAILS=$(printf '%s\n' "$out" | grep -c '^  FAIL  ')
[ "$B_FAILS" = 6 ] && ok "B: reports exactly six failures, no invented ones" \
  || bad "B: reports exactly six failures, no invented ones" "printed $B_FAILS"
# An optional tool that is missing (bun) is a WARN, not a FAIL: the Quickstart
# works without the panel, and a de-facto blocker list is what sends a newcomer
# chasing a tool they never needed.
case "$out" in
  *"warn  bun"*) ok "B: a missing optional tool is WARN, not FAIL" ;;
  *) bad "B: a missing optional tool is WARN, not FAIL" ;;
esac
B_WARNS=$(printf '%s\n' "$out" | grep -c '^  warn  ')
[ "$B_WARNS" = 1 ] && ok "B: exactly one warning (bun) and it is excluded from the failure count" \
  || bad "B: exactly one warning (bun) and it is excluded from the failure count" "printed $B_WARNS"
summary_matches_printed "$out" && ok "B: summary count matches printed FAIL lines" \
  || bad "B: summary count matches printed FAIL lines"
every_fail_has_a_fix "$out" && ok "B: every FAIL carries its exact fix" \
  || bad "B: every FAIL carries its exact fix"

# ── Case C: the worst case — nothing on PATH at all, on the real checkout ────
# A newcomer on a bare machine. env -i with no HOME either: the preflight must still
# complete (it uses no external binary to do its own work) and still name everything.
REAL="$(cd "$HERE/.." && pwd)"
if [ -f "$REAL/bin/parlay-preflight" ]; then
  out="$(env -i PATH=/nonexistent "$BASH_BIN" "$REAL/bin/parlay-preflight" 2>&1)"; rc=$?
  printf '\n--- preflight on the real checkout with env -i PATH=/nonexistent ---\n%s\n---\n\n' "$out"
  [ "$rc" = 1 ] && ok "C: exits 1 with nothing installed" || bad "C: exits 1 with nothing installed" "exit was $rc"
  names_all "$out" "C" "FAIL  go" "FAIL  git" "FAIL  HOME" "warn  curl" "warn  bun" \
    && ok "C: survives a stripped environment and still reports everything" \
    || bad "C: survives a stripped environment and still reports everything"
  summary_matches_printed "$out" && ok "C: summary count matches printed FAIL lines" \
    || bad "C: summary count matches printed FAIL lines"
else
  ok "C: skipped — running from a copied script (no real checkout to point at)"
fi

printf '\n%s passed, %s failed.\n' "$pass" "$fail"
[ "$fail" = 0 ] || exit 1
exit 0
