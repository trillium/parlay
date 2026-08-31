#!/usr/bin/env bash
# Behavior tests for the model-required gate (task-qyu8q): parlay-spawn must
# refuse to launch when no model was chosen deliberately — no implicit
# "claude session default", no silent sonnet fallback. Satisfied by an
# explicit --model or a --profile whose profiles.toml entry names a model.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-spawn-model.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT

# Isolated HOME with no config.toml — beads_required stays off, so failures
# below are attributable to the model gate and nothing else.
HOME_DIR="$ROOT/home"
mkdir -p "$HOME_DIR"

# Inert herdr: the model gate must refuse before this is ever consulted, but
# it needs to exist on PATH for the script to get past its own preflight.
STUB_DIR="$ROOT/stub"
mkdir -p "$STUB_DIR"
cat > "$STUB_DIR/herdr" <<'STUB'
#!/usr/bin/env bash
echo '{}'
exit 0
STUB
chmod +x "$STUB_DIR/herdr"

# A profiles.toml fixture independent of the real repo catalog, so this test
# does not drift if packages/spawn-profiles/profiles.toml changes shape.
PROFILES_TOML="$ROOT/profiles.toml"
cat > "$PROFILES_TOML" <<'EOF'
[[profile]]
name = "has-model"
kind = "claude"
model = "sonnet"

[[profile]]
name = "no-model"
kind = "claude"
EOF

run_spawn() {
  HOME="$HOME_DIR" PATH="$STUB_DIR:$PATH" PARLAY_SERVER="http://127.0.0.1:1" \
    PARLAY_SPAWN_PROFILES_TOML="$PROFILES_TOML" \
    PARLAY_SPAWN_SKIP_CONTRACT=1 PARLAY_SPAWN_NO_WATCHDOG=1 \
    "$SPAWN" "$@" 2>&1
}

# ── 1. Named-form refusal without --model or --profile ──────────────────────
out=$(run_spawn no-model-a "No Model" "#aabbcc" "task")
status=$?
if [ "$status" -eq 2 ]; then
  pass "named form: no model → exit 2"
else
  fail "named form: no model → expected exit 2, got $status; output:"$'\n'"$out"
fi
if grep -qF "refusing to spawn — no model was chosen" <<<"$out"; then
  pass "named form: no model → refusal message printed"
else
  fail "named form: no model → refusal message missing; output:"$'\n'"$out"
fi

# ── 2. --ephemeral refusal without --model or --profile ─────────────────────
out=$(run_spawn --ephemeral "task")
status=$?
if [ "$status" -eq 2 ]; then
  pass "--ephemeral: no model → exit 2"
else
  fail "--ephemeral: no model → expected exit 2, got $status; output:"$'\n'"$out"
fi
if grep -qF "refusing to spawn — no model was chosen" <<<"$out"; then
  pass "--ephemeral: no model → refusal message printed"
else
  fail "--ephemeral: no model → refusal message missing; output:"$'\n'"$out"
fi

# ── 3. Batch refusal: neither shared --model nor --profile ──────────────────
out=$(run_spawn no-model-b1=/tmp/none-b1 --prompt "brief")
status=$?
[ "$status" -ne 0 ] || fail "batch: no model → expected non-zero exit"
if grep -qF "batch: FAILED to spawn no-model-b1 (/tmp/none-b1)" <<<"$out"; then
  pass "batch: no model → per-pair failure reported"
else
  fail "batch: no model → per-pair failure not reported; output:"$'\n'"$out"
fi
if grep -qF "refusing to spawn — no model was chosen" <<<"$out"; then
  pass "batch: no model → refusal message surfaced from the re-exec'd child"
else
  fail "batch: no model → refusal message missing; output:"$'\n'"$out"
fi

# ── 4. --profile naming a profile with no model still refuses ───────────────
out=$(run_spawn no-model-c "No Model" "#aabbcc" "task" --profile no-model)
status=$?
if [ "$status" -eq 2 ]; then
  pass "--profile with no model field → exit 2"
else
  fail "--profile with no model field → expected exit 2, got $status; output:"$'\n'"$out"
fi
if grep -qF "refusing to spawn — no model was chosen" <<<"$out"; then
  pass "--profile with no model field → refusal message printed"
else
  fail "--profile with no model field → refusal message missing; output:"$'\n'"$out"
fi

# ── 5. Unknown --profile name refuses with a specific error, not the generic one ─
out=$(run_spawn no-model-d "No Model" "#aabbcc" "task" --profile does-not-exist)
status=$?
if [ "$status" -eq 2 ]; then
  pass "--profile unknown name → exit 2"
else
  fail "--profile unknown name → expected exit 2, got $status; output:"$'\n'"$out"
fi
if grep -qF "not found in $PROFILES_TOML" <<<"$out"; then
  pass "--profile unknown name → specific not-found error printed"
else
  fail "--profile unknown name → expected not-found error; output:"$'\n'"$out"
fi

# ── 6. Explicit --model satisfies the gate (spawn proceeds past it) ─────────
out=$(run_spawn has-model-a "Has Model" "#aabbcc" "task" --model sonnet)
if grep -qF "refusing to spawn — no model was chosen" <<<"$out"; then
  fail "--model sonnet was refused by the model gate; output:"$'\n'"$out"
else
  pass "--model sonnet satisfies the gate (no refusal)"
fi
# It should get far enough to attempt registration against the dead server —
# proof the gate did not short-circuit before the rest of the pipeline.
if grep -qF "registering agent 'has-model-a'" <<<"$out"; then
  pass "--model sonnet: spawn proceeded past the gate to registration"
else
  fail "--model sonnet: spawn did not reach registration; output:"$'\n'"$out"
fi

# ── 7. A model-bearing --profile satisfies the gate ──────────────────────────
out=$(run_spawn has-model-b "Has Model" "#aabbcc" "task" --profile has-model)
if grep -qF "refusing to spawn — no model was chosen" <<<"$out"; then
  fail "--profile has-model was refused by the model gate; output:"$'\n'"$out"
else
  pass "--profile has-model satisfies the gate (no refusal)"
fi
if grep -qF "using model sonnet" <<<"$out"; then
  pass "--profile has-model: resolved model sonnet reported"
else
  fail "--profile has-model: resolved-model message missing; output:"$'\n'"$out"
fi
if grep -qF "registering agent 'has-model-b'" <<<"$out"; then
  pass "--profile has-model: spawn proceeded past the gate to registration"
else
  fail "--profile has-model: spawn did not reach registration; output:"$'\n'"$out"
fi

# ── 8. An explicit --kind survives a profile that also names a kind ─────────
# has-model's profile kind is "claude"; pin --kind opencode explicitly and
# confirm the profile does not silently override it.
out=$(run_spawn has-model-c "Has Model" "#aabbcc" "task" --kind opencode --profile has-model)
if grep -qF "using model sonnet (kind=opencode)" <<<"$out"; then
  pass "explicit --kind opencode is not overridden by profile's kind=claude"
else
  fail "explicit --kind was overridden by --profile's kind; output:"$'\n'"$out"
fi

# ── 9. Batch: a shared --profile satisfies the gate for every child ─────────
out=$(run_spawn has-model-d1=/tmp/none-d1 has-model-d2=/tmp/none-d2 --prompt "brief" --profile has-model)
if grep -qF "refusing to spawn — no model was chosen" <<<"$out"; then
  fail "batch --profile has-model was refused by the model gate; output:"$'\n'"$out"
else
  pass "batch --profile has-model satisfies the gate for every child"
fi

exit "$FAILED"
