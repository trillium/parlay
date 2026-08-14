#!/usr/bin/env bash
# Behavior tests for bin/parlay-treehouse-guard (robots-n8d9).
#
# The defect: `treehouse get --lease` reset a pool slot a LIVE agent was
# working in — it checked origin/main out over the agent's branch, because its
# eligibility rules see dirty / has-attributable-processes / already-leased and
# nothing else. The guard's job is to take a protective lease on every slot
# that is still occupied, so treehouse skips it.
#
# Everything here runs against a REAL fake pool: real git repos, a real
# origin, a real treehouse-state.json. The assertions read the state file the
# guard actually wrote, never the script's source.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$SELF_DIR/parlay-treehouse-guard"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq not installed" >&2; exit 0; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-th-guard.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT

git_q() { git -C "$1" -c user.email=test@example.com -c user.name=test "${@:2}"; }

# --- fixture: origin <- repo, with a pool of linked worktrees ---------------

ORIGIN="$ROOT/origin.git"
git init -q --bare -b main "$ORIGIN"

REPO="$ROOT/repo"
git init -q -b main "$REPO"
echo x > "$REPO/README"
git_q "$REPO" add -A >/dev/null
git_q "$REPO" commit -qm init
git_q "$REPO" remote add origin "$ORIGIN"
git_q "$REPO" push -q origin main
git_q "$REPO" fetch -q origin

POOL="$ROOT/pool"
mkdir -p "$POOL"
for n in free dirty unlanded live dead-owner foreign; do
  git_q "$REPO" worktree add -q --detach "$POOL/$n" origin/main
done

# dirty: an uncommitted file
echo scratch > "$POOL/dirty/wip.txt"

# unlanded: a committed branch that origin has never seen
git_q "$POOL/unlanded" checkout -q -b agent/local-only
echo work > "$POOL/unlanded/work.txt"
git_q "$POOL/unlanded" add -A >/dev/null
git_q "$POOL/unlanded" commit -qm "agent work"

# live / dead-owner: clean and landed, but claimed by a firstmate meta.
METAS="$ROOT/homes/one/state"
mkdir -p "$METAS"
cat > "$METAS/live.meta" <<EOF
window=default:wY:p81
backend=herdr
worktree=$POOL/live
EOF
cat > "$METAS/gone.meta" <<EOF
window=default:wZ:p99
backend=herdr
worktree=$POOL/dead-owner
EOF

# Stand-in for firstmate's own classifier: alive for the pane the live meta
# names, dead for the one whose agent has exited.
FMLIB="$ROOT/fm-backend.sh"
cat > "$FMLIB" <<'EOF'
fm_backend_agent_alive() {
  case "$2" in
    default:wY:p81) printf 'alive' ;;
    *) printf 'dead' ;;
  esac
}
EOF

STATE="$POOL/treehouse-state.json"
{
  printf '{"worktrees":['
  sep=""
  for n in free dirty unlanded live dead-owner foreign; do
    printf '%s{"name":"%s","path":"%s/%s","created_at":"2026-08-05T00:00:00-07:00"}' "$sep" "$n" "$POOL" "$n"
    sep=","
  done
  printf ']}'
} > "$STATE"

# `foreign` already carries somebody else's lease — the guard must not touch it.
jq '(.worktrees[] | select(.name=="foreign")) |= (.leased=true | .lease_id="abc" | .lease_holder="beadme")' \
  "$STATE" > "$STATE.tmp" && mv "$STATE.tmp" "$STATE"

run_guard() {
  PARLAY_TH_STATE="$STATE" \
  PARLAY_TH_META_ROOTS="$METAS" \
  PARLAY_TH_FM_BACKEND="$FMLIB" \
  PARLAY_TH_QUIET=1 \
    "$GUARD" "$REPO" 2>/dev/null
}

holder() { jq -r --arg n "$1" '.worktrees[] | select(.name==$n) | .lease_holder // ""' "$STATE"; }
leased() { jq -r --arg n "$1" '.worktrees[] | select(.name==$n) | (.leased // false) | tostring' "$STATE"; }

# --- 1. an occupied slot is leased out of the pool, with its reason ----------

run_guard

for spec in "dirty:dirty" "unlanded:unlanded" "live:live-agent"; do
  name=${spec%%:*}; want=${spec##*:}
  if [ "$(leased "$name")" = "true" ] && [ "$(holder "$name")" = "parlay-guard:$want" ]; then
    pass "$name slot is protected (parlay-guard:$want)"
  else
    fail "$name slot was left claimable: leased=$(leased "$name") holder=$(holder "$name"), want parlay-guard:$want"
  fi
done

# --- 2. a genuinely free slot stays in the pool ------------------------------

if [ "$(leased free)" = "false" ]; then
  pass "an idle, clean, landed slot stays available"
else
  fail "guard leased a free slot — that starves the pool"
fi

# A meta whose endpoint reads confidently dead is not an owner.
if [ "$(leased dead-owner)" = "false" ]; then
  pass "a slot whose recorded agent is dead stays available"
else
  fail "guard protected a slot whose agent has exited: holder=$(holder dead-owner)"
fi

# --- 3. somebody else's lease is never rewritten -----------------------------

if [ "$(holder foreign)" = "beadme" ]; then
  pass "an existing non-guard lease is left untouched"
else
  fail "guard overwrote another holder's lease: $(holder foreign)"
fi

# --- 4. protection is released once the work lands (self-healing) ------------

git_q "$POOL/unlanded" push -q origin agent/local-only
git_q "$POOL/unlanded" fetch -q origin
run_guard
if [ "$(leased unlanded)" = "false" ]; then
  pass "a guard lease is released once its reason lapses"
else
  fail "guard lease outlived its reason — the slot never returns to the pool"
fi

# The still-occupied ones must survive that same sweep.
if [ "$(leased dirty)" = "true" ] && [ "$(leased live)" = "true" ]; then
  pass "still-occupied slots stay protected across sweeps"
else
  fail "a sweep released a slot that is still occupied"
fi

# --- 5. --why reports the reason a slot cannot be reclaimed ------------------

why() {
  PARLAY_TH_META_ROOTS="$METAS" PARLAY_TH_FM_BACKEND="$FMLIB" PARLAY_TH_QUIET=1 \
    "$GUARD" --why "$1" 2>/dev/null
}
[ "$(why "$POOL/dirty")" = "dirty" ] \
  && pass "--why names the dirty tree" || fail "--why on a dirty slot said '$(why "$POOL/dirty")'"
[ "$(why "$POOL/live")" = "live-agent" ] \
  && pass "--why names the live agent" || fail "--why on a live slot said '$(why "$POOL/live")'"
[ -z "$(why "$POOL/free")" ] \
  && pass "--why is silent for a free slot" || fail "--why claimed a free slot is occupied: '$(why "$POOL/free")'"

# --- 6. the lease we write is one treehouse can actually parse ---------------
# treehouse reads leased_at with Go's time.RFC3339. A `-0700` offset (what
# `date +%z` emits) is not RFC3339: treehouse declares the whole state file
# corrupt and "recovers" by marking EVERY slot in the pool leased, which takes
# the entire pool out of service. Caught for real against the live binary.

stamp=$(jq -r '.worktrees[] | select(.name=="dirty") | .leased_at' "$STATE")
if [[ "$stamp" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$ ]]; then
  pass "leased_at is strict RFC3339 ($stamp)"
else
  fail "leased_at '$stamp' is not RFC3339 — treehouse will call the pool corrupt"
fi

# --- 7. an unclassifiable endpoint protects rather than reclaims -------------
# No liveness oracle available at all: the meta still names the slot, and the
# guard must fail safe. Checking out over a running agent is unrecoverable;
# holding a slot one sweep longer is not.

PARLAY_TH_STATE="$STATE" PARLAY_TH_META_ROOTS="$METAS" \
  PARLAY_TH_FM_BACKEND="$ROOT/does-not-exist.sh" PARLAY_TH_QUIET=1 \
  "$GUARD" "$REPO" 2>/dev/null
if [ "$(leased dead-owner)" = "true" ] && [ "$(holder dead-owner)" = "parlay-guard:live-agent" ]; then
  pass "an unclassifiable owner fails safe toward protecting the slot"
else
  fail "guard reclaimed a claimed slot it could not classify: leased=$(leased dead-owner)"
fi

exit "$FAILED"
