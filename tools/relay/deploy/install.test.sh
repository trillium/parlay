#!/usr/bin/env bash
# Behavior tests for install.sh's upstream-server guard (robots-93xu).
#
# The defect: install.sh defaulted SERVER from an AMBIENT $PARLAY_SERVER. The
# LaunchAgent it installs is a fixed singleton serving the CANONICAL runtime dir,
# and that dir is reserved for the default server — so an install run from any
# shell that happened to export a non-default $PARLAY_SERVER (a go-server dev
# shell on :4242, say) permanently rebound the production relay. Every agent on
# the default server was then refused enrollment, fleet-wide, with no other
# symptom. That is exactly what happened on the captain's box.
#
# Hermetic: every case either exits at the guard (before install.sh touches
# anything at all) or is stopped immediately after it by a `uname` stub that
# reports a non-macOS host. Nothing is built, copied, rendered, or bootstrapped —
# no case can reach launchctl or the real LaunchAgent.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL="$SELF_DIR/install.sh"
DEFAULT_SERVER="http://localhost:31337"
OTHER_SERVER="http://localhost:4242"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; [ -n "${2:-}" ] && printf '      %s\n' "$2" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/pi.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT
STUB="$ROOT/stubs"
mkdir -p "$STUB"

# uname: reports a non-macOS host, so any run that gets PAST the server guard
# stops at install.sh's own Darwin check instead of installing anything. This is
# how "the guard allowed it through" is asserted without side effects.
cat > "$STUB/uname" <<'S'
#!/usr/bin/env bash
[ "${1:-}" = "-s" ] && { echo Linux; exit 0; }
exec /usr/bin/uname "$@"
S
chmod +x "$STUB/uname"

# run [ENV_SERVER] -- [args...]  → sets RC and $ROOT/out
run() {
  local env_server="$1"; shift
  [ "${1:-}" = "--" ] && shift
  RC=0
  if [ -n "$env_server" ]; then
    env -i PATH="$STUB:/usr/bin:/bin:/usr/sbin:/sbin" HOME="$ROOT/home" \
      PARLAY_SERVER="$env_server" \
      /bin/bash "$INSTALL" "$@" >"$ROOT/out" 2>&1 || RC=$?
  else
    env -i PATH="$STUB:/usr/bin:/bin:/usr/sbin:/sbin" HOME="$ROOT/home" \
      /bin/bash "$INSTALL" "$@" >"$ROOT/out" 2>&1 || RC=$?
  fi
}

# past_guard asserts the run was NOT stopped by the server guard. It must have
# reached the Darwin check, which the uname stub fails.
past_guard() {
  local what="$1"
  if grep -q "refusing to install" "$ROOT/out"; then
    fail "$what: blocked by the server guard" "$(cat "$ROOT/out")"
  elif ! grep -q "macOS-only" "$ROOT/out"; then
    fail "$what: did not reach the Darwin check" "rc=$RC $(cat "$ROOT/out")"
  else
    pass "$what"
  fi
}

# ── 1. THE DEFECT: an ambient $PARLAY_SERVER can no longer rebind the relay ────
run "$OTHER_SERVER" --
if [ "$RC" -ne 2 ]; then
  fail "ambient non-default server: exit $RC, want 2" "$(cat "$ROOT/out")"
elif ! grep -q "refusing to install" "$ROOT/out"; then
  fail "ambient non-default server: was accepted" "$(cat "$ROOT/out")"
elif ! grep -q "ambient" "$ROOT/out"; then
  fail "ambient non-default server: message does not say it came from the env" "$(cat "$ROOT/out")"
elif ! grep -q -- "--allow-non-default-server" "$ROOT/out"; then
  fail "ambient non-default server: message does not name the override" "$(cat "$ROOT/out")"
else
  pass "an ambient \$PARLAY_SERVER is refused, and the message blames the env"
fi

# ── 2. An explicit --server is refused too, but is not blamed on the env ───────
run "" -- --server "$OTHER_SERVER"
if [ "$RC" -ne 2 ]; then
  fail "explicit non-default --server: exit $RC, want 2" "$(cat "$ROOT/out")"
elif grep -q "ambient" "$ROOT/out"; then
  fail "explicit non-default --server: wrongly blamed on the environment" "$(cat "$ROOT/out")"
else
  pass "an explicit non-default --server is refused without blaming the env"
fi

# ── 3. --server WINS over an ambient env var, both ways ───────────────────────
# An explicit default beats a non-default env var...
run "$OTHER_SERVER" -- --server "$DEFAULT_SERVER"
past_guard "explicit --server overrides a non-default ambient \$PARLAY_SERVER"
# ...and is not itself mistaken for an env-sourced value.
run "$DEFAULT_SERVER" -- --server "$OTHER_SERVER"
if [ "$RC" -eq 2 ] && ! grep -q "ambient" "$ROOT/out"; then
  pass "a non-default --server is refused on its own terms, not the env's"
else
  fail "explicit --server was attributed to the environment" "rc=$RC $(cat "$ROOT/out")"
fi

# ── 4. The default server is untouched by the guard ───────────────────────────
run "" --
past_guard "the default server installs as before (no env, no flag)"
run "$DEFAULT_SERVER" --
past_guard "an ambient \$PARLAY_SERVER equal to the default is fine"

# ── 5. A trailing slash is the same server, not a different one ───────────────
run "${DEFAULT_SERVER}/" --
past_guard "a trailing slash on the default server is normalized, not refused"

# ── 6. The override is a real escape hatch ───────────────────────────────────
run "" -- --server "$OTHER_SERVER" --allow-non-default-server
past_guard "--allow-non-default-server lets a deliberate rebind through"

# ── 7. Unknown flags are still rejected ──────────────────────────────────────
run "" -- --bogus
if [ "$RC" -ne 2 ] || ! grep -q "unknown arg" "$ROOT/out"; then
  fail "unknown flag: exit $RC" "$(cat "$ROOT/out")"
else
  pass "unknown flag exits 2"
fi

if [ "$FAILED" -ne 0 ]; then
  echo "install.test.sh: FAILED" >&2
  exit 1
fi
echo "install.test.sh: all tests passed"
