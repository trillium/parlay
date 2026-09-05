#!/usr/bin/env bash
# Cross-implementation parity harness — docs/scope-go-spawn.md Stage 4
# (Go side folded into the parlay CLI itself by task-42qot).
#
# Runs the SAME named-spawn scenarios through bin/parlay-spawn (bash) and the
# in-process Go pipeline (`parlay spawn`, via bin/parlay, its production
# wrapper) and asserts equivalent observable outcomes: exit code, and a
# refusal/registration message substring. Hermetic — every scenario refuses
# before any herdr tab, subprocess, or network side effect that would start a
# real agent; the one scenario that reaches the network (case 6) points at
# PARLAY_SERVER="http://127.0.0.1:1", a guaranteed connection-refused address,
# so it fails fast without a mock server and never launches anything.
#
# This harness needs a REAL go build (bin/parlay's build-if-stale) to get a
# genuine A/B behavioral comparison — so it only runs where a go toolchain
# exists (the "go" CI job, not "shell"; see .github/workflows/ci.yml).
#
# Scope: the named-spawn shape's gate chain only (kebab validation → bead
# gate → PII routing → model-required → registration), in gate order. Not
# covered here — each is either a documented Full/Partial organ elsewhere in
# docs/scope-go-spawn.md's gap matrix (§2) with its own test coverage, or a
# disclosed, out-of-scope divergence (§5 risk register): ephemeral/batch
# dispatch shapes, --profile/--kind/--pane/--workspace, the herdr RPC-vs-shell
# fast path, the launcher-gated duplicate-agent guard, the herdr-only
# watchdog, and identity-registration's missing bead/gc fields. Widening this
# file to those is future work, not a gap in what it claims to cover.
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SELF_DIR/.." && pwd)"
BASH_SPAWN="$SELF_DIR/parlay-spawn"
GO_SPAWN="$REPO_ROOT/bin/parlay"

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ok   $1"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL $1"; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/parlay-spawn-parity.XXXXXX")"
# go build's module cache under $HOME/go/pkg/mod is written read-only by the
# go tool itself (its own tamper-guard) — a plain `rm -rf` on this tree fails
# partway through under a redirected HOME like this test's. chmod the tree
# writable first so cleanup actually removes it instead of leaving debris.
trap 'chmod -R u+w "$ROOT" 2>/dev/null; rm -rf "$ROOT"' EXIT

HOME_DIR="$ROOT/home"
mkdir -p "$HOME_DIR"

# Inert herdr: every scenario here refuses (or fails registration) before the
# launcher is ever consulted, but it must exist on PATH for both scripts to
# get past their own preflight.
STUB_DIR="$ROOT/stub"
mkdir -p "$STUB_DIR"
cat > "$STUB_DIR/herdr" <<'STUB'
#!/usr/bin/env bash
echo '{}'
exit 0
STUB
chmod +x "$STUB_DIR/herdr"

# Fake bead-store binary for the closed-bead scenario. Both implementations
# resolve a bead id's store CLI from the token before its first '-'
# (bead.go's resolveBeadStatus / bash's bead_gate), so a bead id prefixed
# "fakestore-" resolves to this binary.
cat > "$STUB_DIR/fakestore" <<'STUB'
#!/usr/bin/env bash
if [ "$1" = "show" ]; then
  printf '{"id":"%s","status":"closed"}\n' "$2"
fi
exit 0
STUB
chmod +x "$STUB_DIR/fakestore"

CLOSED_BEAD="fakestore-9001"

# Pin the real Go build/module cache BEFORE redirecting HOME below — go
# derives GOCACHE/GOMODCACHE from $HOME by default, so without this every
# scenario's go build (via bin/parlay-bin) would redownload the module graph
# into the throwaway HOME_DIR instead of reusing CI's actions/setup-go cache
# (same fix as .github/workflows/ci.yml's "Pin Go cache paths" step).
GO_CACHE_ENV=()
if command -v go >/dev/null 2>&1; then
  GO_CACHE_ENV=(
    "GOCACHE=$(go env GOCACHE)"
    "GOMODCACHE=$(go env GOMODCACHE)"
    "GOPATH=$(go env GOPATH)"
  )
fi

common_env() {
  env "${GO_CACHE_ENV[@]}" \
    HOME="$HOME_DIR" PATH="$STUB_DIR:$PATH" PARLAY_SERVER="http://127.0.0.1:1" \
    PARLAY_SPAWN_SKIP_CONTRACT=1 PARLAY_SPAWN_NO_WATCHDOG=1 \
    "$@"
}

run_bash() {
  # Deliberately `-` not `:-`: PARITY_VIA_CLI="" (case 1) must produce an
  # EMPTY PARLAY_SPAWN_VIA_CLI, not fall back to the "1" default — only
  # unset should default.
  local via_cli="${PARITY_VIA_CLI-1}"
  common_env env PARLAY_SPAWN_VIA_CLI="$via_cli" PARLAY_SPAWN_BEADS_REQUIRED="${PARITY_BEADS_REQUIRED:-0}" \
    "$BASH_SPAWN" "$@" 2>&1
}

run_go() {
  # PARLAY_SPAWN_IMPL= (empty) neutralizes any ambient escape-hatch setting:
  # the Go side of this A/B must be the in-process pipeline, never a detour
  # back through bash. (Empty env falls through to config.toml, absent under
  # the redirected HOME, which selects in-process — config.SpawnImpl.)
  common_env env PARLAY_SPAWN_IMPL= PARLAY_SPAWN_BEADS_REQUIRED="${PARITY_BEADS_REQUIRED:-0}" \
    "$GO_SPAWN" spawn "$@" 2>&1
}

# assert_parity NAME EXPECTED_RC SUBSTRING [args...]
#
# SUBSTRING must appear verbatim in BOTH outputs — used only for scenarios
# where the two implementations' messages are byte-identical (they are, for
# every case below except case 2, which is called out separately).
assert_parity() {
  local name="$1" want_rc="$2" substr="$3"
  shift 3
  local bash_out bash_rc go_out go_rc
  bash_out="$(run_bash "$@")"; bash_rc=$?
  go_out="$(run_go "$@")"; go_rc=$?

  if [ "$bash_rc" -eq "$want_rc" ]; then ok "$name: bash exit $want_rc"; else bad "$name: bash exit — want $want_rc, got $bash_rc; output:"$'\n'"$bash_out"; fi
  if [ "$go_rc" -eq "$want_rc" ]; then ok "$name: go exit $want_rc"; else bad "$name: go exit — want $want_rc, got $go_rc; output:"$'\n'"$go_out"; fi
  if grep -qF "$substr" <<<"$bash_out"; then ok "$name: bash message contains expected substring"; else bad "$name: bash message missing substring [$substr]; output:"$'\n'"$bash_out"; fi
  if grep -qF "$substr" <<<"$go_out"; then ok "$name: go message contains expected substring"; else bad "$name: go message missing substring [$substr]; output:"$'\n'"$go_out"; fi
}

# ── 1. PARLAY_SPAWN_VIA_CLI missing — the one-front-door invariant ──────────
# BASH-ONLY since task-42qot: `parlay spawn` runs the pipeline in-process, so
# the Go side has no cross-binary handshake left to police — only the bash
# script (still invokable directly) keeps its refusal.
bash_out="$(PARITY_VIA_CLI="" run_bash parity-a "Parity A" "#aabbcc" "task")"; bash_rc=$?
if [ "$bash_rc" -eq 2 ]; then ok "via-cli-missing: bash exit 2"; else bad "via-cli-missing: bash exit — want 2, got $bash_rc; output:"$'\n'"$bash_out"; fi
if grep -qF "refusing to run directly — task-qyu8q scope 3" <<<"$bash_out"; then ok "via-cli-missing: bash message"; else bad "via-cli-missing: bash message missing; output:"$'\n'"$bash_out"; fi

# ── 2. Invalid kebab-slug agent-id ───────────────────────────────────────────
# Quote style diverges by design (bash: 'Not_Kebab', go's fmt %q: "Not_Kebab")
# — a cosmetic byte difference, not a behavioral one — so this case is
# asserted by hand rather than through assert_parity's single shared substring.
bash_out="$(run_bash Not_Kebab "Bad Slug" "#aabbcc" "task" --model sonnet)"; bash_rc=$?
go_out="$(run_go Not_Kebab "Bad Slug" "#aabbcc" "task" --model sonnet)"; go_rc=$?
if [ "$bash_rc" -eq 2 ]; then ok "bad-kebab: bash exit 2"; else bad "bad-kebab: bash exit — want 2, got $bash_rc; output:"$'\n'"$bash_out"; fi
if [ "$go_rc" -eq 2 ]; then ok "bad-kebab: go exit 2"; else bad "bad-kebab: go exit — want 2, got $go_rc; output:"$'\n'"$go_out"; fi
if grep -qF "agent-id must be a kebab-slug" <<<"$bash_out"; then ok "bad-kebab: bash message"; else bad "bad-kebab: bash message missing; output:"$'\n'"$bash_out"; fi
if grep -qF "agent-id must be a kebab-slug" <<<"$go_out"; then ok "bad-kebab: go message"; else bad "bad-kebab: go message missing; output:"$'\n'"$go_out"; fi

# ── 3. beads-required mode ON, no --bead ─────────────────────────────────────
PARITY_BEADS_REQUIRED=1 assert_parity "beads-required-missing" 2 "beads-required mode is ON" \
  parity-b "Parity B" "#aabbcc" "task" --model sonnet

# ── 4. Named --bead pointing at a closed bead ───────────────────────────────
assert_parity "closed-bead" 1 "is closed — refusing to spawn." \
  parity-c "Parity C" "#aabbcc" "task" --model sonnet --bead "$CLOSED_BEAD"

# ── 5. No model chosen (named form, no --model/--profile) ──────────────────
assert_parity "no-model" 2 "refusing to spawn — no model was chosen" \
  parity-d "Parity D" "#aabbcc" "task"

# ── 6. Every gate satisfied — reaches the same network boundary and fails
#         identically against an unreachable Parlay server. This is the
#         hermetic stand-in for "successful spawn": both implementations
#         parsed the same flags, resolved the same model, and attempted the
#         same registration call — proof the launch-command shape matches —
#         without either one ever starting a real agent. ─────────────────
assert_parity "registration-unreachable" 1 "register-agent failed — is Pulse running on" \
  parity-e "Parity E" "#aabbcc" "task" --model sonnet

echo
echo "$PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
