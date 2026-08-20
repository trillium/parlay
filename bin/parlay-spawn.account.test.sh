#!/usr/bin/env bash
# Behavior tests for parlay-spawn's --account / ccjuggler token injection.
#
# Four failure modes pinned:
#   1. resolve_account_token fails loud (exit non-zero, message) when both
#      keychain and flat-file miss — and does so BEFORE tab creation
#   2. keychain hit: CLAUDE_CODE_OAUTH_TOKEN injected via herdr tab --env
#   3. flat-file fallback: token injected when keychain misses
#   4. PARLAY_SPAWN_DEFAULT_ACCOUNT overridden by explicit --account flag
#
# Hermetic: stubs replace `security` and `herdr`; real jq is used (it's on
# PATH everywhere this runs). HOME is redirected. Nothing touches Pulse or
# the real keychain.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-spawn-account.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT

STUB="$ROOT/stubs"; mkdir -p "$STUB"

# curl: inert — every POST "succeeds" with empty body.
cat > "$STUB/curl" <<'S'
#!/usr/bin/env bash
exit 0
S

# identity: no-op (launch-spec registration, best-effort).
cat > "$STUB/identity" <<'S'
#!/usr/bin/env bash
exit 0
S

# herdr: the binary path (RPC socket disabled via HERDR_SOCK=/nonexistent).
#   - `agent get <name>`: returns '{}' so real jq resolves .result.agent.name
#     to empty — duplicate guard passes.
#   - `tab create`: records every --env KEY=VALUE into $RECORDED_ENV_FILE,
#     then returns JSON with tab_id but NO root_pane so spawn exits cleanly
#     right after env injection (we've already captured what we need).
#   - everything else: exit 0.
cat > "$STUB/herdr" <<'S'
#!/usr/bin/env bash
case "${1:-}" in
  agent)
    case "${2:-}" in
      get)   echo '{}'; exit 0 ;;
      start) echo '{}'; exit 1 ;;
      *)     exit 0 ;;
    esac ;;
  tab)
    case "${2:-}" in
      create)
        i=1
        while [ "$i" -le "$#" ]; do
          eval "_a=\${$i}"
          if [ "$_a" = "--env" ]; then
            i=$(( i + 1 ))
            eval "_v=\${$i}"
            printf '%s\n' "$_v" >> "${RECORDED_ENV_FILE:-/dev/null}"
          fi
          i=$(( i + 1 ))
        done
        # No root_pane → parlay-spawn prints error and exits 1 after this.
        echo '{"result":{"tab":{"tab_id":"t1"}}}'; exit 0
        ;;
      close) exit 0 ;;
    esac ;;
esac
echo '{}'
exit 0
S

# ccjuggler-resolve: replaces packages/ccjuggler's bin entry.
#   Controlled by STUB_SECURITY_TOKEN (simulates keychain) and the real
#   ~/.ccjuggler/<account>/.oauth-token flat-file path under the test HOME.
cat > "$STUB/ccjuggler-resolve" <<'S'
#!/usr/bin/env bash
account="${1:-}"
if [ -z "$account" ]; then echo "Usage: ccjuggler-resolve <account>" >&2; exit 2; fi
# Simulate keychain hit
if [ -n "${STUB_SECURITY_TOKEN:-}" ]; then
  printf '%s' "$STUB_SECURITY_TOKEN"
  exit 0
fi
# Flat-file fallback
f="$HOME/.ccjuggler/${account}/.oauth-token"
if [ -f "$f" ]; then cat "$f"; exit 0; fi
echo "ccjuggler: no token found for account '${account}' — tried keychain 'ccjuggler-${account}' and $f" >&2
exit 1
S

chmod +x "$STUB"/*

# Helper: run parlay-spawn with the stub PATH and a redirected HOME.
# All extra env vars must be exported by the caller before invoking this.
run_spawn() {
  local home_dir="$1"; shift
  ( export HOME="$home_dir"
    export PATH="$STUB:$PATH"
    export PARLAY_SERVER="http://127.0.0.1:1"
    export PARLAY_SPAWN_SKIP_CONTRACT=1
    export PARLAY_SPAWN_NO_WATCHDOG=1
    export HERDR_SOCK=/nonexistent
    # STUB_SECURITY_TOKEN, RECORDED_ENV_FILE, PARLAY_SPAWN_DEFAULT_ACCOUNT
    # are inherited from the caller's exported env.
    "$SPAWN" "$@" 2>&1 )
}

# ── 1. Missing token → loud exit, message printed, no tab created ─────────────
HOME1="$ROOT/home1"; mkdir -p "$HOME1"
export STUB_SECURITY_TOKEN=""
out=$(run_spawn "$HOME1" bad-acct "Bad Acct" "#aabbcc" "task" --account ghost-acc)
if grep -q "no token found" <<<"$out"; then
  pass "missing token: 'no token found' message printed"
else
  fail "missing token: expected 'no token found' (from ccjuggler-resolve); got:"$'\n'"$out"
fi
# herdr tab create must NOT be called — env file stays empty.
# ("launching detached" is logged before token resolution, so that line is
# not a reliable signal; checking the env file is the authoritative test.)
ENV1="$ROOT/env-missing.txt"; > "$ENV1"
export STUB_SECURITY_TOKEN=""
export RECORDED_ENV_FILE="$ENV1"
run_spawn "$HOME1" bad-acct3 "Bad Acct" "#aabbcc" "task" --account ghost-acc >/dev/null 2>/dev/null || true
unset STUB_SECURITY_TOKEN RECORDED_ENV_FILE
if [ -s "$ENV1" ]; then
  fail "missing token: herdr tab create was still called (env file non-empty)"
else
  pass "missing token: spawn exits before herdr tab create"
fi
if run_spawn "$HOME1" bad-acct2 "Bad Acct" "#aabbcc" "task" --account ghost-acc >/dev/null 2>&1; then
  fail "missing token: parlay-spawn exited 0 (should be non-zero)"
else
  pass "missing token: parlay-spawn exits non-zero"
fi

# ── 2. Keychain hit → CLAUDE_CODE_OAUTH_TOKEN in herdr tab --env ─────────────
HOME2="$ROOT/home2"; mkdir -p "$HOME2"
ENV2="$ROOT/env-keychain.txt"; > "$ENV2"
export STUB_SECURITY_TOKEN="tok-from-keychain"
export RECORDED_ENV_FILE="$ENV2"
run_spawn "$HOME2" kc-acct "KC Acct" "#aabbcc" "task" --account kc-acc >/dev/null
unset STUB_SECURITY_TOKEN RECORDED_ENV_FILE
if grep -qF "CLAUDE_CODE_OAUTH_TOKEN=tok-from-keychain" "$ENV2"; then
  pass "keychain hit: CLAUDE_CODE_OAUTH_TOKEN injected via herdr tab --env"
else
  fail "keychain hit: token not in herdr --env args; recorded:"$'\n'"$(cat "$ENV2")"
fi

# ── 3. Flat-file fallback → token in herdr tab --env ─────────────────────────
HOME3="$ROOT/home3"; mkdir -p "$HOME3"
mkdir -p "$HOME3/.ccjuggler/file-acc"
printf 'tok-from-file' > "$HOME3/.ccjuggler/file-acc/.oauth-token"
ENV3="$ROOT/env-flatfile.txt"; > "$ENV3"
export STUB_SECURITY_TOKEN=""          # keychain misses → falls to file
export RECORDED_ENV_FILE="$ENV3"
run_spawn "$HOME3" ff-acct "FF Acct" "#aabbcc" "task" --account file-acc >/dev/null
unset STUB_SECURITY_TOKEN RECORDED_ENV_FILE
if grep -qF "CLAUDE_CODE_OAUTH_TOKEN=tok-from-file" "$ENV3"; then
  pass "flat-file fallback: CLAUDE_CODE_OAUTH_TOKEN injected via herdr tab --env"
else
  fail "flat-file fallback: token not in herdr --env args; recorded:"$'\n'"$(cat "$ENV3")"
fi

# ── 4. PARLAY_SPAWN_DEFAULT_ACCOUNT overridden by explicit --account ──────────
HOME4="$ROOT/home4"; mkdir -p "$HOME4"
mkdir -p "$HOME4/.ccjuggler/default-acc"
printf 'default-tok' > "$HOME4/.ccjuggler/default-acc/.oauth-token"
mkdir -p "$HOME4/.ccjuggler/explicit-acc"
printf 'explicit-tok' > "$HOME4/.ccjuggler/explicit-acc/.oauth-token"
ENV4="$ROOT/env-override.txt"; > "$ENV4"
export STUB_SECURITY_TOKEN=""
export RECORDED_ENV_FILE="$ENV4"
export PARLAY_SPAWN_DEFAULT_ACCOUNT="default-acc"
run_spawn "$HOME4" ov-acct "Override" "#aabbcc" "task" --account explicit-acc >/dev/null
unset STUB_SECURITY_TOKEN RECORDED_ENV_FILE PARLAY_SPAWN_DEFAULT_ACCOUNT
if grep -qF "CLAUDE_CODE_OAUTH_TOKEN=explicit-tok" "$ENV4"; then
  pass "--account flag overrides PARLAY_SPAWN_DEFAULT_ACCOUNT"
else
  fail "--account flag did not override default account; recorded:"$'\n'"$(cat "$ENV4")"
fi
if grep -qF "CLAUDE_CODE_OAUTH_TOKEN=default-tok" "$ENV4"; then
  fail "default account token leaked despite --account override"
fi

exit "$FAILED"
