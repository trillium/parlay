#!/usr/bin/env bash
# Behavior tests for parlay-spawn's --worktree repo resolution (robots-d04t).
#
# The defect: `treehouse get --lease` has no --repo flag — it resolves its pool
# from the PROCESS cwd. parlay-spawn ran it with the CALLER's shell cwd
# inherited, so `parlay-spawn ... --cwd ~/code/herdr-web --worktree` fired from
# inside a firstmate worktree leased a *firstmate* worktree and launched the
# agent in the wrong repo, silently.
#
# Hermetic harness: `curl`, `herdr`, `jq`-adjacent side effects and $HOME are
# all redirected, so the run reaches the worktree block (step 2c) and then dies
# harmlessly at `herdr tab create`. Nothing touches a live Pulse or a real tab.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-spawn-worktree.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT

mkrepo() {
  local d="$1"
  mkdir -p "$d"
  git -C "$d" init -q -b main
  git -C "$d" config user.email test@example.com
  git -C "$d" config user.name test
  echo x > "$d/README"
  git -C "$d" add -A
  git -C "$d" commit -qm init
}

TARGET="$ROOT/target"; mkrepo "$TARGET"
OTHER="$ROOT/other";   mkrepo "$OTHER"

# A pre-made pool worktree per repo, named after its repo — the treehouse stub
# hands back the one matching ITS OWN cwd, exactly like the real tool.
mkdir -p "$ROOT/pool"
git -C "$TARGET" worktree add -q --detach "$ROOT/pool/target"
git -C "$OTHER"  worktree add -q --detach "$ROOT/pool/other"

STUB="$ROOT/stubs"; mkdir -p "$STUB"

cat > "$STUB/curl" <<'S'
#!/usr/bin/env bash
# Inert curl: every parlay-spawn POST "succeeds" with an empty body.
exit 0
S

cat > "$STUB/herdr" <<'S'
#!/usr/bin/env bash
# Inert herdr: `agent get` reports "no such agent"; `tab create` returns junk so
# the run dies right after the worktree block instead of creating a real tab.
echo '{}'
exit 0
S

cat > "$STUB/treehouse" <<S
#!/usr/bin/env bash
# Pool-per-repo stub keyed on the PROCESS cwd, like the real treehouse.
case "\$1" in
  get)    echo "$ROOT/pool/\$(basename "\$(git rev-parse --show-toplevel 2>/dev/null || echo unknown)")" ;;
  return) : ;;
esac
exit 0
S

# Ignores cwd entirely and always hands back a worktree of the WRONG repo.
cat > "$STUB/treehouse-liar" <<S
#!/usr/bin/env bash
case "\$1" in
  get)    echo "$ROOT/pool/other" ;;
  return) : ;;
esac
exit 0
S

chmod +x "$STUB"/*

run_spawn() {
  # Runs from \$OTHER — the caller-in-the-wrong-repo shape from the repro.
  ( cd "$OTHER" \
      && HOME="$ROOT/home" PATH="$STUB:$PATH" PARLAY_SERVER="http://127.0.0.1:1" \
         PARLAY_SPAWN_SKIP_CONTRACT=1 PARLAY_SPAWN_NO_WATCHDOG=1 \
         "$SPAWN" "$@" 2>&1 )
}

# ── 1. treehouse is resolved against --cwd, not the caller's shell cwd ───────
mkdir -p "$ROOT/home"
out=$(run_spawn wt-ok "WT OK" "#ffffff" "task" --cwd "$TARGET" --worktree)
if grep -qF "worktree via treehouse at $ROOT/pool/target" <<<"$out"; then
  pass "treehouse runs with cwd=\$PROJECT_PATH (leases the --cwd repo's worktree)"
else
  fail "treehouse resolved against the caller's cwd, not --cwd; got:"$'\n'"$out"
fi
if grep -qF "$ROOT/pool/other" <<<"$out"; then
  fail "leased a worktree of the CALLER's repo — robots-d04t regression"$'\n'"$out"
fi

# ── 2. a wrong-repo worktree is rejected loudly, never launched into ─────────
rm -rf "$ROOT/home"; mkdir -p "$ROOT/home"
mv "$STUB/treehouse" "$STUB/treehouse-honest"
mv "$STUB/treehouse-liar" "$STUB/treehouse"
out=$(run_spawn wt-liar "WT Liar" "#ffffff" "task" --cwd "$TARGET" --worktree)
mv "$STUB/treehouse" "$STUB/treehouse-liar"
mv "$STUB/treehouse-honest" "$STUB/treehouse"
if grep -q "WRONG-REPO WORKTREE" <<<"$out"; then
  pass "a wrong-repo treehouse path is rejected loudly"
else
  fail "wrong-repo treehouse path was accepted silently; got:"$'\n'"$out"
fi
# parlay-spawn prints the symlink-resolved project path (/private/var on macOS).
TARGET_REAL="$(cd "$TARGET" && pwd -P)"
if grep -qE "worktree created at ($TARGET|$TARGET_REAL)/\.worktrees/parlay-wt-liar" <<<"$out"; then
  pass "falls back to a plain git worktree of the --cwd repo"
else
  fail "no correct-repo fallback after rejecting the treehouse path; got:"$'\n'"$out"
fi

# ── 3. repo_identity agrees across worktrees and separates unrelated repos ───
eval "$(sed -n '/^repo_identity() {$/,/^}$/p' "$SPAWN")"
if [ "$(repo_identity "$TARGET")" = "$(repo_identity "$ROOT/pool/target")" ]; then
  pass "repo_identity: a repo and its own worktree match"
else
  fail "repo_identity: a repo and its own worktree disagree"
fi
if [ "$(repo_identity "$TARGET")" != "$(repo_identity "$OTHER")" ]; then
  pass "repo_identity: unrelated repos differ"
else
  fail "repo_identity: unrelated repos collide"
fi
if repo_identity "$ROOT" >/dev/null 2>&1; then
  fail "repo_identity: should fail outside a git repo"
else
  pass "repo_identity: fails outside a git repo"
fi

# ── 4. --help documents the keychain lookup the code actually performs ───────
help=$("$SPAWN" 2>&1 || true)
if grep -qF "security find-generic-password -a ccjuggler -s ccjuggler-NAME" <<<"$help"; then
  pass "--help keychain flags match resolve_account_token"
else
  fail "--help keychain flags are transposed relative to the code"
fi

exit "$FAILED"
