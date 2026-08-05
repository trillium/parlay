#!/usr/bin/env bash
# parlay-monitor — the thinnest possible per-agent monitor.
#
# One-call enroll + stream: registers this agent with the central relay (adds
# itself to the registry, which starts the relay's upstream poll loop for this
# channel), then execs `tail -F` on the agent's spool file so its final process
# footprint is `tail` alone (~1.2MB) — not a ~40MB bun poller.
#
# A harness Monitor tool runs this and wakes the agent on every CHAT_MSG line.
#
# Usage:
#   parlay-monitor.sh --agent <id> [--notify-safe]
#
# --notify-safe: cap each emitted CHAT_MSG line to a notification-safe budget
#   (PARLAY_NOTIFY_BUDGET chars, default 400). WHY: when this stream runs under a
#   harness Monitor tool, the harness truncates long single-event lines mid-word
#   for display — so an agent reading a long voice-dictated message only ever sees
#   the head, cut mid-word, with no signal that content was lost (robots-n6vl).
#   The RAW spool line is complete; only the harness *display* truncates. In
#   --notify-safe mode we truncate deterministically at a budget BELOW the harness
#   cap and append an explicit pointer that preserves the message id and tells the
#   agent how to fetch the full text — so a truncation is self-describing and
#   recoverable instead of a silent mid-word cut. Default OFF so raw programmatic
#   consumers of the stream keep getting complete, unmodified lines.
#
# Env:
#   PARLAY_SERVER          upstream Pulse server to enroll against. Anything other
#                          than the default (http://localhost:31337) gets its own
#                          server-scoped runtime dir and relay, so a sandbox can
#                          never enroll into the production registry (robots-buu8).
#   PARLAY_RELAY_RUNTIME   runtime dir holding relay.sock + <agent>.chan spools
#                          (default: server-scoped; $TMPDIR/parlay for the default
#                          server, $TMPDIR/parlay/by-server/<slug> otherwise)
#   PARLAY_RELAY_SOCK      explicit control-socket path (default: <runtime>/relay.sock)
#   PARLAY_NOTIFY_BUDGET   --notify-safe per-line char budget (default 400)
#
# Exit codes: 0 (never, tail runs until killed), 2 usage error, 1 relay/enroll error.
set -euo pipefail

usage() {
  cat >&2 <<EOF
Usage: parlay-monitor.sh --agent <id> [--notify-safe]

Registers <id> with the parlay relay, then streams its channel's CHAT_MSG lines
to stdout via 'tail -F'. Intended to be run under a harness Monitor tool.

  --notify-safe   cap each emitted line to a notification-safe budget and append
                  a "fetch full text" pointer (harness Monitor tools truncate long
                  lines mid-word; this makes that recoverable). Default off.

Env:
  PARLAY_SERVER          upstream server; a non-default value gets its own
                         server-scoped runtime dir + relay
  PARLAY_RELAY_RUNTIME   runtime dir (default: server-scoped under \$TMPDIR/parlay)
  PARLAY_RELAY_SOCK      control socket path (default <runtime>/relay.sock)
  PARLAY_NOTIFY_BUDGET   --notify-safe per-line char budget (default 400)
EOF
  exit 2
}

AGENT=""
NOTIFY_SAFE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --agent) AGENT="${2:-}"; shift 2 ;;
    --notify-safe) NOTIFY_SAFE=1; shift ;;
    -h|--help) usage ;;
    *) echo "parlay-monitor: unknown arg: $1" >&2; usage ;;
  esac
done

[ -n "$AGENT" ] || { echo "parlay-monitor: --agent <id> is required" >&2; usage; }

# ── Never die quietly before streaming starts (robots-dcag) ───────────────────
# By the time this script runs, `parlay listen` has already registered and
# announced the agent with Pulse. If we then exit without reaching the stream,
# the panel shows a healthy agent whose event stream does not exist — it takes
# no directives for the rest of the session and nothing says so. Any exit from
# the setup phase therefore names itself, its code, and the consequence. `set
# -e` deaths land here too, which is exactly the case that went unreported.
# Cleared once `tail` is reached, since a stream ending later is a different
# (and visible) event.
STREAMING=0
on_setup_exit() {
  local code=$?
  [ "$code" = 0 ] && return 0
  [ "$STREAMING" = 1 ] && return 0
  echo "parlay-monitor: FAILED during setup (exit $code) — '$AGENT' is NOT streaming." >&2
  echo "parlay-monitor:   If 'parlay listen' already registered it, this agent is now" >&2
  echo "parlay-monitor:   registered-but-deaf: visible in the panel, receiving nothing." >&2
  echo "parlay-monitor:   Re-run the listen/Monitor command to re-arm it (robots-dcag)." >&2
}
trap on_setup_exit EXIT

# Validate the kebab-slug shape so the spool path can never escape the runtime dir.
if ! printf '%s' "$AGENT" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*$'; then
  echo "parlay-monitor: --agent must be a kebab-slug (got: '$AGENT')" >&2
  exit 2
fi

HERE="$(cd "$(dirname "$0")" && pwd)"
ENSURE_UP="$HERE/../relay/deploy/ensure-up.sh"
RELAY_LIB="$HERE/../relay/deploy/lib.sh"

# ── Resolve the runtime dir, SCOPED BY UPSTREAM SERVER (robots-buu8) ───────────
# A relay is a per-runtime-dir singleton bound to ONE upstream server, so which
# relay we enroll on decides which server's registry we land in — $PARLAY_SERVER
# alone does not. Enrolling on the shared $TMPDIR/parlay relay (bound to
# production :31337) while $PARLAY_SERVER points at a scratch server silently
# registered the agent in the captain's LIVE registry.
#
# parlay_relay_scoped_runtime_dir reserves the canonical dir for the default
# server and gives every other $PARLAY_SERVER its own dir (and thus its own
# relay). Exported so ensure-up.sh and the relay launcher it starts resolve the
# identical dir. An explicit $PARLAY_RELAY_RUNTIME still wins — a caller that
# pinned a dir keeps it, and the mismatch guard below covers the rest.
if [ -r "$RELAY_LIB" ]; then
  # shellcheck source=../relay/deploy/lib.sh
  . "$RELAY_LIB"
fi
# Guarded on the helper, not just the file: a stale lib.sh would make this an
# unresolved command, and under `set -e` that aborts the monitor outright.
if command -v parlay_relay_scoped_runtime_dir >/dev/null 2>&1; then
  RUNTIME="$(parlay_relay_scoped_runtime_dir)"
  # Say so out loud. Scoping means this monitor is NOT on the shared relay, so if
  # the target server is wrong or dead the agent goes quiet — that must be visible
  # in the monitor's own stderr, not diagnosed later from an empty channel.
  if [ -z "${PARLAY_RELAY_RUNTIME:-}" ] && [ "$RUNTIME" != "$(parlay_relay_runtime_dir)" ]; then
    echo "parlay-monitor: PARLAY_SERVER=$(parlay_relay_target_server) is not the default" >&2
    echo "parlay-monitor:   server — using a server-scoped relay at $RUNTIME" >&2
    SCOPED=1
  fi
  export PARLAY_RELAY_RUNTIME="$RUNTIME"
else
  # lib.sh missing or stale (older/partial checkout): keep the original
  # resolution. The pre-enroll server check below still catches a cross-server
  # enroll, so the leak stays closed even without scoping.
  RUNTIME="${PARLAY_RELAY_RUNTIME:-${TMPDIR:-/tmp}/parlay}"
fi
RUNTIME="${RUNTIME%/}"
SOCK="${PARLAY_RELAY_SOCK:-$RUNTIME/relay.sock}"
SPOOL="$RUNTIME/$AGENT.chan"

# A Unix socket path over sun_path (104 bytes) fails bind() with a bare "invalid
# argument" that names neither the limit nor the path. Check up front and say it.
if command -v parlay_relay_sock_path_ok >/dev/null 2>&1 \
   && ! parlay_relay_sock_path_ok "$SOCK"; then
  echo "parlay-monitor: control socket path is ${#SOCK} bytes, over the 103-byte" >&2
  echo "parlay-monitor:   Unix-socket limit — the relay cannot bind it:" >&2
  echo "parlay-monitor:   $SOCK" >&2
  echo "parlay-monitor: set PARLAY_RELAY_RUNTIME to a shorter directory." >&2
  exit 1
fi

# Record which upstream server this scoped runtime dir belongs to. The dir name
# is a hash (sun_path is tight), so this marker is what makes a stray scoped
# relay identifiable by a human later. Not a .chan file, so no relay reads it.
if [ "${SCOPED:-0}" = 1 ]; then
  mkdir -p "$RUNTIME" 2>/dev/null || true
  printf '%s\n' "$(parlay_relay_target_server)" >"$RUNTIME/server" 2>/dev/null || true
fi

# Ensure a relay is up before enrolling, so a monitor never dead-ends on a
# missing relay. ensure-up is idempotent and concurrency-safe: it no-ops if the
# relay already answers /health, otherwise it starts it (launchd if installed AND
# it serves this runtime dir + server, else the binary) and waits for /health. It
# respects PARLAY_RELAY_RUNTIME/SOCK via the same lib resolution, and honors
# PARLAY_SERVER for the started relay.
if [ -x "$ENSURE_UP" ]; then
  if ! "$ENSURE_UP"; then
    echo "parlay-monitor: relay is not up and could not be started" >&2
    echo "parlay-monitor: install the relay (tools/relay/deploy/install.sh) or start it manually" >&2
    exit 1
  fi
elif [ ! -S "$SOCK" ]; then
  # ensure-up missing (older checkout): fall back to the original hard requirement.
  echo "parlay-monitor: relay control socket not found at $SOCK" >&2
  echo "parlay-monitor: start the relay first (tools/relay/parlay-relay)" >&2
  exit 1
fi

# After ensure-up, the socket must exist. Guard so the enroll below has a target.
if [ ! -S "$SOCK" ]; then
  echo "parlay-monitor: relay control socket still not found at $SOCK after ensure-up" >&2
  exit 1
fi

# ── Refuse to enroll on a relay bound to the wrong upstream server ────────────
# Last line of defence for robots-buu8, and the one that holds even when the
# scoping above is bypassed (explicit $PARLAY_RELAY_RUNTIME/$PARLAY_RELAY_SOCK,
# a lib.sh-less checkout, or a relay someone started by hand). GET /agents is
# read-only, so this runs BEFORE /register — a mismatch must abort without ever
# touching the wrong registry. An unreachable/older relay reports nothing; that
# is not a mismatch, so we proceed rather than hard-fail on unknown.
#
# THE PROBE MUST NEVER ABORT THE MONITOR (robots-dcag). This was written as a
# bare `VAR=$(curl … | sed …)`, and under this script's `set -euo pipefail` a
# failing command substitution in a plain assignment takes its own exit status
# and kills the script — so a curl timeout (exit 28) ended the monitor HERE,
# silently, three lines before the first "enrolling" message. `parlay listen`
# registers and announces with Pulse before shelling out to this script, so the
# panel showed the agent present and healthy while its event stream was dead:
# registered-but-deaf for the rest of the session, receiving no directives.
# `2>/dev/null` hid curl's own complaint; nothing else printed. Every probe
# failure is now caught and reported, and an unknown answer only ever means
# "could not verify", never "abort".
if [ -n "${PARLAY_SERVER:-}" ]; then
  WANT_SERVER="${PARLAY_SERVER%/}"
  # 2s was also simply too tight: /agents serializes the whole registry, and on
  # the captain's box (269 agents) it routinely answers in >2s, so the timeout
  # was reached on a perfectly healthy relay. This is a one-shot startup probe,
  # not a hot path — give it room, and let a caller tune it.
  PROBE_TIMEOUT="${PARLAY_RELAY_PROBE_TIMEOUT:-15}"
  RELAY_SERVER=""
  PROBE_ERR=""
  if command -v parlay_relay_reported_server >/dev/null 2>&1; then
    # lib.sh's helper is already internally guarded (`|| return 0`), but keep the
    # `|| true` anyway: this assignment must not be able to abort under set -e
    # regardless of what a future/older lib.sh does inside.
    RELAY_SERVER="$(parlay_relay_reported_server "$SOCK" 2>/dev/null || true)"
  else
    # lib.sh missing or stale: same probe inline, guarded the same way. The
    # substitution's status is consumed by `||` so `set -e` never sees it.
    PROBE_BODY="$(curl -fsS --max-time "$PROBE_TIMEOUT" --unix-socket "$SOCK" \
      http://relay/agents 2>/dev/null)" || { PROBE_ERR=$?; PROBE_BODY=""; }
    RELAY_SERVER="$(printf '%s' "$PROBE_BODY" \
      | sed -n 's/.*"server":"\([^"]*\)".*/\1/p')"
  fi
  RELAY_SERVER="${RELAY_SERVER%/}"
  # Say when the check could not be made. Proceeding unverified is the correct
  # behaviour (an older relay does not report a server at all), but it must be
  # visible — a silent skip is how a real mismatch would slip through later.
  if [ -z "$RELAY_SERVER" ]; then
    echo "parlay-monitor: relay at $SOCK did not report its upstream server" >&2
    if [ -n "$PROBE_ERR" ]; then
      echo "parlay-monitor:   (probe failed, curl exit $PROBE_ERR, ${PROBE_TIMEOUT}s timeout)" >&2
    fi
    echo "parlay-monitor:   proceeding unverified — cannot confirm it serves $WANT_SERVER" >&2
  fi
  if [ -n "$RELAY_SERVER" ] && [ "$RELAY_SERVER" != "$WANT_SERVER" ]; then
    echo "parlay-monitor: refusing to enroll '$AGENT' — relay at $SOCK is bound to" >&2
    echo "parlay-monitor:   $RELAY_SERVER but PARLAY_SERVER is $WANT_SERVER." >&2
    echo "parlay-monitor: enrolling anyway would register this agent in the WRONG" >&2
    echo "parlay-monitor:   server's registry (robots-buu8)." >&2
    echo "parlay-monitor: unset PARLAY_RELAY_RUNTIME/PARLAY_RELAY_SOCK to get an" >&2
    echo "parlay-monitor:   automatically server-scoped relay, or point them at a" >&2
    echo "parlay-monitor:   runtime dir whose relay serves $WANT_SERVER." >&2
    exit 1
  fi
fi

# 1. Enroll: POST /register {"agent":"<id>"} to the relay over its Unix socket.
#    Idempotent server-side — re-running is safe. The relay creates the spool and
#    starts (or reuses) the upstream poll loop for this channel.
echo "parlay-monitor: enrolling '$AGENT' via $SOCK" >&2
REG=$(curl -s --unix-socket "$SOCK" \
  -X POST "http://relay/register" \
  -H "Content-Type: application/json" \
  --data "{\"agent\":\"$AGENT\"}") || {
    echo "parlay-monitor: enroll request failed (is the relay running?)" >&2
    exit 1
  }

# Confirm the relay accepted us. The response is {"ok":true,...} or {"error":...}.
case "$REG" in
  *'"ok":true'*) : ;;
  *) echo "parlay-monitor: relay rejected enroll: $REG" >&2; exit 1 ;;
esac

# The relay returns the authoritative spool path; prefer it so the monitor and
# relay never disagree on the location. Fall back to the computed path.
RELAY_SPOOL=$(printf '%s' "$REG" | sed -n 's/.*"spool":"\([^"]*\)".*/\1/p')
[ -n "$RELAY_SPOOL" ] && SPOOL="$RELAY_SPOOL"

# The relay creates the spool during register, but guard against a race where
# this reader arrives a beat early. Bounded wait, then proceed regardless — tail
# -F will pick the file up once it appears.
for _ in 1 2 3 4 5 6 7 8 9 10; do
  [ -e "$SPOOL" ] && break
  sleep 0.1
done

echo "parlay-monitor: streaming '$AGENT' from $SPOOL" >&2
# Setup is over: past here, an exit is the stream ending (usually the harness
# killing us), not the silent registered-but-deaf failure the trap warns about.
STREAMING=1

# 2. Stream. Flags:
#      -n0  start at end-of-file — no replay of already-consumed spool lines
#      -F   follow by name; re-open on truncate/rotate/recreate. This is the
#           "channel re-open after relay restart" correctness requirement: if the
#           relay is restarted and the spool is recreated, tail -F reattaches
#           without the monitor needing to restart.
#
# Default (no --notify-safe): `exec` replaces this shell with tail so the monitor's
# footprint is tail's alone (~1.2MB) and the raw spool line reaches stdout byte-
# for-byte. Programmatic consumers depend on that completeness.
#
# --notify-safe: pipe tail through awk that caps each over-budget line and appends
# a self-describing pointer (id survives — it sits in the first ~55 chars). fflush
# after every line keeps the Monitor tool's per-line event contract intact. This
# costs one extra awk process; only harness agents that opt in pay it.
if [ "$NOTIFY_SAFE" = 1 ]; then
  BUDGET="${PARLAY_NOTIFY_BUDGET:-400}"
  # No `exec` here: it cannot replace the shell with a pipeline. tail+awk run under
  # this shell; killing the monitor's process group (as the harness does) reaps both.
  tail -n0 -F "$SPOOL" | awk -v BUD="$BUDGET" '
    {
      if (length($0) > BUD) {
        printf "%s ⟪+%d chars truncated for notification — run: parlay history 30 --full⟫\n", substr($0, 1, BUD), length($0) - BUD
      } else {
        print $0
      }
      fflush()
    }'
else
  exec tail -n0 -F "$SPOOL"
fi
