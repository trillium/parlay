# Phone voice path

Talk to an agent from a phone: the agent's reply is spoken on the phone, and what
you say back comes out as text. This directory is the fleet glue around
[VoiceMode](https://github.com/mbailey/voicemode) that makes that reliable. It is
one fleet's setup, not part of parlay's core.

```
agent text ─▶ vturn.sh ─▶ voicemode converse ─▶ Kokoro (speech) ─▶ LiveKit room ─▶ phone page plays it
agent text ◀─ vturn.sh ◀─ Whisper (text)     ◀─ LiveKit room    ◀─ phone microphone
```

## Use it

1. Install and start everything: `examples/fleet/voice/install.sh --build --restart --restart-proxy`
   (copies the files, builds the page, starts the page server and the TLS forwarder the phone connects to).
2. Open `https://<this machine's tailnet name>:3443/` on the phone and leave it on screen.
3. Check: `~/.voicemode/voice-health` — prints `READY` when a turn will work.
4. Take a turn: `~/.voicemode/vturn.sh "text to say" 20` — prints what was heard.

## What is here

| File | What it does |
|---|---|
| `frontend/app/page.tsx` | The phone page. Joins the voice room by itself on load, rejoins whenever it finds itself disconnected, keeps the screen awake, and reports to the Mac every 5 s that it is there. |
| `frontend/app/api/connection-details/route.ts` | Hands the page a token for one fixed room (`voice_assistant_room`) and the right socket address for how the page was reached. |
| `frontend/app/api/voice-log/route.ts` | Turn state for the page to draw; receives the page's presence and event reports. |
| `vturn.sh` + `vstage.py` | One spoken turn, with a status and a duration for every stage. |
| `tlsproxy.js` | TLS in front of the page and the voice server (a phone only gives its microphone to a secure page). Listens on this machine's tailnet address only. |
| `voice-health` | One command that says whether a turn will work, and the fix if not. |
| `voice-roundtrip.test.sh` + `voice-synth-phone.py` | A full round trip with no human: a synthetic phone joins a room of its own, hears the bot, answers, and the answer must come back as text. |
| `install.sh` | Copies all of the above into place, rebuilds the page, restarts the page server and the forwarder. |

The three `frontend/` files are an overlay on VoiceMode's stock frontend; the rest
of that frontend is untouched.

## Why the phone used to drop off the line

The stock page waits for a tap after every load, joins a room with a random name,
and does nothing when the connection drops. A reload, a locked screen or a network
blip left the room empty; LiveKit closes an empty room after 20 seconds; the next
turn failed with `No active LiveKit rooms found`. Nothing was broken on the Mac.

## voice-health

```
voice-health [--json] [--services-only | --engines-only]
```

Exit `0` ready, `1` something on the Mac is down or out of date, `2` everything is
up but no phone is on the line. Every failing line carries a `fix:`.

## vturn.sh

A turn always talks in `voice_assistant_room`, the one room the phone page joins;
it never picks "whichever room is active". `VOICE_ROOM` names a different room, and
the round-trip test uses that to stay off the real line.

Exit `0` heard something (printed on stdout), `3` no phone on the line, `4` the
phone was there but nothing usable came back. Each turn appends one record to
`~/.voicemode/turn-timing.jsonl`:

| Field | Meaning |
|---|---|
| `link_ms` | start of turn until the bot dialled into the phone's room |
| `tts_ms` | Kokoro synthesis time |
| `playback_ms` | how long the phone page reported the bot's voice arriving with sound allowed; `null` if the page did not report it |
| `listen_ms` | end of playback until the turn returned |
| `speech_s` | seconds of audio Whisper transcribed |
| `total_ms` | the whole turn |

`session.env` in `~/.voicemode` (optional) sets `VOICE_SESSION_NAME`,
`VOICE_SESSION_ID` and `VOICE_SESSION_CWD`: the chat the page says it is talking to.

## voice-roundtrip.test.sh

Manual, not in CI: it needs live LiveKit, Kokoro and Whisper and the `voicemode`
tool. It never touches the real phone line: it uses its own room and a scratch
state directory. Exit `0` passed, `1` a check failed, `2` services not up.

## Limits

- **A locked phone is off the line.** A browser cannot hold a microphone while the
  phone is locked. The page asks to keep the screen awake and rejoins by itself
  when it comes back, but it must be on screen during a turn.
- **A VoiceMode upgrade removes the page overlay.** `voice-health` reports it as an
  old page build; re-run `install.sh --build --restart`.
- **Nothing here starts at boot.** `install.sh --restart --restart-proxy` brings the
  page server and the forwarder back after a reboot.
- **The page server binds to loopback.** The phone reaches it through the forwarder
  only. A wildcard bind fails when any other program holds port 3000.
- **Only the tailnet can reach the page.** The page hands a room token and the turn
  log to whoever asks, so being in the tailnet is its only access control. The
  forwarder therefore listens on the tailnet address alone and refuses to start
  when Tailscale is down; `voice-health` fails `forwarder reach` if it is ever found
  listening on every interface. `VOICE_BIND_HOST` overrides the address.
