#!/usr/bin/env python3
"""A synthetic phone for the LiveKit voice path: joins a room the way the phone
page does, publishes a microphone track, records what the speech bot plays, and
"speaks" a Kokoro-generated line once the bot has finished talking.

It exists so the whole round trip (reply text -> TTS -> played on the phone ->
phone speech -> STT -> text back to the agent) can be exercised with no human
and no phone. Run it with the voice-mode tool's own interpreter, which already
carries the LiveKit SDK:

    ~/.local/share/uv/tools/voice-mode/bin/python voice-synth-phone.py \
        --room voice_roundtrip_test --say "the quick brown fox" --out /tmp/x

It prints ONE JSON object on stdout: per-stage epoch timestamps, whether the
bot's speech arrived, and the paths of the recorded audio. Exit 0 means the
phone side of the turn completed (bot heard, line spoken); the caller decides
whether the bot understood it.
"""
import argparse
import asyncio
import json
import os
import sys
import time
import urllib.request
import wave

import numpy as np
from livekit import api, rtc

RATE = 48000          # what a browser microphone publishes
FRAME = RATE // 100   # 10 ms
BOT_PREFIX = "voice-mode-bot"


def kokoro_pcm(base, text, voice):
    """Speech for the synthetic human, as 48 kHz mono int16."""
    req = urllib.request.Request(
        base.rstrip("/") + "/audio/speech",
        data=json.dumps({"model": "tts-1", "input": text, "voice": voice,
                         "response_format": "pcm"}).encode(),
        headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        raw = r.read()
    pcm24 = np.frombuffer(raw[: len(raw) // 2 * 2], dtype=np.int16)
    if pcm24.size == 0:
        raise RuntimeError("Kokoro returned no audio")
    return np.repeat(pcm24, 2)  # 24 kHz -> 48 kHz


def write_wav(path, samples, rate):
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(np.asarray(samples, dtype=np.int16).tobytes())


async def run(a):
    t = {}
    out = {"room": a.room, "identity": a.identity, "said": a.say, "stages": t,
           "bot_audio_seconds": 0.0, "bot_spoke": False, "line_spoken": False}

    def mark(k):
        t.setdefault(k, round(time.time(), 3))

    speech = await asyncio.to_thread(kokoro_pcm, a.tts_url, a.say, a.voice)
    mark("speech_ready")

    token = (api.AccessToken(a.api_key, a.api_secret)
             .with_identity(a.identity).with_name("Synthetic phone")
             .with_grants(api.VideoGrants(room_join=True, room=a.room, can_publish=True,
                                          can_subscribe=True, can_publish_data=True))
             .to_jwt())
    room = rtc.Room()
    bot_frames = []          # int16 arrays at 16 kHz
    state = {"last_loud": 0.0, "first_loud": 0.0, "bot_left": False}
    tasks = []

    async def record(track):
        stream = rtc.AudioStream(track, sample_rate=16000, num_channels=1)
        async for ev in stream:
            x = np.frombuffer(ev.frame.data, dtype=np.int16)
            bot_frames.append(x.copy())
            if x.size and float(np.sqrt(np.mean(x.astype(np.float64) ** 2))) > a.loud_rms:
                now = time.time()
                if not state["first_loud"]:
                    state["first_loud"] = now
                    mark("bot_audio_first")
                state["last_loud"] = now

    @room.on("participant_connected")
    def _pc(p):
        if p.identity.startswith(BOT_PREFIX):
            mark("bot_joined")

    @room.on("participant_disconnected")
    def _pd(p):
        if p.identity.startswith(BOT_PREFIX):
            mark("bot_left")
            state["bot_left"] = True

    @room.on("track_subscribed")
    def _ts(track, pub, participant):
        if track.kind == rtc.TrackKind.KIND_AUDIO and participant.identity.startswith(BOT_PREFIX):
            mark("bot_track_subscribed")
            tasks.append(asyncio.create_task(record(track)))

    await room.connect(a.url, token)
    mark("joined")
    source = rtc.AudioSource(RATE, 1)
    track = rtc.LocalAudioTrack.create_audio_track("mic", source)
    await room.local_participant.publish_track(
        track, rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_MICROPHONE))
    mark("mic_published")
    print("READY", file=sys.stderr, flush=True)
    if a.ready_file:
        with open(a.ready_file, "w") as f:
            f.write(str(os.getpid()))

    silence = np.zeros(FRAME, dtype=np.int16)
    pos = -1              # -1 = not speaking yet
    deadline = time.time() + a.timeout
    spoke_done_at = 0.0
    while time.time() < deadline:
        now = time.time()
        if pos < 0 and state["first_loud"] and now - state["last_loud"] >= a.quiet_after:
            mark("bot_audio_end")
            pos = 0
            mark("line_start")
        if 0 <= pos < speech.size:
            chunk = speech[pos:pos + FRAME]
            pos += FRAME
            if chunk.size < FRAME:
                chunk = np.concatenate([chunk, silence[: FRAME - chunk.size]])
            if pos >= speech.size:
                mark("line_end")
                spoke_done_at = time.time()
                out["line_spoken"] = True
        else:
            chunk = silence
        await source.capture_frame(
            rtc.AudioFrame(chunk.tobytes(), RATE, 1, FRAME))
        if state["bot_left"] and (out["line_spoken"] or not a.wait_after_bot_left):
            break
        if spoke_done_at and now - spoke_done_at > a.linger:
            break
    await room.disconnect()
    mark("left")
    for tk in tasks:
        tk.cancel()

    bot = np.concatenate(bot_frames) if bot_frames else np.zeros(0, dtype=np.int16)
    out["bot_audio_seconds"] = round(bot.size / 16000.0, 2)
    out["bot_spoke"] = bool(state["first_loud"])
    if a.out:
        os.makedirs(a.out, exist_ok=True)
        out["bot_wav"] = os.path.join(a.out, "bot-heard-by-phone.wav")
        write_wav(out["bot_wav"], bot, 16000)
        out["line_wav"] = os.path.join(a.out, "phone-line.wav")
        write_wav(out["line_wav"], speech, RATE)
    print(json.dumps(out))
    return 0 if (out["bot_spoke"] and out["line_spoken"]) else 1


def main():
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--room", required=True)
    p.add_argument("--say", required=True, help="what the synthetic human says")
    p.add_argument("--identity", default="synthetic_phone")
    p.add_argument("--url", default=os.environ.get("LIVEKIT_URL", "ws://127.0.0.1:7880"))
    p.add_argument("--api-key", default=os.environ.get("LIVEKIT_API_KEY"), required=not os.environ.get("LIVEKIT_API_KEY"))
    p.add_argument("--api-secret", default=os.environ.get("LIVEKIT_API_SECRET"), required=not os.environ.get("LIVEKIT_API_SECRET"))
    p.add_argument("--tts-url", default=os.environ.get("VOICE_TTS_URL", "http://127.0.0.1:8880/v1"))
    p.add_argument("--voice", default="am_adam")
    p.add_argument("--out", default="", help="directory for the recorded audio")
    p.add_argument("--ready-file", default="", help="written once the mic is published")
    p.add_argument("--timeout", type=float, default=90.0)
    p.add_argument("--quiet-after", type=float, default=1.5,
                   help="seconds of bot silence that count as the bot having finished")
    p.add_argument("--linger", type=float, default=20.0,
                   help="seconds to stay after speaking, waiting for the bot to leave")
    p.add_argument("--loud-rms", type=float, default=200.0)
    p.add_argument("--wait-after-bot-left", action="store_true")
    a = p.parse_args()
    try:
        sys.exit(asyncio.run(run(a)))
    except Exception as e:  # one line the caller can show
        print(json.dumps({"error": f"{type(e).__name__}: {e}"}))
        sys.exit(2)


if __name__ == "__main__":
    main()
