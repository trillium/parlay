#!/usr/bin/env bash
# Behavior tests for install.sh's single-server deployment entry point.
#
# The old LaunchAgent guard existed only to protect the now-retired Pulse
# service. Non-default URLs must reach the normal platform check rather than
# being routed through a second-server refusal or escape hatch.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL="${SELF_DIR}/install.sh"
ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-install-test.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT
STUB="${ROOT}/stubs"
mkdir -p "${STUB}"
cat >"${STUB}/uname" <<'S'
#!/usr/bin/env bash
[ "${1:-}" = "-s" ] && { echo Linux; exit 0; }
exec /usr/bin/uname "$@"
S
chmod +x "${STUB}/uname"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }
run() {
  RC=0
  env -i PATH="${STUB}:/usr/bin:/bin:/usr/sbin:/sbin" HOME="${ROOT}/home" \
    /bin/bash "${INSTALL}" "$@" >"${ROOT}/out" 2>&1 || RC=$?
}

# Arbitrary caller URLs are no longer second-server deployment cases; the
# ordinary macOS-only check is the first side effect-free gate in this harness.
run --server "http://localhost:9999"
if [ "${RC}" -ne 1 ] || ! grep -q 'macOS-only' "${ROOT}/out"; then
  fail "non-default server did not reach the normal platform check (rc=${RC})"
else
  pass "non-default server has no retired refusal guard"
fi

run --server "http://localhost:4242/"
if [ "${RC}" -ne 1 ] || ! grep -q 'macOS-only' "${ROOT}/out"; then
  fail "default server handling changed unexpectedly (rc=${RC})"
else
  pass "default server remains the canonical target"
fi

run --bogus
if [ "${RC}" -ne 2 ] || ! grep -q 'unknown arg' "${ROOT}/out"; then
  fail "unknown flag was not rejected (rc=${RC})"
else
  pass "unknown flags still exit 2"
fi

if [ "${FAILED}" -ne 0 ]; then
  echo "install.test.sh: FAILED" >&2
  exit 1
fi
echo "install.test.sh: all tests passed"
