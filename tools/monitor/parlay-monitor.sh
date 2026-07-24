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
#   PARLAY_RELAY_RUNTIME   runtime dir holding relay.sock + <agent>.chan spools
#                          (default: $TMPDIR/parlay, falling back to /tmp/parlay)
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
  PARLAY_RELAY_RUNTIME   runtime dir (default \$TMPDIR/parlay or /tmp/parlay)
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

# Validate the kebab-slug shape so the spool path can never escape the runtime dir.
if ! printf '%s' "$AGENT" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*$'; then
  echo "parlay-monitor: --agent must be a kebab-slug (got: '$AGENT')" >&2
  exit 2
fi

# Resolve the runtime dir and control socket. TMPDIR is per-user on macOS, so the
# default matches the relay's own default (defaultRuntimeDir in relay/main.go).
RUNTIME="${PARLAY_RELAY_RUNTIME:-${TMPDIR:-/tmp}/parlay}"
RUNTIME="${RUNTIME%/}"
SOCK="${PARLAY_RELAY_SOCK:-$RUNTIME/relay.sock}"
SPOOL="$RUNTIME/$AGENT.chan"

# Ensure a relay is up before enrolling, so a monitor never dead-ends on a
# missing relay. ensure-up is idempotent and concurrency-safe: it no-ops if the
# relay already answers /health, otherwise it starts it (launchd if installed,
# else the binary) and waits for /health. It respects PARLAY_RELAY_RUNTIME/SOCK
# via the same lib resolution, and honors PARLAY_SERVER for the started relay.
ENSURE_UP="$(cd "$(dirname "$0")" && pwd)/../relay/deploy/ensure-up.sh"
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
