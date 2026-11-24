#!/usr/bin/python3
"""Pipeline state for the voice page, with per-stage timing.

    vstage.py session <name> <id> <cwd>
    vstage.py begin <text>
    vstage.py set <key> <status> <detail> [ms]
    vstage.py finish --t0 MS --t1 MS --out FILE --err FILE
                     --kokoro-from BYTES --whisper-from BYTES --client-from BYTES

`finish` is the whole post-mortem of one turn. `voicemode converse` reports no
timing for the LiveKit transport, so the numbers are recovered from what every
stage already writes: converse's own stderr (when it entered the room), the
Kokoro log (synthesis time), the Whisper log (seconds of speech received) and
the phone page's event log (when the phone saw the bot start and stop talking).
It fills the stage list the page shows, appends one line to turn-timing.jsonl,
and prints the heard text (or the failure reason) on stdout. It exits 0 when
something was heard, 3 when no phone was on the line, 4 when nothing was heard.

State lives in $VOICEMODE_DIR (default ~/.voicemode); the Kokoro and Whisper
logs are read from $VOICE_LOG_DIR (default $VOICEMODE_DIR/logs).
"""
import argparse
import json
import os
import re
import sys
import time

D = os.environ.get("VOICEMODE_DIR") or os.path.join(os.path.expanduser("~"), ".voicemode")
LOGS = os.environ.get("VOICE_LOG_DIR") or os.path.join(D, "logs")
F = os.path.join(D, "turns.json")
STAGES = [
    ("claude_text", "Claude wrote the reply"),
    ("phone_link", "Phone is on the line"),
    ("tts", "Speech generated (Kokoro)"),
    ("playback", "Reply spoken to your phone"),
    ("mic", "Your voice reached the server"),
    ("stt", "Speech turned into text (Whisper)"),
    ("delivered", "Text delivered to this Claude chat"),
    ("thinking", "Claude is thinking about it"),
]
NO_PHONE = ("No active LiveKit rooms found", "No participants in LiveKit room")
ANSI = re.compile(r"\x1b\[[0-9;]*m")


def load():
    try:
        with open(F) as f:
            return json.load(f)
    except Exception:
        return {"session": {}, "turns": []}


def save(d):
    with open(F + ".tmp", "w") as f:
        f.write(json.dumps(d))
    os.replace(F + ".tmp", F)


def set_stage(d, key, status, detail="", ms=None):
    if not d.get("turns"):
        return
    for s in d["turns"][-1]["stages"]:
        if s["key"] == key:
            s["status"] = status
            s["at"] = time.strftime("%H:%M:%S")
            s["detail"] = detail
            if ms is not None:
                s["ms"] = int(ms)
            else:
                s.pop("ms", None)


def tail_from(path, offset):
    """Text a log gained since `offset` bytes. A rotated (shorter) log reads whole."""
    try:
        with open(path, "rb") as f:
            f.seek(0, 2)
            if offset > f.tell():
                offset = 0
            f.seek(offset)
            return ANSI.sub("", f.read().decode("utf-8", "replace"))
    except OSError:
        return ""


def log_ms(line):
    """'2026-11-24 12:02:41,018 - ...' (local time) -> epoch milliseconds."""
    m = re.match(r"(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d),(\d{3})", line)
    if not m:
        return None
    return int(time.mktime(time.strptime(m.group(1), "%Y-%m-%d %H:%M:%S")) * 1000) + int(m.group(2))


def iso_ms(s):
    """'2026-11-23T23:01:53.282Z' -> epoch milliseconds."""
    m = re.match(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,3}))?Z$", s or "")
    if not m:
        return None
    import calendar
    base = calendar.timegm(time.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S")) * 1000
    return base + int((m.group(2) or "0").ljust(3, "0"))


def heard_text(raw):
    """converse prints only the result, but a long answer comes back on several
    lines. Taking the last line alone (the old `tail -1`) kept only its tail."""
    keep = []
    for ln in raw.splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("\u26a0") or ln.startswith("Run 'voicemode deps'"):
            continue
        keep.append(ln)
    return " ".join(keep)


def refusal(raw):
    """voicemode's own reason for not running at all (it prints it and exits)."""
    for ln in raw.splitlines():
        if "Missing core dependencies" in ln:
            return "voicemode did not run: " + ln.strip().lstrip("\u26a0\ufe0f ").strip()
    return ""


def jl(who, text):
    with open(os.path.join(D, "page-log.jsonl"), "a") as f:
        f.write(json.dumps({"t": time.strftime("%H:%M:%S"), "who": who, "text": text}) + "\n")


def page_state(s):
    with open(os.path.join(D, "page-state.txt"), "w") as f:
        f.write(s + "\n")


def secs(ms):
    return f"{ms / 1000:.1f}s"


def finish(a):
    d = load()
    try:
        with open(a.out, encoding="utf-8", errors="replace") as f:
            raw = f.read()
    except OSError:
        raw = ""
    heard = heard_text(raw)
    broken = "" if heard else refusal(raw)
    try:
        with open(a.err, encoding="utf-8", errors="replace") as f:
            err_lines = f.read().splitlines()
    except OSError:
        err_lines = []

    total_ms = max(0, a.t1 - a.t0)
    # When the bot dialled into the room: the room was found, so the phone is there.
    link_at = next((log_ms(l) for l in err_lines if "signal_stream" in l and "connecting to" in l), None)
    link_ms = max(0, link_at - a.t0) if link_at else None

    kok = tail_from(os.path.join(LOGS, "kokoro", "kokoro.log"), a.kokoro_from)
    synth = [float(x) for x in re.findall(r"Synthesis completed in ([0-9.]+)ms", kok)]
    tts_ms = sum(synth) if synth else None

    whi = tail_from(os.path.join(LOGS, "whisper", "whisper.err.log"), a.whisper_from)
    speech = [float(x) for x in re.findall(r"processing '[^']*' \(\d+ samples, ([0-9.]+) sec\)", whi)]

    # The phone's own account of the turn.
    speaking_at = listening_at = None
    got_track = sound_blocked = False
    for ln in tail_from(os.path.join(D, "page-client.log"), a.client_from).splitlines():
        try:
            ev = json.loads(ln)
        except ValueError:
            continue
        if ev.get("event") == "track_subscribed" and str(ev.get("detail", "")).strip('"').startswith("voice-mode-bot"):
            got_track = True
        if ev.get("event") == "bot_audio":
            # {"on": bool, "sound": bool}: the bot's voice started/stopped arriving
            # at the page, and whether that browser was allowed to play sound.
            try:
                det = json.loads(ev.get("detail") or "{}")
            except ValueError:
                det = {}
            at = iso_ms(ev.get("at"))
            if det.get("on"):
                speaking_at = speaking_at or at
                sound_blocked = sound_blocked or det.get("sound") is False
            elif speaking_at:
                listening_at = at  # the LAST stop: a pause between sentences is not the end
    play_ms = None
    if speaking_at and listening_at and listening_at >= speaking_at and not sound_blocked:
        play_ms = listening_at - speaking_at
    listen_from = listening_at or ((link_at + int(tts_ms)) if (link_at and tts_ms) else None)
    listen_ms = max(0, a.t1 - listen_from) if listen_from else None

    def playback(default_detail):
        if play_ms is not None:
            return f"the phone page confirmed it played for {secs(play_ms)}", play_ms
        if sound_blocked:
            return "the phone received it but its sound is off: press 'Tap to turn sound on' on the page", None
        if got_track:
            return "the phone page received the audio", None
        return default_detail, None

    result = "ok"
    if broken:
        # Nothing was attempted: say so, instead of blaming the phone or the mic.
        result = "error"
        heard = broken
        set_stage(d, "phone_link", "fail", broken + ". Run it with ffmpeg and portaudio on PATH.", total_ms)
        set_stage(d, "tts", "skip", "not attempted")
        set_stage(d, "playback", "skip", "not attempted")
        page_state("idle")
        jl("system", broken)
    elif heard in NO_PHONE:
        result = "no_phone"
        set_stage(d, "phone_link", "fail", heard + ": the phone page is closed, asleep, or not started", total_ms)
        set_stage(d, "tts", "skip", "not attempted")
        set_stage(d, "playback", "skip", "no phone to play to")
        page_state("idle")
        jl("system", heard)
    elif heard == "" or heard.startswith("No response within") or heard.startswith("LiveKit error"):
        result = "no_speech" if not heard.startswith("LiveKit error") else "error"
        set_stage(d, "phone_link", "ok", "phone was in the room", link_ms)
        if tts_ms is not None:
            set_stage(d, "tts", "ok", f"{len(synth)} piece(s) synthesised", tts_ms)
        else:
            set_stage(d, "tts", "fail", "no synthesis seen in Kokoro log")
        det, ms = playback("sent to the phone; the phone page did not confirm playing it")
        set_stage(d, "playback", "ok", det, ms)
        why = heard or "no audio heard"
        got = f" ({sum(speech):.1f}s of sound reached Whisper but was not accepted as speech)" if speech else ""
        set_stage(d, "mic", "fail", f"{why}: nothing loud enough arrived, or you did not speak{got}", listen_ms)
        set_stage(d, "stt", "skip", "no audio to transcribe")
        page_state("idle")
        jl("system", why)
    else:
        set_stage(d, "phone_link", "ok", "phone was in the room", link_ms)
        set_stage(d, "tts", "ok", f"{len(synth)} piece(s) synthesised" if synth else "done", tts_ms)
        det, ms = playback("spoken to the phone")
        set_stage(d, "playback", "ok", det, ms)
        set_stage(d, "mic", "ok",
                  f"{sum(speech):.1f}s of speech received" if speech else "audio received from the phone", listen_ms)
        set_stage(d, "stt", "ok", "heard: " + heard)
        set_stage(d, "delivered", "ok", "received by this Claude chat at " + time.strftime("%H:%M:%S"), total_ms)
        set_stage(d, "thinking", "run", "Claude is working on it")
        jl("you", heard)
        page_state("thinking")
    if d.get("turns"):
        d["turns"][-1]["total_ms"] = total_ms
    save(d)

    rec = {
        "at": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "result": result, "total_ms": total_ms,
        "link_ms": link_ms, "tts_ms": int(tts_ms) if tts_ms is not None else None,
        "playback_ms": play_ms, "listen_ms": listen_ms,
        "speech_s": round(sum(speech), 1) if speech else None,
        "phone_received_audio": got_track or speaking_at is not None,
        "phone_confirmed_playback": play_ms is not None,
        "said": d["turns"][-1]["said"] if d.get("turns") else "", "heard": heard,
    }
    with open(os.path.join(D, "turn-timing.jsonl"), "a") as f:
        f.write(json.dumps(rec) + "\n")
    print(heard)
    return {"ok": 0, "no_phone": 3}.get(result, 4)


def main():
    a = sys.argv[1] if len(sys.argv) > 1 else ""
    if a == "finish":
        p = argparse.ArgumentParser()
        p.add_argument("--t0", type=int, required=True)
        p.add_argument("--t1", type=int, required=True)
        p.add_argument("--out", required=True)
        p.add_argument("--err", required=True)
        p.add_argument("--kokoro-from", type=int, default=0)
        p.add_argument("--whisper-from", type=int, default=0)
        p.add_argument("--client-from", type=int, default=0)
        sys.exit(finish(p.parse_args(sys.argv[2:])))
    d = load()
    if a == "session":
        d["session"] = {"name": sys.argv[2], "id": sys.argv[3], "cwd": sys.argv[4], "host": os.uname().nodename}
    elif a == "begin":
        t = {"id": int(time.time()), "said": sys.argv[2], "at": time.strftime("%H:%M:%S"),
             "stages": [{"key": k, "label": l, "status": "wait", "at": "", "detail": ""} for k, l in STAGES]}
        d["turns"] = (d.get("turns", []) + [t])[-6:]
    elif a == "set":
        ms = int(sys.argv[5]) if len(sys.argv) > 5 and sys.argv[5].lstrip("-").isdigit() else None
        set_stage(d, sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) > 4 else "", ms)
    else:
        sys.exit(__doc__)
    save(d)


if __name__ == "__main__":
    main()
