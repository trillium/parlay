#!/bin/bash
# One spoken turn through the phone page, with per-stage status and timing for
# the page. Prints what the phone said back (or the failure reason) on stdout.
# Exit 0 = heard something; 3 = no phone on the line; 4 = phone there, nothing heard.
#
# Usage: vturn.sh "text to say" [listen_seconds]
#
# Environment:
#   VOICEMODE_DIR   where the page's state lives        (default ~/.voicemode)
#   VOICE_LOG_DIR   Kokoro/Whisper logs, read for timing (default $VOICEMODE_DIR/logs)
#   VOICEMODE_BIN   the voicemode CLI                   (default ~/.local/bin/voicemode)
#   VOICE_ROOM      the room to talk in (default voice_assistant_room, the one
#                   room the phone page joins); the round-trip test sets it
#                   to stay off the real line
#   VOICE_SESSION_NAME / VOICE_SESSION_ID / VOICE_SESSION_CWD
#                   which chat the phone page says it is talking to; also read
#                   from $VOICEMODE_DIR/session.env when present
set -u
D="${VOICEMODE_DIR:-$HOME/.voicemode}"
VM="${VOICEMODE_BIN:-$HOME/.local/bin/voicemode}"
HERE="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
L="${VOICE_LOG_DIR:-$D/logs}"
vs() { /usr/bin/python3 "$HERE/vstage.py" "$@"; }
export VOICEMODE_DIR="$D" VOICE_LOG_DIR="$L"
# shellcheck disable=SC1091
[ -r "$D/session.env" ] && . "$D/session.env"
now_ms() { /usr/bin/perl -MTime::HiRes=time -e 'printf "%d\n", time * 1000'; }
size() { if [ -f "$1" ]; then /usr/bin/wc -c < "$1" | /usr/bin/tr -d ' '; else echo 0; fi; }

if [ $# -lt 1 ] || [ -z "$1" ]; then echo "usage: vturn.sh \"text to say\" [listen_seconds]" >&2; exit 2; fi
SAY="$1"; LISTEN="${2:-20}"

vs session "${VOICE_SESSION_NAME:-Claude Code}" "${VOICE_SESSION_ID:-}" "${VOICE_SESSION_CWD:-$PWD}"
vs begin "$SAY"
vs set claude_text ok "$SAY"
/usr/bin/python3 -c 'import json,sys,time;print(json.dumps({"t":time.strftime("%H:%M:%S"),"who":"claude","text":sys.argv[1]}))' "$SAY" >> "$D/page-log.jsonl"
vs set phone_link run "looking for the phone's voice room"
vs set tts run "generating speech"
echo listening > "$D/page-state.txt"

KFROM=$(size "$L/kokoro/kokoro.log")
WFROM=$(size "$L/whisper/whisper.err.log")
CFROM=$(size "$D/page-client.log")
OUT="$D/last-converse.out"; ERR="$D/last-converse.err"
# Always name the room. Left to discover one, voicemode takes whichever room the
# server lists first, so a second room (a test, a stray page) could receive the
# turn and have its answer returned as if the phone had said it.
ROOM="${VOICE_ROOM:-voice_assistant_room}"

T0=$(now_ms)
"$VM" converse --transport livekit --room-name "$ROOM" -m "$SAY" -d "$LISTEN" > "$OUT" 2> "$ERR"
T1=$(now_ms)

vs finish --t0 "$T0" --t1 "$T1" --out "$OUT" --err "$ERR" \
  --kokoro-from "$KFROM" --whisper-from "$WFROM" --client-from "$CFROM"
