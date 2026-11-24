#!/bin/bash
# voice-roundtrip.test.sh: prove one spoken turn end to end, with no human.
#
#   text -> Kokoro speech -> LiveKit room -> a phone hears it
#        -> the phone answers -> Whisper -> text comes back out of vturn.sh
#
# MANUAL harness: it needs the live voice services (LiveKit, Kokoro, Whisper)
# and the voicemode tool, so it is not part of CI. It never touches the real
# phone line: the turn runs in a room of its own, against a synthetic phone
# (voice-synth-phone.py), and writes its page state into a scratch directory.
#
# Usage: voice-roundtrip.test.sh [--keep]     --keep leaves the scratch directory
# Exit:  0 every check passed; 1 a check failed; 2 the services are not up.
#
# Environment:
#   VOICE_PYTHON    python that has the LiveKit SDK
#                   (default ~/.local/share/uv/tools/voice-mode/bin/python)
#   VOICEMODE_BIN   the voicemode CLI                (default ~/.local/bin/voicemode)
#   VOICE_LOG_DIR   real Kokoro/Whisper logs         (default ~/.voicemode/logs)
#   VOICE_STT_URL   Whisper                          (default http://127.0.0.1:2022)
set -u

# Self-isolate: system tools FIRST (so a developer shim cannot stand in for one),
# no ambient shell hooks, no inherited state directory that would point the turn
# at the real page. The caller's PATH stays on the end: voicemode refuses to run
# without ffmpeg and portaudio, which live wherever the package manager put them.
PATH="/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"; export PATH
unset BASH_ENV ENV CDPATH VOICE_ROOM
HERE="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
PY="${VOICE_PYTHON:-$HOME/.local/share/uv/tools/voice-mode/bin/python}"
VOICEMODE_BIN="${VOICEMODE_BIN:-$HOME/.local/bin/voicemode}"; export VOICEMODE_BIN
VOICE_LOG_DIR="${VOICE_LOG_DIR:-$HOME/.voicemode/logs}"; export VOICE_LOG_DIR
STT="${VOICE_STT_URL:-http://127.0.0.1:2022}"
KEEP=0; [ "${1:-}" = "--keep" ] && KEEP=1

WORK="$(/usr/bin/mktemp -d "${TMPDIR:-/tmp}/voice-roundtrip.XXXXXX")" || exit 2
export VOICEMODE_DIR="$WORK"
ROOM="voice_roundtrip_test_$$"
ASK="This is a voice check. Please read the test phrase aloud now."
LINE="The quick brown fox jumps over the lazy dog near the river bank."
PHONE_PID=""

pass=0; fail=0
ok()  { pass=$((pass + 1)); printf 'ok   %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL %s\n' "$1"; [ -n "${2:-}" ] && printf '     %s\n' "$2"; }
finish() {
  if [ -n "$PHONE_PID" ] && kill -0 "$PHONE_PID" 2>/dev/null; then kill "$PHONE_PID" 2>/dev/null; fi
  if [ "$KEEP" = 1 ] || [ "$fail" -gt 0 ]; then printf 'scratch kept: %s\n' "$WORK"; fi
}
trap finish EXIT

# How much of the EXPECTED sentence shows up in what was actually heard (0-100).
overlap() {
  /usr/bin/python3 -c '
import re, sys
from collections import Counter
want = Counter(re.findall(r"[a-z0-9]+", sys.argv[1].lower()))
got = Counter(re.findall(r"[a-z0-9]+", sys.argv[2].lower()))
total = sum(want.values())
print(int(100 * sum((want & got).values()) / total) if total else 0)' "$1" "$2"
}
field() { /usr/bin/python3 -c '
import json, sys
try:
    v = json.loads(sys.argv[1])
    for k in sys.argv[2].split("."):
        v = v[k]
    print(v)
except Exception:
    print("")' "$1" "$2"; }

echo "== 0. tools and services"
for need in "$PY" "$VOICEMODE_BIN" "$HERE/vturn.sh" "$HERE/vstage.py" "$HERE/voice-synth-phone.py" "$HERE/voice-health"; do
  if [ ! -e "$need" ]; then echo "missing: $need" >&2; exit 2; fi
done
if ! "$HERE/voice-health" --engines-only > "$WORK/health.txt" 2>&1; then
  /bin/cat "$WORK/health.txt"; echo "services are not up: nothing to test" >&2; exit 2
fi
ok "voice-health --engines-only: LiveKit, Kokoro and Whisper are up"

echo "== 1. the turn script reads a result correctly (no services involved)"
# A long answer comes back from converse on several lines; the old script kept
# only the last one.
/usr/bin/python3 "$HERE/vstage.py" session "test" "" "$WORK" && /usr/bin/python3 "$HERE/vstage.py" begin "hello"
printf 'The quick brown fox jumps over the lazy dog near the river\n bank.\n' > "$WORK/multi.out"; : > "$WORK/empty.err"
got="$(/usr/bin/python3 "$HERE/vstage.py" finish --t0 1000 --t1 9000 --out "$WORK/multi.out" --err "$WORK/empty.err")"; rc=$?
if [ "$rc" = 0 ] && [ "$got" = "The quick brown fox jumps over the lazy dog near the river bank." ]; then
  ok "a two-line answer is returned whole, exit 0"
else bad "a two-line answer is returned whole, exit 0" "exit $rc, got: $got"; fi
/usr/bin/python3 "$HERE/vstage.py" begin "hello again"
printf 'No active LiveKit rooms found\n' > "$WORK/none.out"
got="$(/usr/bin/python3 "$HERE/vstage.py" finish --t0 1000 --t1 1400 --out "$WORK/none.out" --err "$WORK/empty.err")"; rc=$?
last="$(/usr/bin/tail -1 "$WORK/turn-timing.jsonl")"
if [ "$rc" = 3 ] && [ "$(field "$last" result)" = "no_phone" ]; then
  ok "'No active LiveKit rooms found' is reported as no phone, exit 3"
else bad "'No active LiveKit rooms found' is reported as no phone, exit 3" "exit $rc, record: $last"; fi

/usr/bin/python3 "$HERE/vstage.py" begin "hello once more"
printf '\342\232\240\357\270\217  Missing core dependencies: portaudio, ffmpeg\n   Run '"'"'voicemode deps'"'"' to install them\n' > "$WORK/deps.out"
got="$(/usr/bin/python3 "$HERE/vstage.py" finish --t0 1000 --t1 1300 --out "$WORK/deps.out" --err "$WORK/empty.err")"; rc=$?
last="$(/usr/bin/tail -1 "$WORK/turn-timing.jsonl")"
case "$got" in "voicemode did not run: Missing core dependencies"*) named=1 ;; *) named=0 ;; esac
if [ "$rc" = 4 ] && [ "$named" = 1 ] && [ "$(field "$last" result)" = "error" ]; then
  ok "voicemode refusing to start is reported as that, not as a silent phone, exit 4"
else bad "voicemode refusing to start is reported as that, not as a silent phone, exit 4" "exit $rc, got: $got"; fi

echo "== 2. a turn into a room with nobody in it must FAIL (so a pass below means something)"
got="$(VOICE_ROOM="${ROOM}_empty" "$HERE/vturn.sh" "Is anybody there?" 6)"; rc=$?
last="$(/usr/bin/tail -1 "$WORK/turn-timing.jsonl")"
case "$got" in "No response within"*|"No participants in LiveKit room"*|"No active LiveKit rooms found"*) known=1 ;; *) known=0 ;; esac
if [ "$rc" != 0 ] && [ "$known" = 1 ] && [ "$(field "$last" result)" != "ok" ]; then
  ok "empty room: exit $rc, result $(field "$last" result) ($got)"
else bad "empty room must fail with a reason, not an answer and not a crash" "exit $rc, got: $got"; fi

echo "== 3. full round trip against a synthetic phone in room $ROOM"
"$PY" "$HERE/voice-synth-phone.py" --room "$ROOM" --say "$LINE" --out "$WORK" \
  --ready-file "$WORK/phone.ready" --timeout 120 > "$WORK/phone.json" 2> "$WORK/phone.err" &
PHONE_PID=$!
n=0
while [ ! -s "$WORK/phone.ready" ] && kill -0 "$PHONE_PID" 2>/dev/null && [ "$n" -lt 300 ]; do
  /usr/bin/perl -e 'select(undef, undef, undef, 0.2)'; n=$((n + 1))
done
if [ ! -s "$WORK/phone.ready" ]; then
  bad "synthetic phone joined the room" "$(/usr/bin/tail -3 "$WORK/phone.err")"
  printf '\n%d passed, %d failed\n' "$pass" "$fail"; exit 1
fi
ok "synthetic phone joined the room and published a microphone"

heard="$(VOICE_ROOM="$ROOM" "$HERE/vturn.sh" "$ASK" 45)"; rc=$?
wait "$PHONE_PID"; phone_rc=$?; PHONE_PID=""
phone="$(/bin/cat "$WORK/phone.json")"
last="$(/usr/bin/tail -1 "$WORK/turn-timing.jsonl")"

pct="$(overlap "$LINE" "$heard")"
if [ "$rc" = 0 ] && [ "$pct" -ge 80 ]; then
  ok "speech -> text: vturn.sh returned what the phone said (${pct}% of the words): $heard"
else bad "speech -> text: vturn.sh returned what the phone said" "exit $rc, ${pct}% match, got: $heard"; fi

if [ "$phone_rc" = 0 ] && [ "$(field "$phone" bot_spoke)" = "True" ]; then
  ok "text -> speech: the phone received $(field "$phone" bot_audio_seconds)s of the bot's audio"
else bad "text -> speech: the phone received the bot's audio" "phone exit $phone_rc: $phone"; fi

# The strongest check on the outbound half: what the phone recorded, run back
# through Whisper, must be the sentence the turn was asked to say.
said_back="$(/usr/bin/curl -sS -m 90 -F "file=@$WORK/bot-heard-by-phone.wav" -F model=whisper-1 \
  "$STT/v1/audio/transcriptions" 2> "$WORK/whisper.err")"
said_text="$(field "$said_back" text | /usr/bin/tr '\n' ' ' | /usr/bin/sed 's/  */ /g; s/^ //; s/ $//')"
pct="$(overlap "$ASK" "$said_text")"
if [ "$pct" -ge 70 ]; then
  ok "what the phone heard transcribes back to the message (${pct}% of the words): $said_text"
else bad "what the phone heard transcribes back to the message" "${pct}% match, got: ${said_text:-$said_back}"; fi

if [ "$(field "$last" result)" = "ok" ] && [ -n "$(field "$last" tts_ms)" ] && [ "$(field "$last" tts_ms)" != "None" ] \
   && [ "$(field "$last" link_ms)" != "None" ] && [ "$(field "$last" total_ms)" -gt 0 ]; then
  ok "per-stage timing recorded: link $(field "$last" link_ms)ms, speech synthesis $(field "$last" tts_ms)ms, listening $(field "$last" listen_ms)ms, whole turn $(field "$last" total_ms)ms"
else bad "per-stage timing recorded" "$last"; fi

if [ -n "$heard" ] && [ "$(field "$(/usr/bin/tail -1 "$WORK/page-log.jsonl" 2>/dev/null)" who)" = "you" ] \
   && [ "$(field "$(/usr/bin/tail -1 "$WORK/page-log.jsonl")" text)" = "$heard" ]; then
  ok "the heard text was written to the page log as a 'you' line"
else bad "the heard text was written to the page log as a 'you' line" "$(/bin/cat "$WORK/page-log.jsonl" 2>/dev/null)"; fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" = 0 ]
