#!/usr/bin/env bash
# Behavior tests for `parlay-spawn --list` (task-qyu8q, scope 2): renders the
# profiles.toml catalog (name/kind/model/account), joins in quota-axi headroom
# when available, and degrades cleanly (never fails the listing) when
# quota-axi is absent or errors.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"
export PARLAY_SPAWN_VIA_CLI=1  # task-qyu8q scope 3: this harness IS the sanctioned direct caller

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-spawn-list.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT

HOME_DIR="$ROOT/home"
mkdir -p "$HOME_DIR"

PROFILES_TOML="$ROOT/profiles.toml"
cat > "$PROFILES_TOML" <<'EOF'
[[profile]]
name = "has-model"
kind = "claude"
model = "sonnet"

[[profile]]
name = "no-model"
kind = "claude"

[[profile]]
name = "opencode-profile"
kind = "opencode"
model = "opencode-go/deepseek-v4-pro"
EOF

STUB_DIR="$ROOT/stub"
mkdir -p "$STUB_DIR"

# A PATH with everything real EXCEPT quota-axi, for the quota-axi-absent case
# below. Dropping every PATH entry that itself provides a quota-axi executable
# (there can be more than one, e.g. a homebrew shim AND ~/.local/bin) can also
# drop that dir's python3 (needed for tomllib) — so a python3 shim goes FIRST,
# ahead of the filtered remainder, to guarantee a tomllib-capable python3
# survives the filter regardless of which directory hosts it upstream.
PYSHIM_DIR="$ROOT/pyshim"
mkdir -p "$PYSHIM_DIR"
ln -sf "$(command -v python3)" "$PYSHIM_DIR/python3"
PATH_NO_QUOTA_AXI="$PYSHIM_DIR:$(
  printf '%s' "$PATH" | tr ':' '\n' | while IFS= read -r dir; do
    [ -x "$dir/quota-axi" ] && continue
    printf '%s\n' "$dir"
  done | tr '\n' ':'
)"

run_list() {
  HOME="$HOME_DIR" PATH="$STUB_DIR:$PATH" \
    PARLAY_SPAWN_PROFILES_TOML="$PROFILES_TOML" \
    "$SPAWN" --list 2>&1
}

# ── 1. Renders every profile from the fixture: name, kind, model ────────────
out=$(run_list)
status=$?
[ "$status" -eq 0 ] || fail "--list: expected exit 0, got $status; output:"$'\n'"$out"
for needle in "has-model" "claude" "sonnet" "no-model" "opencode-profile" "opencode" "opencode-go/deepseek-v4-pro"; do
  if grep -qF "$needle" <<<"$out"; then
    pass "--list: output contains '$needle'"
  else
    fail "--list: output missing '$needle'; got:"$'\n'"$out"
  fi
done

# ── 2. A model-less profile is called out, not left blank ───────────────────
if grep -qF "cannot satisfy the model gate" <<<"$out"; then
  pass "--list: model-less profile is flagged, not silently blank"
else
  fail "--list: model-less profile row not flagged; got:"$'\n'"$out"
fi

# ── 3. quota-axi absent from PATH → degrades to the static catalog ──────────
out_noquota=$(HOME="$HOME_DIR" PATH="$PATH_NO_QUOTA_AXI" \
  PARLAY_SPAWN_PROFILES_TOML="$PROFILES_TOML" "$SPAWN" --list 2>&1)
status=$?
if [ "$status" -eq 0 ]; then
  pass "--list: quota-axi absent → still exits 0"
else
  fail "--list: quota-axi absent → expected exit 0, got $status; output:"$'\n'"$out_noquota"
fi
if grep -qF "quota-axi not found on PATH" <<<"$out_noquota"; then
  pass "--list: quota-axi absent → degrade note printed"
else
  fail "--list: quota-axi absent → expected degrade note; got:"$'\n'"$out_noquota"
fi
if grep -qF "has-model" <<<"$out_noquota"; then
  pass "--list: quota-axi absent → static catalog still rendered"
else
  fail "--list: quota-axi absent → catalog missing; got:"$'\n'"$out_noquota"
fi

# ── 4. quota-axi present but erroring → degrades cleanly, never fails ───────
cat > "$STUB_DIR/quota-axi" <<'STUB'
#!/usr/bin/env bash
echo "boom" >&2
exit 1
STUB
chmod +x "$STUB_DIR/quota-axi"
out=$(run_list)
status=$?
if [ "$status" -eq 0 ]; then
  pass "--list: quota-axi errors → still exits 0"
else
  fail "--list: quota-axi errors → expected exit 0, got $status; output:"$'\n'"$out"
fi
if grep -qF "quota-axi errored" <<<"$out"; then
  pass "--list: quota-axi errors → degrade note printed"
else
  fail "--list: quota-axi errors → expected degrade note; got:"$'\n'"$out"
fi

# ── 5. quota-axi present and healthy → headroom is joined in ────────────────
cat > "$STUB_DIR/quota-axi" <<'STUB'
#!/usr/bin/env bash
cat <<'JSON'
{
  "providers": [
    {"provider": "claude", "windows": [{"kind": "weekly", "label": "week", "percentRemaining": 5, "resetsAt": "2026-08-31T19:00:00Z"}]}
  ]
}
JSON
STUB
chmod +x "$STUB_DIR/quota-axi"
out=$(run_list)
if grep -qF "week 5% remaining" <<<"$out"; then
  pass "--list: live quota-axi headroom joined onto the matching profile row"
else
  fail "--list: expected joined quota headroom; got:"$'\n'"$out"
fi
if grep -qF "resets 19:00" <<<"$out"; then
  pass "--list: quota reset time rendered"
else
  fail "--list: expected reset time; got:"$'\n'"$out"
fi
# opencode-profile has no matching provider in this fixture's quota JSON — its
# row must render plainly, not error or drop the row.
if grep -qF "opencode-profile" <<<"$out"; then
  pass "--list: unmatched-provider profile still renders"
else
  fail "--list: unmatched-provider profile row disappeared; got:"$'\n'"$out"
fi

# ── 6. Malformed profiles.toml degrades cleanly, no raw traceback ───────────
BAD_TOML="$ROOT/bad-profiles.toml"
printf 'this is [ not valid toml' > "$BAD_TOML"
out=$(HOME="$HOME_DIR" PATH="$STUB_DIR:$PATH" \
  PARLAY_SPAWN_PROFILES_TOML="$BAD_TOML" "$SPAWN" --list 2>&1)
status=$?
if [ "$status" -eq 2 ]; then
  pass "--list: malformed profiles.toml → exit 2 (not a raw traceback exit)"
else
  fail "--list: malformed profiles.toml → expected exit 2, got $status; output:"$'\n'"$out"
fi
if grep -qF "not valid TOML" <<<"$out"; then
  pass "--list: malformed profiles.toml → clean error message printed"
else
  fail "--list: malformed profiles.toml → expected clean error message; got:"$'\n'"$out"
fi
if grep -qF "Traceback" <<<"$out"; then
  fail "--list: malformed profiles.toml → raw Python traceback leaked to output"
else
  pass "--list: malformed profiles.toml → no raw traceback"
fi

# ── 7. The mandatory-model refusal error points at --list ───────────────────
out=$(HOME="$HOME_DIR" PATH="$STUB_DIR:$PATH" PARLAY_SERVER="http://127.0.0.1:1" \
  PARLAY_SPAWN_PROFILES_TOML="$PROFILES_TOML" \
  PARLAY_SPAWN_SKIP_CONTRACT=1 PARLAY_SPAWN_NO_WATCHDOG=1 \
  "$SPAWN" no-model "No Model" "#aabbcc" "task" 2>&1)
if grep -qF "parlay-spawn --list" <<<"$out"; then
  pass "refusal error names 'parlay-spawn --list'"
else
  fail "refusal error does not mention --list; got:"$'\n'"$out"
fi

exit "$FAILED"
