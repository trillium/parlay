#!/usr/bin/env bash
# install.sh: put the phone voice path from this directory into the live machine.
#
# Two destinations:
#   ~/.voicemode/            vturn.sh, vstage.py, tlsproxy.js, voice-health,
#                            voice-synth-phone.py, voice-roundtrip.test.sh
#   <voicemode>/frontend/    the three files under frontend/app that this
#                            directory overrides (the rest of the page is the
#                            stock voicemode frontend)
#
# Every file it replaces is first COPIED to ~/.voicemode/backups/<time>/, and
# nothing is ever deleted, so a bad install is undone by copying a backup back.
#
# Usage:
#   install.sh                     copy the files
#   install.sh --build             ...then rebuild the page (next build)
#   install.sh --build --restart   ...then restart the page server on :3000
#   install.sh --build --restart --restart-proxy
#                                  the whole thing, first time or after a reboot:
#                                  copy, build, page server and TLS forwarder
#   install.sh --restart           only restart the page server
#   install.sh --restart-proxy     only restart the TLS forwarder (tlsproxy.js)
#   install.sh --status            say which installed files differ from this directory
#
# A rebuilt page is only served after a restart. `voicemode update` / a uv tool
# upgrade replaces the frontend directory: re-run `--build --restart` after one
# (voice-health reports it as "page build ... old page").
#
# Environment:
#   VOICEMODE_DIR        where the scripts go        (default ~/.voicemode)
#   VOICE_FRONTEND_DIR   the voicemode frontend      (default: asked of the voice-mode tool)
#   VOICE_PYTHON         the voice-mode tool python  (default ~/.local/share/uv/tools/voice-mode/bin/python)
#   VOICE_FRONTEND_PORT  page server port            (default 3000)
#   VOICE_FRONTEND_HOST  page server bind address    (default 127.0.0.1)
#   VOICE_TLS_PAGE_PORT / VOICE_TLS_LIVEKIT_PORT / VOICE_LIVEKIT_PORT
#                        forwarder ports             (default 3443 / 7443 / 7880)
set -euo pipefail

HERE="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
D="${VOICEMODE_DIR:-$HOME/.voicemode}"
PY="${VOICE_PYTHON:-$HOME/.local/share/uv/tools/voice-mode/bin/python}"
PORT="${VOICE_FRONTEND_PORT:-3000}"
# Loopback, not 0.0.0.0. The phone never talks to this port (it needs TLS, so it
# goes through the forwarder, which dials 127.0.0.1), and a wildcard bind fails
# with EADDRINUSE whenever some other project's dev server already holds the
# same port number on the IPv6 wildcard. A loopback bind always wins that race.
BIND="${VOICE_FRONTEND_HOST:-127.0.0.1}"
TLS_PAGE="${VOICE_TLS_PAGE_PORT:-3443}"
TLS_LK="${VOICE_TLS_LIVEKIT_PORT:-7443}"
LK="${VOICE_LIVEKIT_PORT:-7880}"
SCRIPTS="vturn.sh vstage.py tlsproxy.js voice-health voice-synth-phone.py voice-roundtrip.test.sh"
PAGE_FILES="app/page.tsx app/api/connection-details/route.ts app/api/voice-log/route.ts"

frontend_dir() {
  if [ -n "${VOICE_FRONTEND_DIR:-}" ]; then printf '%s\n' "$VOICE_FRONTEND_DIR"; return; fi
  local found=""
  found="$("$PY" -c 'import os, voice_mode; print(os.path.join(os.path.dirname(voice_mode.__file__), "frontend"))' 2>/dev/null)" || found=""
  printf '%s\n' "$found"
}

FE="$(frontend_dir)"
if [ -z "$FE" ] || [ ! -f "$FE/package.json" ]; then
  echo "install.sh: cannot find the voicemode frontend (looked at '${FE:-nothing}')." >&2
  echo "  fix: install voicemode (uv tool install voice-mode), or set VOICE_FRONTEND_DIR" >&2
  exit 1
fi

do_build=0; do_restart=0; do_proxy=0; do_copy=0; do_status=0
for arg in "$@"; do
  case "$arg" in
    --build) do_build=1 ;;
    --restart) do_restart=1 ;;
    --restart-proxy) do_proxy=1 ;;
    --status) do_status=1 ;;
    -h|--help) /usr/bin/sed -n '2,/^set -euo/p' "$0" | /usr/bin/sed '$d' | /usr/bin/sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "install.sh: unknown argument $arg" >&2; exit 64 ;;
  esac
done
# Files are copied unless the call is ONLY a restart or a status report.
if [ "$do_build" = 1 ] || [ "$#" = 0 ]; then do_copy=1; fi

same() { [ -f "$2" ] && /usr/bin/cmp -s "$1" "$2"; }

# detach <directory> <log> <command...>: start a long-running server that
# survives this script. Three things a bare `cmd &` gets wrong here:
#   - it leaves a copy of this shell behind holding the caller's stdout, so
#     `install.sh --restart | tee log` never returns;
#   - it stays in the caller's process group, so whatever stops the caller
#     (a closed terminal, a timed-out agent tool call) stops the server too;
#   - it keeps the caller's stdin.
# So: redirect the whole subshell, give it a session of its own, and exec.
detach() {
  local dir="$1" log="$2"; shift 2
  ( cd "$dir" && exec /usr/bin/perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV or die "exec $ARGV[0]: $!\n"' "$@" ) \
    >> "$log" 2>&1 < /dev/null &
}

if [ "$do_status" = 1 ]; then
  drift=0
  for f in $SCRIPTS; do
    if same "$HERE/$f" "$D/$f"; then echo "same     $D/$f"; else echo "DIFFERS  $D/$f"; drift=1; fi
  done
  for f in $PAGE_FILES; do
    if same "$HERE/frontend/$f" "$FE/$f"; then echo "same     $FE/$f"; else echo "DIFFERS  $FE/$f"; drift=1; fi
  done
  [ "$drift" = 0 ] && echo "installed files match this directory" || echo "run: $0 --build --restart"
  exit "$drift"
fi

BACKUP="$D/backups/$(/bin/date +%Y%m%d-%H%M%S)"
put() { # put <source> <destination> <mode>
  local src="$1" dst="$2" mode="$3"
  if same "$src" "$dst"; then echo "same     $dst"; return; fi
  if [ -e "$dst" ]; then
    local keep="$BACKUP/${dst#/}"
    /bin/mkdir -p "$(dirname -- "$keep")"
    /bin/cp -p "$dst" "$keep"
  fi
  /bin/mkdir -p "$(dirname -- "$dst")"
  /bin/cp "$src" "$dst.new.$$"
  /bin/chmod "$mode" "$dst.new.$$"
  /bin/mv -f "$dst.new.$$" "$dst"
  echo "updated  $dst"
}

if [ "$do_copy" = 1 ]; then
  /bin/mkdir -p "$D" "$D/logs/frontend"
  for f in $SCRIPTS; do
    mode=755; [ "$f" = tlsproxy.js ] && mode=644
    put "$HERE/$f" "$D/$f" "$mode"
  done
  for f in $PAGE_FILES; do put "$HERE/frontend/$f" "$FE/$f" 644; done
  [ -d "$BACKUP" ] && echo "replaced files were saved under $BACKUP"
fi

if [ "$do_build" = 1 ]; then
  command -v npm >/dev/null 2>&1 || { echo "install.sh: npm is not on PATH; cannot build the page" >&2; exit 1; }
  echo "building the page in $FE (takes a minute)"
  ( cd "$FE" && npm exec -- next build ) > "$D/logs/frontend/build.log" 2>&1 || {
    /usr/bin/tail -30 "$D/logs/frontend/build.log" >&2
    echo "install.sh: the page build failed; full log: $D/logs/frontend/build.log" >&2
    exit 1
  }
  echo "built    $FE/.next"
fi

# The page server is whatever listens on the port FROM the frontend directory.
# Matched by port and working directory, never by a command-line pattern, so an
# unrelated server on the same port number (another project's dev server bound
# to IPv6, say) is left alone.
page_server_pids() {
  local pid cwd
  for pid in $(/usr/sbin/lsof -nP -tiTCP:"$PORT" -sTCP:LISTEN 2>/dev/null || true); do
    cwd="$(/usr/sbin/lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | /usr/bin/sed -n 's/^n//p')" || cwd=""
    [ "$cwd" = "$FE" ] && printf '%s\n' "$pid"
  done
  return 0
}

if [ "$do_restart" = 1 ]; then
  command -v npm >/dev/null 2>&1 || { echo "install.sh: npm is not on PATH; cannot start the page server" >&2; exit 1; }
  [ -f "$FE/.next/BUILD_ID" ] || { echo "install.sh: the page has never been built; run with --build" >&2; exit 1; }
  for pid in $(page_server_pids); do
    echo "stopping page server pid $pid"
    kill "$pid" 2>/dev/null || true
  done
  n=0
  while [ -n "$(page_server_pids)" ] && [ "$n" -lt 50 ]; do /usr/bin/perl -e 'select(undef, undef, undef, 0.2)'; n=$((n + 1)); done
  if [ -n "$(page_server_pids)" ]; then
    echo "install.sh: the old page server would not stop (pid $(page_server_pids | /usr/bin/tr '\n' ' '))" >&2; exit 1
  fi
  /bin/mkdir -p "$D/logs/frontend"
  VOICEMODE_DIR="$D" detach "$FE" "$D/logs/frontend/frontend.log" npm exec -- next start -H "$BIND" -p "$PORT"
  n=0; up=0
  while [ "$n" -lt 150 ]; do
    if /usr/bin/curl -fsS -m 2 "http://127.0.0.1:$PORT/api/voice-log" >/dev/null 2>&1 && [ -n "$(page_server_pids)" ]; then up=1; break; fi
    /usr/bin/perl -e 'select(undef, undef, undef, 0.2)'; n=$((n + 1))
  done
  if [ "$up" = 1 ]; then
    echo "started  page server on :$PORT (pid $(page_server_pids | /usr/bin/tr '\n' ' '))"
  else
    /usr/bin/tail -20 "$D/logs/frontend/frontend.log" >&2 || true
    echo "install.sh: the page server did not come up; log: $D/logs/frontend/frontend.log" >&2
    exit 1
  fi
fi

if [ "$do_proxy" = 1 ]; then
  command -v node >/dev/null 2>&1 || { echo "install.sh: node is not on PATH; cannot start the forwarder" >&2; exit 1; }
  [ -f "$D/tlsproxy.js" ] || { echo "install.sh: $D/tlsproxy.js is missing; run install.sh first" >&2; exit 1; }
  # The forwarder is whatever listens on the TLS page port AND is tlsproxy.js,
  # judged from that one process's own command line.
  for pid in $(/usr/sbin/lsof -nP -tiTCP:"$TLS_PAGE" -sTCP:LISTEN 2>/dev/null || true); do
    cmd="$(/bin/ps -o command= -p "$pid" 2>/dev/null)" || cmd=""
    case "$cmd" in
      *tlsproxy.js*) echo "stopping forwarder pid $pid"; kill "$pid" 2>/dev/null || true ;;
      *) echo "install.sh: port $TLS_PAGE is held by something that is not the forwarder (pid $pid: $cmd)" >&2; exit 1 ;;
    esac
  done
  n=0
  while [ -n "$(/usr/sbin/lsof -nP -tiTCP:"$TLS_PAGE" -sTCP:LISTEN 2>/dev/null || true)" ] && [ "$n" -lt 50 ]; do
    /usr/bin/perl -e 'select(undef, undef, undef, 0.2)'; n=$((n + 1))
  done
  VOICEMODE_DIR="$D" detach "$D" "$D/logs/tlsproxy.log" node "$D/tlsproxy.js" "$TLS_PAGE:$PORT" "$TLS_LK:$LK"
  n=0; up=0
  while [ "$n" -lt 50 ]; do
    if [ -n "$(/usr/sbin/lsof -nP -tiTCP:"$TLS_PAGE" -sTCP:LISTEN 2>/dev/null || true)" ]; then up=1; break; fi
    /usr/bin/perl -e 'select(undef, undef, undef, 0.2)'; n=$((n + 1))
  done
  if [ "$up" = 1 ]; then echo "started  forwarder :$TLS_PAGE -> :$PORT, :$TLS_LK -> :$LK"
  else /usr/bin/tail -20 "$D/logs/tlsproxy.log" >&2 || true; echo "install.sh: the forwarder did not come up; log: $D/logs/tlsproxy.log" >&2; exit 1; fi
fi

echo "next: $D/voice-health"
