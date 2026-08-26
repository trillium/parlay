#!/usr/bin/env bash
# Behavior tests for pii_route_model's model selection (robots-pd98).
#
# The defect: pii_route_model hard-coded MODEL="opencode-go/ox-alpha-free",
# documented in parlay-spawn's help as "free, limited-time". The time lapsed.
# From then on every default --no-pii spawn was pinned to a model the provider
# no longer serves.
#
# What makes that worse than a wrong default: the failure lands at the opencode
# layer AFTER parlay-spawn has registered the agent and returned 0. The fleet
# ends up holding a registered agent that cannot answer, with no error anywhere
# upstream — the robots-dcag "registered but not working" shape.
#
# There were no tests for this file at all, which is why a dead model name could
# ship and sit. The point of these is not to pin one model name (that is what
# rotted); it is to pin the RULE: never route to a model the local opencode does
# not currently list, and never abort the spawn while checking.
#
# Every case stubs `opencode` on PATH, so nothing here talks to a provider.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="$SELF_DIR/parlay-pii-lib.sh"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

STUB_DIR="$(mktemp -d)"
trap 'rm -rf "$STUB_DIR"' EXIT

# TEST_PATH is what each case runs under. The default prepends the stub dir to
# the caller's PATH, which is fine for every case that INSTALLS a stub.
#
# The "opencode is not installed" case cannot use it: deleting the stub just
# falls through to whatever real opencode is on the developer's PATH, and on
# the captain's box there is one. That case ran green on CI (no opencode there)
# and red locally, which is the wrong way round — a test must not depend on
# whether the machine happens to have the tool it is pretending is missing.
# So that case runs under a minimal PATH, and asserts the absence rather than
# assuming it.
TEST_PATH="$STUB_DIR:$PATH"
MINIMAL_PATH="$STUB_DIR:/usr/bin:/bin"

# stub_opencode <mode> [lines...]
#   list    — print the given model lines, exit 0
#   empty   — print nothing, exit 0 (installed, but no models)
#   fail    — print nothing, exit 1 (installed, but the call errored)
#   absent  — remove the stub entirely
stub_opencode() {
  local mode="$1"; shift
  if [ "$mode" = absent ]; then
    rm -f "$STUB_DIR/opencode"
    return 0
  fi
  {
    printf '#!/usr/bin/env bash\n'
    case "$mode" in
      list)  printf 'printf "%%s\\n" %s\n' "$(printf '%q ' "$@")" ;;
      empty) printf 'exit 0\n' ;;
      fail)  printf 'exit 1\n' ;;
    esac
  } > "$STUB_DIR/opencode"
  chmod +x "$STUB_DIR/opencode"
}

# route <expected-kind> <expected-model> <label>
# Runs pii_route_model in a fresh subshell with PII=0 and defaults, under
# `set -euo pipefail` — the mode parlay-spawn itself runs in, and the mode in
# which a carelessly written probe aborts the whole spawn.
route() {
  local want_kind="$1" want_model="$2" label="$3" out
  out="$(
    set -euo pipefail
    PATH="$TEST_PATH"
    # shellcheck source=/dev/null
    . "$LIB"
    PII=0; KIND="claude"; MODEL=""
    pii_route_model 2>/dev/null
    printf '%s\t%s' "$KIND" "$MODEL"
  )" || { fail "$label — pii_route_model exited non-zero (it must never abort the spawn)"; return; }
  if [ "$out" = "$(printf '%s\t%s' "$want_kind" "$want_model")" ]; then
    pass "$label"
  else
    fail "$label — got [$(printf '%s' "$out" | tr '\t' '|')], want [$want_kind|$want_model]"
  fi
}

# --- the happy path: pick the most-preferred model that is actually offered ---
stub_opencode list \
  opencode/hy3-free \
  opencode/nemotron-3.5-lightning-free \
  opencode-go/deepseek-v4-pro
route opencode opencode/nemotron-3.5-lightning-free \
  "picks the most-preferred free model when several are offered"

# Preference must beat list order — the provider's ordering is not our ranking.
stub_opencode list \
  opencode/nemotron-3-ultra-free \
  opencode/mimo-v2.5-free
route opencode opencode/mimo-v2.5-free \
  "preference order wins over the order opencode happens to print"

# --- the actual robots-pd98 bug: the pinned model is gone ---------------------
# The live list here is exactly what the captain's box returns, minus any
# ox-alpha entry. The old code pinned ox-alpha-free regardless.
stub_opencode list \
  opencode/big-pickle \
  opencode/hy3-free \
  opencode/mimo-v2.5-free \
  opencode/muse-spark-1.2-contributor-free \
  opencode/nemotron-3-ultra-free \
  opencode/nemotron-3.5-lightning-free \
  opencode-go/deepseek-v4-pro
route opencode opencode/nemotron-3.5-lightning-free \
  "routes to a live model against the real post-retirement list"

# --- never route to a model that is not listed --------------------------------
# The one property that had to hold and did not. Whatever gets picked, it must
# appear in the list the provider just gave us.
picked="$(
  set -euo pipefail
  PATH="$TEST_PATH"
  # shellcheck source=/dev/null
  . "$LIB"
  PII=0; KIND="claude"; MODEL=""
  pii_route_model 2>/dev/null
  printf '%s' "$MODEL"
)" || picked="<aborted>"
if printf '%s\n' opencode/big-pickle opencode/hy3-free opencode/mimo-v2.5-free \
     opencode/muse-spark-1.2-contributor-free opencode/nemotron-3-ultra-free \
     opencode/nemotron-3.5-lightning-free | grep -qxF "$picked"; then
  pass "the routed model is a member of the list opencode returned"
else
  fail "routed to '$picked', which opencode did not list — this is robots-pd98 itself"
fi

# --- every preferred model has retired ---------------------------------------
# ox-alpha-free will not be the last name to lapse. A free model we did not
# anticipate still beats falling back to a paid one.
stub_opencode list opencode/some-model-invented-later opencode-go/deepseek-v4-pro
route opencode opencode/some-model-invented-later \
  "falls back to an unanticipated free model when every preferred name is gone"

# --- paid-only: opencode/ prefix is the free tier, opencode-go/ is not --------
# Routing a --no-pii spawn to a subscription model would silently spend the
# budget the flag exists to protect.
stub_opencode list opencode-go/deepseek-v4-pro opencode-go/qwen3.8-max
route claude "" \
  "stays on claude when only paid opencode-go models are offered"

# --- the three unknowable states all stay on claude ---------------------------
# "Could not determine" must never be read as "no free models exist", and must
# never be read as "route anyway and hope".
stub_opencode empty
route claude "" "stays on claude when opencode lists nothing"

stub_opencode fail
route claude "" "stays on claude when 'opencode models' exits non-zero"

stub_opencode absent
TEST_PATH="$MINIMAL_PATH"
if PATH="$TEST_PATH" command -v opencode >/dev/null 2>&1; then
  fail "an opencode is still reachable under the minimal PATH — the not-installed case proves nothing"
else
  pass "the minimal PATH really has no opencode (case has teeth)"
fi
route claude "" "stays on claude when opencode is not installed"
TEST_PATH="$STUB_DIR:$PATH"

# --- pii_live_free_models must be safe to call OUTSIDE a `||` context ---------
# This case exists because a mutation that should have been caught was not.
#
# Rewriting the probe as a bare `_list=$(opencode models …)` — the exact shape
# AGENTS.md forbids, where under `set -euo pipefail` the assignment takes the
# substitution's exit status — did not fail any test above. The reason is subtle
# and worth stating: bash disables `set -e` inside any command that is an
# operand of `||`, and every call above reaches the probe through
# `_live="$(pii_live_free_models)" || _live=""`. The caller's guard was
# masking the callee's defect.
#
# That makes the safety accidental: it holds only as long as every future caller
# remembers the `||`. So this calls the function where `set -e` is genuinely
# live. A failing `opencode` must yield empty output and a survivable exit, not
# an aborted spawn.
#
# Note how the status is captured below. Writing it the obvious way —
#
#     probe_out="$( … )" && probe_ok=1 || probe_ok=0
#
# silently defeats this test, and did on the first attempt: the `&&`/`||`
# suppression is CONTAGIOUS into the command substitution, so the inner
# `set -e` stopped applying and the mutated probe reported success. The outer
# script deliberately runs without `set -e`, so a bare assignment followed by
# `$?` is both safe here and the only spelling that actually observes the
# subshell's exit status.
stub_opencode fail
probe_out="$(
  set -euo pipefail
  PATH="$TEST_PATH"
  # shellcheck source=/dev/null
  . "$LIB"
  pii_live_free_models
  printf 'SURVIVED'
)"
probe_rc=$?
probe_ok=0
[ "$probe_rc" -eq 0 ] && probe_ok=1
if [ "$probe_ok" = 1 ] && [ "$probe_out" = "SURVIVED" ]; then
  pass "pii_live_free_models survives a failing 'opencode models' with set -e live"
else
  fail "pii_live_free_models aborted under set -e when opencode failed (out=[$probe_out]) — a best-effort probe written as a bare VAR=\$(cmd) is not best-effort"
fi

# --- explicit --kind/--model are still respected ------------------------------
stub_opencode list opencode/nemotron-3.5-lightning-free
out="$(
  set -euo pipefail
  PATH="$TEST_PATH"
  # shellcheck source=/dev/null
  . "$LIB"
  PII=0; KIND="claude"; MODEL="opus"
  pii_route_model 2>/dev/null
  printf '%s\t%s' "$KIND" "$MODEL"
)" || out="<aborted>"
[ "$out" = "$(printf 'claude\topus')" ] \
  && pass "an explicitly pinned --model is not overridden" \
  || fail "an explicitly pinned --model was overridden: [$out]"

out="$(
  set -euo pipefail
  PATH="$TEST_PATH"
  # shellcheck source=/dev/null
  . "$LIB"
  PII=0; KIND="openrouter"; MODEL=""
  pii_route_model 2>/dev/null
  printf '%s\t%s' "$KIND" "$MODEL"
)" || out="<aborted>"
[ "$out" = "$(printf 'openrouter\t')" ] \
  && pass "an explicitly pinned --kind is not overridden" \
  || fail "an explicitly pinned --kind was overridden: [$out]"

# --- PII=1 must never be routed to a third-party model ------------------------
# The security half of this file. pii_route_model is guarded by PII=0, and this
# asserts the guard rather than trusting the read.
out="$(
  set -euo pipefail
  PATH="$TEST_PATH"
  # shellcheck source=/dev/null
  . "$LIB"
  # shellcheck disable=SC2034  # PII is read by pii_route_model in the sourced lib
  PII=1; KIND="claude"; MODEL=""
  pii_route_model 2>/dev/null
  printf '%s\t%s' "$KIND" "$MODEL"
)" || out="<aborted>"
[ "$out" = "$(printf 'claude\t')" ] \
  && pass "a PII task is never routed to a free third-party model" \
  || fail "a PII task was routed away from claude: [$out]"

# --- regression floor: the pre-fix code really was broken ---------------------
# A test suite that passes against both the old and new spelling would prove
# nothing. Reproduce the old body against the real post-retirement list and
# assert it picks a model that is not there.
stub_opencode list opencode/nemotron-3.5-lightning-free opencode-go/deepseek-v4-pro
old_model="opencode-go/ox-alpha-free"
if PATH="$TEST_PATH" opencode models | grep -qxF "$old_model"; then
  fail "the stub still offers $old_model — the regression floor proves nothing"
else
  pass "pre-fix hard-coded $old_model is absent from the live list (test has teeth)"
fi

printf '%s\n' "--- $( [ "$FAILED" -eq 0 ] && echo 'all passed' || echo 'FAILURES' ) ---"
exit "$FAILED"
