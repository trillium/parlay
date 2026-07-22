#!/usr/bin/env bash
# Behavior tests for parlay-spawn batch dispatch (`id=repo` pairs).
#
# The parlay port of firstmate's tests/fm-spawn-batch.test.sh. These exercise
# argument routing only. Each spawn attempt is made hermetic two ways:
#   * PARLAY_SERVER points at a dead port, so the very first side effect (the
#     register-agent POST) fails fast and the pair exits non-zero BEFORE any
#     herdr tab/session is created — no windows, no worktrees, no live agents.
#   * `herdr` is stubbed to a no-op on PATH, so the duplicate-name guard never
#     touches a real daemon.
# The only behavior asserted on its own is "a multi-pair batch does not stop
# after the first failure"; detection and rejection cases are table-driven.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

# ── Hermetic harness ────────────────────────────────────────────────────────
# A throwaway PATH shim whose `herdr` is an inert no-op, plus a dead server so
# register-agent fails fast. Real curl/jq/bash are still found on the base PATH.
STUB_DIR="$(mktemp -d "${TMPDIR:-/tmp}/parlay-spawn-batch.XXXXXX")"
trap 'rm -rf "$STUB_DIR"' EXIT
cat > "$STUB_DIR/herdr" <<'STUB'
#!/usr/bin/env bash
# Inert herdr for tests: the duplicate-name guard calls `herdr agent get <id>`
# and reads `.result.agent.name`; returning `{}` reports "no such agent".
echo '{}'
exit 0
STUB
chmod +x "$STUB_DIR/herdr"

run_spawn() {
  PATH="$STUB_DIR:$PATH" PARLAY_SERVER="http://127.0.0.1:1" "$SPAWN" "$@" 2>&1
}

# Every pair in a batch is dispatched even though each one fails at the (dead)
# register step; the loop must not stop early. This is the load-bearing batch
# guarantee, kept explicit.
test_batch_dispatches_every_pair() {
  local out status
  out=$(run_spawn nope-batch-a-z1=/tmp/none-a nope-batch-b-z2=/tmp/none-b --prompt "brief")
  status=$?
  [ "$status" -ne 0 ] || fail "batch with failing spawns should exit non-zero"
  printf '%s\n' "$out" | grep -F 'batch: FAILED to spawn nope-batch-a-z1 (/tmp/none-a)' >/dev/null \
    || fail "first pair was not dispatched/reported"
  printf '%s\n' "$out" | grep -F 'batch: FAILED to spawn nope-batch-b-z2 (/tmp/none-b)' >/dev/null \
    || fail "second pair was not dispatched/reported (loop stopped early?)"
  pass "batch dispatch re-execs and reports every id=repo pair"
}

# Boundary cases for batch detection. Each row:
#   <label>|<batch yes/no>|<expect substring>|<args>
# batch=yes -> a 'batch:' line must appear; batch=no -> it must not.
test_batch_mode_boundaries() {
  local label batch expect args out status
  while IFS='|' read -r label batch expect args; do
    [ -n "$label" ] || continue
    # shellcheck disable=SC2086  # args is an intentional word-split arg list
    out=$(run_spawn $args)
    status=$?
    [ "$status" -ne 0 ] || fail "$label: expected non-zero exit"
    if [ -n "$expect" ]; then
      printf '%s\n' "$out" | grep -F "$expect" >/dev/null || fail "$label: missing '$expect'"
    fi
    case "$batch" in
      yes) printf '%s\n' "$out" | grep -F 'batch:' >/dev/null || fail "$label: did not enter batch dispatch" ;;
      no)  printf '%s\n' "$out" | grep -F 'batch:' >/dev/null && fail "$label: wrongly entered batch dispatch" ;;
    esac
  done <<'ROWS'
single id=repo pair routes through batch|yes|batch: FAILED to spawn nope-batch-solo-z3 (/tmp/none-solo)|nope-batch-solo-z3=/tmp/none-solo --prompt brief
non-pair arg in batch is rejected|yes|batch dispatch expects every argument as id=repo; got 'bogus-no-equals'|nope-batch-mix-z5=/tmp/none-mix bogus-no-equals --prompt brief
plain '<id> <repo>' is single-task|no||nope-single-z4 projects-none-single
id part containing '/' is not a pair|no||weird/id-z6=projects/none projects/none
ROWS
  pass "batch detection: single pair batches, non-pair rejected, single-task and slash-id stay single"
}

# A batch with no shared --prompt is rejected before any pair is dispatched:
# there is no per-id brief file, so the shared brief is mandatory.
test_batch_requires_prompt() {
  local out status
  out=$(run_spawn nope-batch-noprompt-z9=/tmp/none-np)
  status=$?
  [ "$status" -eq 2 ] || fail "missing --prompt should exit 2 (got $status)"
  printf '%s\n' "$out" | grep -F 'batch dispatch requires a shared --prompt' >/dev/null \
    || fail "missing --prompt error not reported"
  printf '%s\n' "$out" | grep -F 'batch:' >/dev/null \
    && fail "missing --prompt should fail before dispatching any pair"
  pass "batch requires a shared --prompt and rejects before dispatch"
}

test_batch_dispatches_every_pair
test_batch_mode_boundaries
test_batch_requires_prompt

if [ "$FAILED" -eq 0 ]; then
  echo "ALL PASS"
else
  echo "SOME TESTS FAILED" >&2
fi
exit "$FAILED"
