#!/usr/bin/env bash
# Behavior tests for install.sh's frontend-assets pipeline (T-02).
#
# What is pinned here:
#   1. Copy-only is the DEFAULT: with no --build, whatever dist/ each package
#      already holds is copied verbatim, and bun is never invoked.
#   2. The installed layout matches what cmd/parlay-server/main.go serves:
#      client dist at the assets root (mux "/"), webview dist under
#      <assets-dir>/fleet (mux "/fleet/" joins "fleet" onto -assets-dir), and
#      the rendered plist's -assets-dir names that same root.
#   3. A missing dist/ is a WARNING, never a failure — a binary-only install
#      must still succeed on a checkout that has never run a frontend build.
#   4. --build runs both packages' `bun run build` first, with the client's
#      live-reload ping disabled (PARLAY_RELOAD_TARGET=off) so an install
#      build can never reload live panels against a half-copied bundle.
#
# Hermetic like tools/relay/deploy/install.test.sh, but this script's asserted
# behavior lies PAST the platform check, so instead of stopping early it runs
# the whole install against stubs: HOME is redirected into the sandbox (every
# install path in lib.sh derives from HOME), the deploy dir is copied into a
# scratch repo skeleton (install.sh resolves packages/{client,webview}/dist
# relative to its own location), a pre-built fake parlay-server binary skips
# the go build, and uname/launchctl/plutil/curl/bun/go are PATH stubs. Nothing
# touches the real launchd, the real support dir, or the network.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; [ -n "${2:-}" ] && printf '      %s\n' "$2" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/goserver-install.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT
STUB="$ROOT/stubs"
mkdir -p "$STUB"

# ── Stubs ─────────────────────────────────────────────────────────────────────
# uname: always Darwin, so the test behaves the same on any host.
printf '#!/bin/sh\n[ "${1:-}" = "-s" ] && { echo Darwin; exit 0; }\nexec /usr/bin/uname "$@"\n' > "$STUB/uname"
# launchctl: `print` says not-loaded (fresh install, skips the bootout wait);
# every other subcommand succeeds silently. Invocations are logged.
cat > "$STUB/launchctl" <<'S'
#!/bin/sh
echo "launchctl $*" >> "${STUB_LOG:?}/launchctl.log"
[ "${1:-}" = "print" ] && exit 1
exit 0
S
# plutil: lint always passes (the template itself is validated by the real
# plutil in a real install; this test is about the assets pipeline).
printf '#!/bin/sh\nexit 0\n' > "$STUB/plutil"
# curl: the health probe answers healthy immediately.
printf '#!/bin/sh\necho '\''{"ok":true}'\''\n' > "$STUB/curl"
# go: must never run — the fake pre-built binary makes the build step skip.
printf '#!/bin/sh\necho "go stub invoked: $*" >&2\nexit 1\n' > "$STUB/go"
# bun: logs cwd + the reload-target env + argv, and emulates `bun run build`
# by writing each package's dist output, so --build's copy step has real
# files to ship.
cat > "$STUB/bun" <<'S'
#!/bin/sh
echo "$PWD|PARLAY_RELOAD_TARGET=${PARLAY_RELOAD_TARGET-unset}|bun $*" >> "${STUB_LOG:?}/bun.log"
case "$PWD" in
  */packages/client)  mkdir -p dist && echo "freshly-built-client"  > dist/index.html ;;
  */packages/webview) mkdir -p dist && echo "freshly-built-webview" > dist/index.html ;;
esac
exit 0
S
chmod +x "$STUB"/*

# ── Sandbox repo skeleton ─────────────────────────────────────────────────────
# fresh_repo [client|noclient] [webview|nowebview] → $REPO, $HOMEDIR reset
fresh_repo() {
  REPO="$ROOT/repo"; HOMEDIR="$ROOT/home"; LOGDIR="$ROOT/log"
  rm -rf "$REPO" "$HOMEDIR" "$LOGDIR"
  mkdir -p "$REPO/packages/go-server" "$HOMEDIR" "$LOGDIR"
  cp -R "$SELF_DIR/." "$REPO/packages/go-server/deploy/"
  # Pre-built fake server binary: step 1's `[ ! -x ]` sees it and skips go.
  printf '#!/bin/sh\nexit 0\n' > "$REPO/packages/go-server/parlay-server"
  chmod +x "$REPO/packages/go-server/parlay-server"
  [ "$1" = client ]  && { mkdir -p "$REPO/packages/client/dist";  echo "prebuilt-client"  > "$REPO/packages/client/dist/index.html"; }
  [ "$2" = webview ] && { mkdir -p "$REPO/packages/webview/dist"; echo "prebuilt-webview" > "$REPO/packages/webview/dist/index.html"; }
  # --build needs the package dirs to exist even when dist/ does not.
  mkdir -p "$REPO/packages/client" "$REPO/packages/webview"
}

# run [args...] → RC, $ROOT/out
run() {
  RC=0
  env -i PATH="$STUB:/usr/bin:/bin:/usr/sbin:/sbin" HOME="$HOMEDIR" STUB_LOG="$LOGDIR" \
    /bin/bash "$REPO/packages/go-server/deploy/install.sh" "$@" >"$ROOT/out" 2>&1 || RC=$?
}

ASSETS="Library/Application Support/parlay/dist"

# ── 1. Copy-only default: both dists ship, bun never runs ─────────────────────
fresh_repo client webview
run
if [ "$RC" -ne 0 ]; then
  fail "default install: exit $RC, want 0" "$(cat "$ROOT/out")"
else
  pass "default install exits 0"
fi
if [ "$(cat "$HOMEDIR/$ASSETS/index.html" 2>/dev/null)" = "prebuilt-client" ]; then
  pass "client dist lands at the assets root (served at /)"
else
  fail "client dist not at assets root" "$(ls -R "$HOMEDIR/$ASSETS" 2>&1)"
fi
if [ "$(cat "$HOMEDIR/$ASSETS/fleet/index.html" 2>/dev/null)" = "prebuilt-webview" ]; then
  pass "webview dist lands under <assets-dir>/fleet (served at /fleet/)"
else
  fail "webview dist not under <assets-dir>/fleet" "$(ls -R "$HOMEDIR/$ASSETS" 2>&1)"
fi
if [ -s "$LOGDIR/bun.log" ]; then
  fail "copy-only default invoked bun" "$(cat "$LOGDIR/bun.log")"
else
  pass "copy-only default never invokes bun"
fi
# The plist's -assets-dir must name the same root the files landed in, or the
# server would serve a different tree than the one the install populated.
PLIST="$HOMEDIR/Library/LaunchAgents/com.parlay.go-server.plist"
if grep -qF "$HOMEDIR/$ASSETS" "$PLIST" 2>/dev/null; then
  pass "rendered plist's -assets-dir names the populated assets root"
else
  fail "plist -assets-dir does not match the populated root" "$(grep -A1 assets "$PLIST" 2>&1)"
fi

# ── 2. Missing webview dist: warn, still exit 0, client still ships ───────────
fresh_repo client nowebview
run
if [ "$RC" -ne 0 ]; then
  fail "missing webview dist: exit $RC, want 0" "$(cat "$ROOT/out")"
elif ! grep -q "packages/webview/dist not found" "$ROOT/out"; then
  fail "missing webview dist: no warning" "$(cat "$ROOT/out")"
elif [ ! -f "$HOMEDIR/$ASSETS/index.html" ]; then
  fail "missing webview dist: client copy was skipped too"
else
  pass "a missing webview dist warns and never fails the install"
fi

# ── 3. Missing client dist: warn, still exit 0 ────────────────────────────────
fresh_repo noclient nowebview
run
if [ "$RC" -ne 0 ] || ! grep -q "packages/client/dist not found" "$ROOT/out"; then
  fail "missing client dist: exit $RC or no warning" "$(cat "$ROOT/out")"
else
  pass "a binary-only install (no dists at all) still succeeds with warnings"
fi

# ── 4. --build: bun runs in both packages, client ping disabled ───────────────
fresh_repo noclient nowebview
run --build
if [ "$RC" -ne 0 ]; then
  fail "--build: exit $RC, want 0" "$(cat "$ROOT/out")"
else
  pass "--build install exits 0"
fi
if grep -q "packages/client|PARLAY_RELOAD_TARGET=off|bun run build" "$LOGDIR/bun.log" 2>/dev/null; then
  pass "--build builds the client with the reload ping disabled (PARLAY_RELOAD_TARGET=off)"
else
  fail "--build client build missing or ping not disabled" "$(cat "$LOGDIR/bun.log" 2>&1)"
fi
if grep -q "packages/webview|.*|bun run build" "$LOGDIR/bun.log" 2>/dev/null; then
  pass "--build builds the webview"
else
  fail "--build webview build missing" "$(cat "$LOGDIR/bun.log" 2>&1)"
fi
if [ "$(cat "$HOMEDIR/$ASSETS/index.html" 2>/dev/null)" = "freshly-built-client" ] \
   && [ "$(cat "$HOMEDIR/$ASSETS/fleet/index.html" 2>/dev/null)" = "freshly-built-webview" ]; then
  pass "--build's output is what the copy step ships"
else
  fail "--build output did not reach the assets dir" "$(ls -R "$HOMEDIR/$ASSETS" 2>&1)"
fi

# ── 5. Unknown flags still rejected ───────────────────────────────────────────
fresh_repo client webview
run --bogus
if [ "$RC" -ne 2 ] || ! grep -q "unknown arg" "$ROOT/out"; then
  fail "unknown flag: exit $RC, want 2" "$(cat "$ROOT/out")"
else
  pass "unknown flag exits 2"
fi

if [ "$FAILED" -ne 0 ]; then
  echo "install.test.sh: FAILED" >&2
  exit 1
fi
echo "install.test.sh: all tests passed"
