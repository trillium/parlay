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
#   parlay-monitor.sh --reap [--apply]
#
# --reap: report every `tail -F` reader on this host whose launcher chain is
#   gone, and with --apply kill them. See the "one reader per channel" section
#   below for why they accumulate and why an orphan is identifiable.
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
#   PARLAY_SERVER          upstream Parlay chat server (resolved by the CLI).
#   PARLAY_RELAY_RUNTIME   runtime dir holding relay.sock + <agent>.chan spools
#                          (default: $TMPDIR/parlay)
#   PARLAY_RELAY_SOCK      explicit control-socket path (default: <runtime>/relay.sock)
#   PARLAY_NOTIFY_BUDGET   --notify-safe per-line char budget (default 400)
#   PARLAY_MONITOR_WATCH_INTERVAL  seconds between orphan checks (default 15)
#   PARLAY_MONITOR_NO_ORPHAN_EXIT  set to 1 to keep streaming after the launcher
#                          dies (deliberate daemonization; off by default)
#
# Exit codes: 0 (never, tail runs until killed), 2 usage error, 1 relay/enroll error.
set -euo pipefail

usage() {
  cat >&2 <<EOF
Usage: parlay-monitor.sh --agent <id> [--notify-safe]
       parlay-monitor.sh --agent <id> --preflight
       parlay-monitor.sh --reap [--apply]

Registers <id> with the parlay relay, then streams its channel's CHAT_MSG lines
to stdout via 'tail -F'. Intended to be run under a harness Monitor tool.

  --preflight     verify the canonical relay is reachable WITHOUT registering
                  or streaming, then exit 0. This is the pre-enrollment probe
                  'parlay listen'/'parlay claim' run so a fresh-clone user (no
                  relay binary) fails BEFORE the agent is registered-but-deaf
                  (issue #173). Reuses the exact same setup guards as a real
                  stream — ensure-up and the socket guard.
  --notify-safe   cap each emitted line to a notification-safe budget and append
                  a "fetch full text" pointer (harness Monitor tools truncate long
                  lines mid-word; this makes that recoverable). Default off.
  --reap          list every channel reader under the relay runtime dir whose
                  launcher chain is gone (dry run). Add --apply to kill them.
                  Scoped to \$PARLAY_RELAY_RUNTIME (default \$TMPDIR/parlay) and
                  everything nested under it, so it can never reach another
                  runtime dir's readers.

Env:
  PARLAY_SERVER          upstream server resolved by the CLI
  PARLAY_RELAY_RUNTIME   runtime dir (default: \$TMPDIR/parlay)
  PARLAY_RELAY_SOCK      control socket path (default <runtime>/relay.sock)
  PARLAY_NOTIFY_BUDGET   --notify-safe per-line char budget (default 400)
  PARLAY_MONITOR_WATCH_INTERVAL  seconds between orphan checks (default 15)
  PARLAY_MONITOR_NO_ORPHAN_EXIT  1 = keep streaming after the launcher dies
EOF
  exit 2
}

AGENT=""
NOTIFY_SAFE=0
REAP=0
APPLY=0
PREFLIGHT=0
while [ $# -gt 0 ]; do
  case "$1" in
    --agent) AGENT="${2:-}"; shift 2 ;;
    --notify-safe) NOTIFY_SAFE=1; shift ;;
    --reap) REAP=1; shift ;;
    --apply) APPLY=1; shift ;;
    --preflight) PREFLIGHT=1; shift ;;
    -h|--help) usage ;;
    *) echo "parlay-monitor: unknown arg: $1" >&2; usage ;;
  esac
done

WATCH_INTERVAL="${PARLAY_MONITOR_WATCH_INTERVAL:-15}"

# ── One reader per channel, and no reader outliving its launcher (robots-3pvi) ─
# The spool is append-only and every `tail -F` on it gets its own copy of every
# line, so a channel with N readers wakes N sessions with the same directive —
# and readers used to accumulate because NOTHING ever ended one. A harness kills
# only the shell it spawned; `tail` sits two or three levels below that, is
# reparented to init, and on a quiet channel never writes again so it never even
# earns a SIGPIPE. 168 readers were live on the captain's box when this was
# found — 139 with no launcher left anywhere in their ancestry, oldest 3 days,
# one channel with 20 of them.
#
# The two helpers below are the whole basis of the fix. A reader is matched as a
# WHOLE command line (`tail -n0 -F <spool>`), never as a `pgrep -f` regex — a
# metacharacter in a spool path must never be able to widen a kill.
#
# A reader is an ORPHAN when climbing its ancestry reaches init without passing
# through any process that isn't part of a monitor chain. A working monitor
# always has a foreign root (the tmux/harness shell, herdr, bun), so this can
# never mistake a live reader for garbage.

# readers_of <spool> [ppid] → one pid per line, exact command-line match,
# optionally only children of <ppid> (how this shell picks out its OWN reader
# without guessing). Returns 0 on every path, including a failed `ps`: under this
# script's `set -euo pipefail` a caller writing `VAR=$(readers_of …)` would
# otherwise die on the probe itself (robots-dcag), and "could not enumerate" must
# never be fatal here.
readers_of() {
  ps -axo pid=,ppid=,command= 2>/dev/null | awk -v want="tail -n0 -F $1" -v wantppid="${2:-}" '
    {
      pid = $1
      ppid = $2
      sub(/^[[:space:]]*[0-9]+[[:space:]]+[0-9]+[[:space:]]+/, "")
      if ($0 != want) next
      if (wantppid != "" && ppid + 0 != wantppid + 0) next
      print pid
    }' || true
}

# is_monitor_chain_cmd <command line> → 0 when that process is part of a monitor
# chain (the reader, its wrapper script, or the CLI that spawned either).
is_monitor_chain_cmd() {
  case "$1" in
    "tail -n0 -F "*.chan) return 0 ;;
    *parlay-monitor.sh*) return 0 ;;
    *"parlay-cli listen"*|*"parlay-cli monitor"*) return 0 ;;
    *"parlay listen"*|*"parlay monitor"*) return 0 ;;
  esac
  return 1
}

# ps_row <snapshot> <pid> → "<ppid> <command>", empty when the pid is gone.
# `awk … exit` closes the pipe early, so printf takes a SIGPIPE and pipefail
# would hand the caller a 141 — swallowed here for the same reason as above.
ps_row() {
  printf '%s\n' "$1" | awk -v p="$2" '
    $1 + 0 == p {
      ppid = $2
      sub(/^[[:space:]]*[0-9]+[[:space:]]+[0-9]+[[:space:]]+/, "")
      print ppid " " $0
      exit
    }' || true
}

# reap_readers <apply 0|1> <runtime-dir> — report, and with apply=1 kill, every
# orphaned reader whose spool lives under <runtime-dir>. Killing the reader
# unwinds the whole chain above it: in --notify-safe mode the awk ends when the
# pipe closes, the wrapper script then exits, and the CLI exits with its code.
#
# SCOPED BY RUNTIME DIR ON PURPOSE. An unscoped host-wide kill is not something
# a test or sandbox can be allowed to run — it would reach readers outside the
# test runtime. The scope is the configured runtime dir and the match is a
# prefix.
reap_readers() {
  local apply="$1" runtime="$2"
  local snapshot readers total orphans pid spool row parent pcmd cur orphan killed left
  snapshot="$(ps -axo pid=,ppid=,command= 2>/dev/null || true)"
  readers="$(printf '%s\n' "$snapshot" | awk -v dir="$runtime/" '
    {
      pid = $1
      sub(/^[[:space:]]*[0-9]+[[:space:]]+[0-9]+[[:space:]]+/, "")
      if ($0 !~ /^tail -n0 -F .*\.chan$/) next
      spool = substr($0, 13)
      if (index(spool, dir) != 1) next
      print pid "\t" spool
    }' || true)"

  total=0; orphans=0; killed=""
  while IFS="$(printf '\t')" read -r pid spool; do
    if [ -z "${pid:-}" ]; then continue; fi
    total=$((total + 1))
    cur="$pid"
    orphan=1
    while :; do
      row="$(ps_row "$snapshot" "$cur")"
      if [ -z "$row" ]; then break; fi                # gone: nothing holds it
      parent="${row%% *}"
      if [ "$parent" -le 1 ]; then break; fi          # reached init: no launcher
      pcmd="$(ps_row "$snapshot" "$parent")"
      if [ -z "$pcmd" ]; then break; fi               # parent already reaped
      pcmd="${pcmd#* }"
      if ! is_monitor_chain_cmd "$pcmd"; then
        orphan=0                                      # a live, foreign launcher
        break
      fi
      cur="$parent"
    done
    if [ "$orphan" = 1 ]; then
      orphans=$((orphans + 1))
      echo "  orphan  pid $pid  ${spool##*/}"
      killed="$killed $pid"
    fi
  done <<EOF
$readers
EOF

  echo "parlay-monitor --reap: $total reader(s), $orphans orphaned, $((total - orphans)) with a live launcher"
  if [ "$orphans" = 0 ]; then return 0; fi
  if [ "$apply" != 1 ]; then
    echo "parlay-monitor --reap: dry run — nothing killed. Re-run with --apply."
    return 0
  fi
  for pid in $killed; do kill "$pid" 2>/dev/null || true; done
  /bin/sleep 1
  left=0
  for pid in $killed; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -9 "$pid" 2>/dev/null || true
      left=$((left + 1))
    fi
  done
  echo "parlay-monitor --reap: killed $orphans orphaned reader(s) ($left needed SIGKILL)"
}

HERE="$(cd "$(dirname "$0")" && pwd)"
ENSURE_UP="$HERE/../relay/deploy/ensure-up.sh"
RELAY_LIB="$HERE/../relay/deploy/lib.sh"

# --reap is a maintenance pass, not a stream: no agent, no relay, no enrollment.
# It runs before any of the setup below so a host with no relay at all can still
# be swept. Scope is the configured canonical runtime dir.
if [ "$REAP" = 1 ]; then
  if [ -r "$RELAY_LIB" ]; then
    # shellcheck source=../relay/deploy/lib.sh
    . "$RELAY_LIB"
  fi
  REAP_RUNTIME="${PARLAY_RELAY_RUNTIME:-}"
  if [ -z "$REAP_RUNTIME" ] && command -v parlay_relay_runtime_dir >/dev/null 2>&1; then
    REAP_RUNTIME="$(parlay_relay_runtime_dir)"
  fi
  if [ -z "$REAP_RUNTIME" ]; then
    REAP_RUNTIME="${TMPDIR:-/tmp}/parlay"
  fi
  reap_readers "$APPLY" "${REAP_RUNTIME%/}"
  exit 0
fi

[ -n "$AGENT" ] || { echo "parlay-monitor: --agent <id> is required" >&2; usage; }

# ── Never die quietly before streaming starts (robots-dcag) ───────────────────
# By the time this script runs, `parlay listen` has already registered and
# announced the agent with the Go server. If we then exit without reaching the stream,
# the panel shows a healthy agent whose event stream does not exist — it takes
# no directives for the rest of the session and nothing says so. Any exit from
# the setup phase therefore names itself, its code, and the consequence. `set
# -e` deaths land here too, which is exactly the case that went unreported.
# Cleared once `tail` is reached, since a stream ending later is a different
# (and visible) event.
#
# In --preflight mode NOTHING has been registered yet — this is the probe
# `parlay listen` runs BEFORE enrolling (issue #173) — so a setup failure there
# is a clean pre-enrollment diagnosis, not the registered-but-deaf trap. The
# consequence line is worded accordingly.
STREAMING=0
on_setup_exit() {
  local code=$?
  [ "$code" = 0 ] && return 0
  [ "$STREAMING" = 1 ] && return 0
  if [ "$PREFLIGHT" = 1 ]; then
    echo "parlay-monitor: FAILED during setup (exit $code) — '$AGENT' not reachable by the relay." >&2
    echo "parlay-monitor:   This ran BEFORE enrollment (--preflight): '$AGENT' was" >&2
    echo "parlay-monitor:   never registered, so it is not left deaf. Fix the relay" >&2
    echo "parlay-monitor:   condition above and re-run — nothing was registered." >&2
    echo "parlay-monitor:   (install the relay: tools/relay/deploy/install.sh)" >&2
    return 0
  fi
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

# ── Resolve the canonical relay runtime ───────────────────────────────────────
# The CLI resolves the upstream server before starting this monitor. The relay
# itself is a single canonical process, so every monitor uses the same runtime
# directory; an explicit runtime override remains available for hermetic tests.
if [ -r "$RELAY_LIB" ]; then
  # shellcheck source=../relay/deploy/lib.sh
  . "$RELAY_LIB"
fi
if command -v parlay_relay_runtime_dir >/dev/null 2>&1; then
  RUNTIME="$(parlay_relay_runtime_dir)"
else
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

# Ensure a relay is up before enrolling, so a monitor never dead-ends on a
# missing relay. ensure-up is idempotent and concurrency-safe: it no-ops if the
# canonical relay already answers /health, otherwise it starts the supervised
# or development relay and waits for /health. It respects
# PARLAY_RELAY_RUNTIME/SOCK via the same lib resolution.
if [ -x "$ENSURE_UP" ]; then
  ENSURE_RC=0
  "$ENSURE_UP" || ENSURE_RC=$?
  if [ "$ENSURE_RC" != 0 ]; then
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

# ── Preflight: exit before enroll, the relay is verified ready (issue #173) ──
# At this point the relay is up (or was started by ensure-up) and the socket
# exists. `parlay listen`/`parlay claim` run this as a
# PRE-enrollment probe so a fresh-clone user (no relay binary, so ensure-up
# failed above) exits with the diagnosis BEFORE the agent is registered — the
# register+announce then discovering a dead relay is the registered-but-deaf
# trap this closes. The stream path falls straight through to enroll below; only
# --preflight stops here.
if [ "$PREFLIGHT" = 1 ]; then
  echo "parlay-monitor: preflight OK — canonical relay is up for '$AGENT'" >&2
  exit 0
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
  /bin/sleep 0.1
done

echo "parlay-monitor: streaming '$AGENT' from $SPOOL" >&2
# Setup is over: past here, an exit is the stream ending (usually the harness
# killing us), not the silent registered-but-deaf failure the trap warns about.
STREAMING=1

# ── Evict any reader this channel already has (robots-3pvi) ──────────────────
# Enrollment is the one moment we know authoritatively who the channel's reader
# should be: us. Anything already tailing this spool is a previous session's
# leftover, and leaving it alive means every directive sent here is ALSO
# delivered to that dead session — the "multiple readers race the same channel"
# half of the defect (shape.chan had 20).
STALE_READERS="$(readers_of "$SPOOL" | tr '\n' ' ' || true)"
if [ -n "$(printf '%s' "$STALE_READERS" | tr -d '[:space:]')" ]; then
  echo "parlay-monitor: '$AGENT' already has reader(s) on $SPOOL:${STALE_READERS% }" >&2
  echo "parlay-monitor:   evicting them — a channel gets exactly one reader, or a" >&2
  echo "parlay-monitor:   stale session is woken by messages meant for this one." >&2
  for _p in $STALE_READERS; do kill "$_p" 2>/dev/null || true; done
  /bin/sleep 0.3
  for _p in $(readers_of "$SPOOL"); do kill -9 "$_p" 2>/dev/null || true; done
fi

# 2. Stream. Flags:
#      -n0  start at end-of-file — no replay of already-consumed spool lines
#      -F   follow by name; re-open on truncate/rotate/recreate. This is the
#           "channel re-open after relay restart" correctness requirement: if the
#           relay is restarted and the spool is recreated, tail -F reattaches
#           without the monitor needing to restart.
#
# --notify-safe pipes tail through awk that caps each over-budget line and appends
# a self-describing pointer (id survives — it sits in the first ~55 chars). fflush
# after every line keeps the Monitor tool's per-line event contract intact. This
# costs one extra awk process; only harness agents that opt in pay it.
#
# WHY tail is no longer `exec`d (robots-3pvi): `exec` bought a ~1.2MB footprint by
# deleting the only process that could ever clean tail up. This shell now stays as
# a supervisor (~2MB more per agent, paid once) and does two things `exec` made
# impossible: it `wait`s on the stream so a trapped signal is acted on immediately
# (a signal arriving during a FOREGROUND command is deferred by bash until that
# command finishes — which for `tail -F` is never), and it kills the reader on the
# way out. Raw stdout completeness is unchanged: tail still writes straight to
# this process's stdout.
if [ "$NOTIFY_SAFE" = 1 ]; then
  BUDGET="${PARLAY_NOTIFY_BUDGET:-400}"
  tail -n0 -F "$SPOOL" | awk -v BUD="$BUDGET" '
    {
      if (length($0) > BUD) {
        printf "%s ⟪+%d chars truncated for notification — run: parlay history 30 --full⟫\n", substr($0, 1, BUD), length($0) - BUD
      } else {
        print $0
      }
      fflush()
    }' &
else
  tail -n0 -F "$SPOOL" &
fi
STREAM_PID=$!

# The reader's own pid. For a backgrounded pipeline `$!` is the LAST process
# (awk), and killing awk only leaves tail writing into a closed pipe — it dies at
# its next write, which on a quiet channel is never. So address tail directly.
# Scoped to this shell's own children so a reader that survived eviction can
# never be mistaken for ours — the watchdog and the teardown both act on this
# pid alone.
READER_PID=""
for _ in 1 2 3 4 5; do
  READER_PID="$(readers_of "$SPOOL" "$$" | head -1 || true)"
  if [ -n "$READER_PID" ]; then break; fi
  /bin/sleep 0.2
done
if [ -z "$READER_PID" ]; then
  echo "parlay-monitor: could not identify the reader process for '$AGENT' — it will" >&2
  echo "parlay-monitor:   still stream, but cannot be reaped if this session dies." >&2
fi

stream_teardown() {
  if [ -n "${WATCHDOG_PID:-}" ]; then kill "$WATCHDOG_PID" 2>/dev/null || true; fi
  if [ -n "${READER_PID:-}" ]; then kill "$READER_PID" 2>/dev/null || true; fi
  if [ -n "${STREAM_PID:-}" ]; then kill "$STREAM_PID" 2>/dev/null || true; fi
  return 0
}
trap 'stream_teardown' EXIT
trap 'stream_teardown; exit 143' TERM INT HUP

# ── Watchdog: the reader dies with its launcher ──────────────────────────────
# Two failure modes, one loop, and it must survive both of the processes above
# it because either can be killed without the other:
#   * this supervisor is gone  → nothing is left to reap the reader at all
#   * our launcher is gone     → we were reparented; the session that wanted
#                                this stream no longer exists
# It only ever kills the ONE pid it was told about, so it can never touch a
# newer reader that enrolled on this channel after us. Opt out with
# PARLAY_MONITOR_NO_ORPHAN_EXIT=1 (deliberate daemonization).
if [ -n "$READER_PID" ] && [ "${PARLAY_MONITOR_NO_ORPHAN_EXIT:-0}" != 1 ]; then
  (
    # Own traps only: an inherited EXIT trap here would have this subshell run
    # the supervisor's teardown on its own way out.
    trap - EXIT TERM INT HUP
    sup=$$ ; reader="$READER_PID" ; launcher="$PPID"
    while :; do
      /bin/sleep "$WATCH_INTERVAL"
      kill -0 "$reader" 2>/dev/null || exit 0
      why=""
      if ! kill -0 "$sup" 2>/dev/null; then
        why="supervisor (pid $sup) exited"
      else
        now="$(ps -o ppid= -p "$sup" 2>/dev/null | tr -d '[:space:]' || true)"
        if [ -n "$now" ] && [ "$now" != "$launcher" ]; then
          why="launcher (pid $launcher) is gone — reparented to $now"
        fi
      fi
      if [ -n "$why" ]; then
        echo "parlay-monitor: $why; stopping the reader for '$AGENT' (robots-3pvi)" >&2
        kill "$reader" 2>/dev/null || true
        /bin/sleep 1
        kill -9 "$reader" 2>/dev/null || true
        exit 0
      fi
    done
  ) &
  WATCHDOG_PID=$!
fi

# `wait`, not a foreground pipeline: this is the one construct bash interrupts to
# run a trap, so TERM/INT/HUP tear the reader down instead of queueing behind a
# `tail -F` that never returns.
STREAM_CODE=0
wait "$STREAM_PID" || STREAM_CODE=$?
exit "$STREAM_CODE"
