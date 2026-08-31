#!/bin/bash
# Regression harness for tools/hooks/post-commit and tools/hooks/post-merge.
#
#   tools/hooks/hooks.test.sh
#
# Every hook here is invoked as `/bin/bash <hook>` on purpose, NOT through its
# `#!/usr/bin/env bash` shebang: macOS /bin/bash is 3.2, where `cd ""` SUCCEEDS,
# and bash 5 errors. The empty-common-dir fail-open these hooks guard against
# only reproduces on 3.2, so letting the shebang pick up a newer bash would
# silently stop testing for it.
#
# The real delivery path is never reached. packages/client/build.ts POSTs to a
# live parlay chat server on 127.0.0.1:4242 and force-reloads whatever panels are
# connected, so three independent barriers keep it out of this harness:
#   1. $HOME is redirected into the scratch dir AND "$HOME/.bun/bin" is prepended
#      to PATH, so BOTH arms of the hooks' `command -v bun || $HOME/.bun/bin/bun`
#      resolution land on the scratch dir, never the machine's real bun.
#   2. That path holds a stub recording "<cwd>|<argv>" and exiting 0 — which is
#      also how "did the hook reach delivery?" is asserted positively.
#   3. The scratch repo has packages/client/src/ but no build.ts at all, so even
#      an escaped real bun would have nothing to execute.
# PARLAY_HOOK_LOG points the hooks' log at the scratch dir for every case but the
# last, so the log assertions do not depend on the macOS-shaped default path (this
# harness also runs on Linux in CI); case 7 unsets it to cover that default.
# No assertion greps hook source: each one reads an exit code, stdout/stderr, or
# what the hook actually wrote to (or refused to create on) disk.
#
# WHAT A GREEN RUN DOES AND DOES NOT CERTIFY. It certifies the hooks' own control
# flow — the announced opt-in skip, primary-checkout resolution, the refusal, and
# what gets logged — against a stub bun in a redirected $HOME. It is NOT evidence
# that the delivery works: the real build and the POST to the live Pulse server never
# execute here, so nothing in this file has ever observed a panel receive a bundle.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
POST_COMMIT="$REPO_ROOT/tools/hooks/post-commit"
POST_MERGE="$REPO_ROOT/tools/hooks/post-merge"
# `type -P` so this is always the git ON DISK: case 4's stub re-execs it by path,
# and a name resolved through PATH (or a shell function) would re-enter the stub.
REAL_GIT="$(type -P git)"
[[ -x "$REAL_GIT" ]] || { echo "no git on PATH" >&2; exit 2; }

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ok   $1"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL $1"; }
eq() { if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1 — want [$3], got [$2]"; fi; }
has_line() { if grep -q "$2" "$3" 2>/dev/null; then ok "$1"; else bad "$1 — no /$2/ in $3"; fi; }
no_line() { if grep -q "$2" "$3" 2>/dev/null; then bad "$1 — /$2/ found in $3"; else ok "$1"; fi; }
absent() { if [[ -e "$2" ]]; then bad "$1 — $2 exists"; else ok "$1"; fi; }

SCRATCH="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/parlay-hooks-test.XXXXXX")" && pwd -P)"
trap 'rm -rf "$SCRATCH"' EXIT

export HOME="$SCRATCH/home"
export GIT_AUTHOR_NAME=hooks-test GIT_AUTHOR_EMAIL=hooks@test
export GIT_COMMITTER_NAME=hooks-test GIT_COMMITTER_EMAIL=hooks@test
unset PARLAY_MAIN_CHECKOUT
mkdir -p "$HOME/.bun/bin"

BUN_CALLS="$HOME/bun-calls.log"
cat > "$HOME/.bun/bin/bun" <<'STUB'
#!/bin/bash
echo "$PWD|$*" >> "$HOME/bun-calls.log"
exit 0
STUB
chmod +x "$HOME/.bun/bin/bun"
# Barrier 1: the hooks prefer a bun on PATH and fall back to $HOME/.bun/bin/bun,
# so the stub has to win BOTH arms or a real bun could reach the real build.ts.
export PATH="$HOME/.bun/bin:$PATH"

# A git whose --git-common-dir probe answers nothing, used by the two cases that
# exercise an unresolvable primary checkout (1c opted out, 4 opted in). Prepended
# to PATH only inside those cases.
mkdir -p "$SCRATCH/bin"
cat > "$SCRATCH/bin/git" <<STUB
#!/bin/bash
for a in "\$@"; do [[ "\$a" == "--git-common-dir" ]] && exit 0; done
exec "$REAL_GIT" "\$@"
STUB
chmod +x "$SCRATCH/bin/git"

LOG_DIR="$SCRATCH/logs"
LOG="$LOG_DIR/bundle-rebuild.out.log"
export PARLAY_HOOK_LOG="$LOG"
DEFAULT_LOG="$HOME/Library/Logs/parlay/bundle-rebuild.out.log"
bun_calls() { [[ -f "$BUN_CALLS" ]] && wc -l < "$BUN_CALLS" | tr -d ' ' || echo 0; }
log_lines() { [[ -f "$LOG" ]] && wc -l < "$LOG" | tr -d ' ' || echo 0; }

RC=0
run_hook() { # <hook> <cwd>
  ( cd "$2" && /bin/bash "$1" ) > "$SCRATCH/out" 2> "$SCRATCH/err"
  RC=$?
}

MAIN="$SCRATCH/main"
mkdir -p "$MAIN/packages/client/src"
git -C "$MAIN" init -q
git -C "$MAIN" symbolic-ref HEAD refs/heads/main
echo seed > "$MAIN/README.md"
echo 'export const a = 1' > "$MAIN/packages/client/src/a.ts"
git -C "$MAIN" add -A --force
git -C "$MAIN" commit -qm seed

echo "hooks regression harness — $(/bin/bash --version | head -1)"

# ---- 1a. opted out, a commit that WOULD have delivered: announces the skip ---
# The gate sits AFTER the worktree guard and the changed-paths test, so it only
# ever speaks for a commit that delivery would otherwise have acted on. That is
# the whole point of the announcement: a contributor whose clone silently stopped
# rebuilding the panel gets told, in full, how to turn it on.
echo "1a. opted out, a change delivery would have acted on"
git -C "$MAIN" checkout -q -b optout-src
echo 'export const z = 26' > "$MAIN/packages/client/src/z.ts"
git -C "$MAIN" add -A --force
git -C "$MAIN" commit -qm "client change on branch"
git -C "$MAIN" checkout -q main
git -C "$MAIN" merge -q --no-ff -m "merge optout-src" optout-src
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  run_hook "$hook" "$MAIN"
  eq "$name exits 0" "$RC" "0"
  eq "$name writes no stderr" "$(cat "$SCRATCH/err")" ""
  has_line "$name announces the skip on stdout" "delivery skipped" "$SCRATCH/out"
  has_line "$name prints the whole enable command" "git config --bool parlay.autobuild true" "$SCRATCH/out"
done
eq "opted out never invokes the build" "$(bun_calls)" "0"
no_line "opted out logs no delivery" "deliver" "$LOG"
absent "opted out creates no log directory" "$LOG_DIR"

# ---- 1b. opted out, an irrelevant commit: completely silent -----------------
# The announcement must not become noise on every commit in the repo, so a change
# outside the panel's build inputs says nothing at all on either stream.
echo "1b. opted out, a change delivery would have ignored"
git -C "$MAIN" checkout -q -b optout-docs
echo doc >> "$MAIN/README.md"
git -C "$MAIN" add -A --force
git -C "$MAIN" commit -qm "docs only"
git -C "$MAIN" checkout -q main
git -C "$MAIN" merge -q --no-ff -m "merge optout-docs" optout-docs
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  run_hook "$hook" "$MAIN"
  eq "$name exits 0 on an irrelevant change" "$RC" "0"
  eq "$name writes no stdout on an irrelevant change" "$(cat "$SCRATCH/out")" ""
  eq "$name writes no stderr on an irrelevant change" "$(cat "$SCRATCH/err")" ""
done
eq "an irrelevant change never invokes the build" "$(bun_calls)" "0"

# ---- 1c. opted out + unresolvable primary checkout: silent, exits 0 ---------
# Case 4 proves an opted-IN clone refuses loudly here. A clone that never asked
# for delivery must not inherit that failure: it exits 0 saying nothing, so the
# feature it declined can never break its commits.
echo "1c. opted out, unresolvable primary checkout"
before="$(bun_calls)"
before_log="$(log_lines)"
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  ( cd "$MAIN" && PATH="$SCRATCH/bin:$PATH" /bin/bash "$hook" ) > "$SCRATCH/out" 2> "$SCRATCH/err"
  RC=$?
  eq "$name exits 0 when opted out and the checkout is unresolvable" "$RC" "0"
  eq "$name writes no stdout when opted out and unresolvable" "$(cat "$SCRATCH/out")" ""
  eq "$name writes no stderr when opted out and unresolvable" "$(cat "$SCRATCH/err")" ""
done
eq "opted out + unresolvable never delivers" "$(bun_calls)" "$before"
eq "opted out + unresolvable logs nothing" "$(log_lines)" "$before_log"

# ---- 1d. opted out, commit in a linked worktree: writes nothing at all ------
# The worktree guard has its own diagnostic (case 3 asserts it), but that is a
# delivery-path diagnostic: a clone that never enabled delivery must not have a
# log directory conjured under it, nor a line appended, by a feature it declined.
echo "1d. opted out, commit in a linked worktree"
WT_OUT="$SCRATCH/wt-optout"
git -C "$MAIN" worktree add -q -b optout-wt "$WT_OUT" > /dev/null 2>&1
echo 'export const y = 25' > "$WT_OUT/packages/client/src/y.ts"
git -C "$WT_OUT" add -A --force
git -C "$WT_OUT" commit -qm "client change in an opted-out worktree"
before="$(bun_calls)"
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  run_hook "$hook" "$WT_OUT"
  eq "$name exits 0 in an opted-out linked worktree" "$RC" "0"
  eq "$name writes no stdout in an opted-out linked worktree" "$(cat "$SCRATCH/out")" ""
  eq "$name writes no stderr in an opted-out linked worktree" "$(cat "$SCRATCH/err")" ""
done
eq "an opted-out linked worktree never delivers" "$(bun_calls)" "$before"
absent "an opted-out linked worktree creates no log directory" "$LOG_DIR"
absent "an opted-out linked worktree writes no log file" "$LOG"

# ---- 2. opted in, primary checkout: proceeds past the guard ---------------
echo "2. opted in, primary checkout"
git -C "$MAIN" config --bool parlay.autobuild true
echo 'export const b = 2' > "$MAIN/packages/client/src/b.ts"
git -C "$MAIN" add -A --force
git -C "$MAIN" commit -qm "client change"
run_hook "$POST_COMMIT" "$MAIN"
eq "post-commit exits 0" "$RC" "0"
has_line "post-commit logs the delivery" "delivering" "$LOG"
eq "post-commit reached delivery once" "$(bun_calls)" "1"
eq "post-commit built from the primary checkout" "$(tail -1 "$BUN_CALLS")" "$MAIN/packages/client|build.ts"

git -C "$MAIN" checkout -q -b merge-src
echo 'export const c = 3' > "$MAIN/packages/client/src/c.ts"
git -C "$MAIN" add -A --force
git -C "$MAIN" commit -qm "client change on branch"
git -C "$MAIN" checkout -q main
git -C "$MAIN" merge -q --no-ff -m "merge" merge-src
run_hook "$POST_MERGE" "$MAIN"
eq "post-merge exits 0" "$RC" "0"
eq "post-merge reached delivery once" "$(bun_calls)" "2"
eq "post-merge built from the primary checkout" "$(tail -1 "$BUN_CALLS")" "$MAIN/packages/client|build.ts"

# ---- 3. opted in, linked worktree: logs a skip, delivers nothing ----------
echo "3. opted in, linked worktree"
WT="$SCRATCH/wt"
git -C "$MAIN" worktree add -q -b feature "$WT" > /dev/null 2>&1
echo 'export const d = 4' > "$WT/packages/client/src/d.ts"
git -C "$WT" add -A --force
git -C "$WT" commit -qm "client change in worktree"
before="$(bun_calls)"
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  run_hook "$hook" "$WT"
  eq "$name exits 0 in a linked worktree" "$RC" "0"
  has_line "$name logs the skip" "skip: $WT is not the main checkout" "$LOG"
done
eq "a linked worktree never delivers" "$(bun_calls)" "$before"

# ---- 4. unresolvable primary checkout: refuses loudly, does not guess -----
# The round-2 regression, and the single most important case in this file: a git
# whose --git-common-dir probe answers nothing, with PARLAY_MAIN_CHECKOUT unset.
# Under bash 3.2 an empty path handed to `cd` succeeds, so a hook that resolved
# it would silently deliver from whatever tree is committing.
echo "4. unresolvable primary checkout"
before="$(bun_calls)"
before_log="$(log_lines)"
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  ( cd "$MAIN" && PATH="$SCRATCH/bin:$PATH" /bin/bash "$hook" ) > "$SCRATCH/out" 2> "$SCRATCH/err"
  RC=$?
  eq "$name exits non-zero" "$RC" "1"
  eq "$name says nothing on stdout" "$(cat "$SCRATCH/out")" ""
  eq "$name gives one line of reason" "$(wc -l < "$SCRATCH/err" | tr -d ' ')" "1"
  has_line "$name names the refusal" "refusing to deliver: cannot resolve the primary checkout" "$SCRATCH/err"
done
eq "an unresolvable checkout never delivers" "$(bun_calls)" "$before"
eq "an unresolvable checkout logs nothing" "$(log_lines)" "$before_log"

# ---- 5. PARLAY_MAIN_CHECKOUT set and readable: it names the delivery target ----
# The override replaces the git-derived primary checkout, so the tree it names is
# where the build runs — here the linked worktree, which case 3 proved skips
# without it — and any OTHER checkout is then the one that skips.
echo "5. PARLAY_MAIN_CHECKOUT set and readable"
before="$(bun_calls)"
( cd "$WT" && PARLAY_MAIN_CHECKOUT="$WT" /bin/bash "$POST_COMMIT" ) > "$SCRATCH/out" 2> "$SCRATCH/err"
RC=$?
eq "post-commit exits 0 under a readable override" "$RC" "0"
eq "the override's tree delivers once" "$(bun_calls)" "$((before + 1))"
eq "post-commit built from the override, not the git-derived primary" "$(tail -1 "$BUN_CALLS")" "$WT/packages/client|build.ts"

before="$(bun_calls)"
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  ( cd "$MAIN" && PARLAY_MAIN_CHECKOUT="$WT" /bin/bash "$hook" ) > "$SCRATCH/out" 2> "$SCRATCH/err"
  RC=$?
  eq "$name exits 0 in a checkout the override does not name" "$RC" "0"
  has_line "$name logs the skip against the override" "skip: $MAIN is not the main checkout" "$LOG"
done
eq "a checkout the override does not name never delivers" "$(bun_calls)" "$before"

# ---- 6. PARLAY_MAIN_CHECKOUT set but unreadable: same refusal as case 4 -------
# An override that cannot be resolved is a mistake, not a fallback: the hook must
# refuse rather than fall back to deriving the checkout from git.
echo "6. PARLAY_MAIN_CHECKOUT set but unreadable"
before="$(bun_calls)"
before_log="$(log_lines)"
for hook in "$POST_COMMIT" "$POST_MERGE"; do
  name="$(basename "$hook")"
  ( cd "$MAIN" && PARLAY_MAIN_CHECKOUT="$SCRATCH/no-such-tree" /bin/bash "$hook" ) > "$SCRATCH/out" 2> "$SCRATCH/err"
  RC=$?
  eq "$name exits non-zero on an unreadable override" "$RC" "1"
  eq "$name says nothing on stdout on an unreadable override" "$(cat "$SCRATCH/out")" ""
  eq "$name gives one line of reason on an unreadable override" "$(wc -l < "$SCRATCH/err" | tr -d ' ')" "1"
  has_line "$name names the refusal on an unreadable override" "refusing to deliver: cannot resolve the primary checkout" "$SCRATCH/err"
done
eq "an unreadable override never delivers" "$(bun_calls)" "$before"
eq "an unreadable override logs nothing" "$(log_lines)" "$before_log"

# ---- 7. PARLAY_HOOK_LOG unset: the default log path still resolves ----------
# Every case above points the log at the scratch dir through the override, so
# this is the one that exercises the coded default under the redirected $HOME.
echo "7. PARLAY_HOOK_LOG unset"
before="$(bun_calls)"
echo 'export const e = 5' > "$MAIN/packages/client/src/e.ts"
git -C "$MAIN" add -A --force
git -C "$MAIN" commit -qm "client change logged to the default path"
( cd "$MAIN" && unset PARLAY_HOOK_LOG && /bin/bash "$POST_COMMIT" ) > "$SCRATCH/out" 2> "$SCRATCH/err"
RC=$?
eq "post-commit exits 0 with no log override" "$RC" "0"
eq "the default log path still delivers" "$(bun_calls)" "$((before + 1))"
has_line "post-commit logs the delivery to the default path" "delivering" "$DEFAULT_LOG"

echo
echo "$PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]]
