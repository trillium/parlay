"use client";

import TranscriptionView from "@/components/TranscriptionView";
import {
  RoomAudioRenderer,
  RoomContext,
  useConnectionState,
  useLocalParticipant,
  useRemoteParticipants,
} from "@livekit/components-react";
import { ConnectionState, Room, RoomEvent } from "livekit-client";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ConnectionDetails } from "./api/connection-details/route";

const BOT_IDENTITY = "voice-mode-bot";

// The phone joins the voice room BY ITSELF and keeps itself there.
//
// Before this, the page waited for a tap on "Start a conversation" after every
// load, joined a room with a random name, and did nothing when the connection
// dropped. A reload, a locked screen or a network blip left the room empty, the
// server closed it 20 seconds later, and the next spoken turn failed with
// "No active LiveKit rooms found". Now the page joins on load, rejoins whenever
// it finds itself disconnected, asks the phone to keep its screen awake, and
// tells the Mac every few seconds that it is still there.

const RETRY_MS = 3000;
const PRESENCE_MS = 5000;

// Send a diagnostic event to the Mac so an agent can read what the phone saw.
function clog(event: string, detail?: unknown) {
  try {
    fetch("/api/voice-log", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        event,
        detail:
          detail === undefined
            ? undefined
            : String((detail as { message?: string })?.message ?? JSON.stringify(detail)),
      }),
      keepalive: true,
    }).catch(() => {});
  } catch {}
}

// One identity per device, kept across reloads: a reconnect then REPLACES the
// stale participant instead of leaving a ghost beside it.
function deviceIdentity(): string {
  const fresh = "phone_" + Math.random().toString(36).slice(2, 10);
  try {
    const have = window.localStorage.getItem("voice_identity");
    if (have && /^[A-Za-z0-9_-]{1,64}$/.test(have)) return have;
    window.localStorage.setItem("voice_identity", fresh);
  } catch {}
  return fresh;
}

type Shared = { micMax: number; wakeLock: boolean; lastJoinWhy: string };

export default function Page() {
  const [room] = useState(() => new Room());
  const [error, setError] = useState("");
  const [soundBlocked, setSoundBlocked] = useState(false);
  const [awake, setAwake] = useState(false);
  // want: the user has not hung up. joining: a join attempt is in flight.
  const want = useRef(true);
  const joining = useRef(false);
  const micDenied = useRef(false);
  const lock = useRef<{ release: () => Promise<void> } | null>(null);
  const identity = useRef("");
  const shared = useRef<Shared>({ micMax: 0, wakeLock: false, lastJoinWhy: "" });

  const keepAwake = useCallback(async () => {
    try {
      const wl = (navigator as unknown as { wakeLock?: { request: (t: string) => Promise<any> } }).wakeLock;
      if (!wl || lock.current || document.visibilityState !== "visible") return;
      const l = await wl.request("screen");
      lock.current = l;
      shared.current.wakeLock = true;
      setAwake(true);
      l.addEventListener("release", () => {
        lock.current = null;
        shared.current.wakeLock = false;
        setAwake(false);
      });
    } catch (e) {
      clog("wake_lock_failed", e);
    }
  }, []);

  const turnMicOn = useCallback(async () => {
    try {
      await room.localParticipant.setMicrophoneEnabled(true);
      micDenied.current = false;
      setError("");
    } catch (e) {
      const name = (e as { name?: string })?.name ?? "";
      // A denied prompt must not be re-asked on a timer; wait for a tap.
      if (name === "NotAllowedError" || name === "SecurityError") micDenied.current = true;
      setError("Microphone blocked: " + (e instanceof Error ? e.message : String(e)));
      clog("mic_failed", e);
    }
  }, [room]);

  const join = useCallback(
    async (why: string) => {
      if (joining.current || room.state !== ConnectionState.Disconnected) return;
      joining.current = true;
      shared.current.lastJoinWhy = why;
      clog("join_attempt", why);
      try {
        const url = new URL(
          process.env.NEXT_PUBLIC_CONN_DETAILS_ENDPOINT ?? "/api/connection-details",
          window.location.origin
        );
        url.searchParams.set("identity", identity.current);
        const response = await fetch(url.toString(), { cache: "no-store" });
        if (!response.ok) throw new Error("connection details answered " + response.status);
        const d: ConnectionDetails = await response.json();
        await room.connect(d.serverUrl, d.participantToken);
        setError("");
        clog("joined", d.roomName);
        await turnMicOn();
        try {
          await room.startAudio();
        } catch {}
        setSoundBlocked(!room.canPlaybackAudio);
        keepAwake();
      } catch (e) {
        setError("Could not reach voice server: " + (e instanceof Error ? e.message : String(e)));
        clog("connect_failed", e);
      } finally {
        joining.current = false;
      }
    },
    [room, turnMicOn, keepAwake]
  );

  // Diagnostics, as before.
  useEffect(() => {
    clog("page_loaded", {
      secure: window.isSecureContext,
      mediaDevices: !!navigator.mediaDevices?.getUserMedia,
      url: location.href,
    });
    navigator.permissions
      ?.query({ name: "microphone" as PermissionName })
      .then((p) => {
        clog("mic_permission", p.state);
        p.onchange = () => clog("mic_permission", p.state);
      })
      .catch(() => {});
    const onErr = (e: ErrorEvent) => clog("window_error", e.message);
    const onRej = (e: PromiseRejectionEvent) => clog("unhandled_rejection", String(e.reason));
    window.addEventListener("error", onErr);
    window.addEventListener("unhandledrejection", onRej);
    const h = (ev: string) => (...a: unknown[]) => clog("room_" + ev, a.length ? String(a[0]) : undefined);
    const onState = h("state");
    const onGone = h("disconnected");
    const onIn = (p: { identity: string }) => clog("participant_joined", p.identity);
    const onOut = (p: { identity: string }) => clog("participant_left", p.identity);
    const onPub = (pub: { source: string }) => clog("local_track_published", pub.source);
    const onSub = (_t: unknown, _p: unknown, part: { identity: string }) => clog("track_subscribed", part.identity);
    const onDev = (e: Error) => clog("media_devices_error", e);
    const onSound = () => setSoundBlocked(!room.canPlaybackAudio);
    // The phone's own record of Claude's turn. "Played" needs both halves: the
    // bot's audio is arriving here, and this browser is allowed to make sound.
    // The Mac reads these two lines to time the playback step of a turn.
    let botAudio = false;
    const onSpeakers = (speakers: { identity: string }[]) => {
      const on = speakers.some((p) => p.identity.startsWith(BOT_IDENTITY));
      if (on !== botAudio) {
        botAudio = on;
        clog("bot_audio", { on, sound: room.canPlaybackAudio });
      }
    };
    room.on(RoomEvent.ActiveSpeakersChanged, onSpeakers);
    room.on(RoomEvent.ConnectionStateChanged, onState);
    room.on(RoomEvent.Disconnected, onGone);
    room.on(RoomEvent.ParticipantConnected, onIn);
    room.on(RoomEvent.ParticipantDisconnected, onOut);
    room.on(RoomEvent.LocalTrackPublished, onPub);
    room.on(RoomEvent.TrackSubscribed, onSub);
    room.on(RoomEvent.MediaDevicesError, onDev);
    room.on(RoomEvent.AudioPlaybackStatusChanged, onSound);
    return () => {
      window.removeEventListener("error", onErr);
      window.removeEventListener("unhandledrejection", onRej);
      room.off(RoomEvent.ConnectionStateChanged, onState);
      room.off(RoomEvent.Disconnected, onGone);
      room.off(RoomEvent.ParticipantConnected, onIn);
      room.off(RoomEvent.ParticipantDisconnected, onOut);
      room.off(RoomEvent.LocalTrackPublished, onPub);
      room.off(RoomEvent.TrackSubscribed, onSub);
      room.off(RoomEvent.MediaDevicesError, onDev);
      room.off(RoomEvent.AudioPlaybackStatusChanged, onSound);
      room.off(RoomEvent.ActiveSpeakersChanged, onSpeakers);
    };
  }, [room]);

  // Stay in the room: join on load, then keep checking.
  useEffect(() => {
    identity.current = deviceIdentity();
    const visible = () => document.visibilityState === "visible";
    const tick = (why: string) => {
      if (!want.current || !visible()) return;
      keepAwake();
      if (room.state === ConnectionState.Disconnected) {
        join(why);
        return;
      }
      if (room.state !== ConnectionState.Connected || micDenied.current) return;
      // Back from a locked screen the microphone can be gone while the room
      // connection survived: put it back.
      const pub = room.localParticipant.getTrackPublications().find((p) => p.kind === "audio");
      const dead = pub?.track?.mediaStreamTrack?.readyState === "ended";
      if (dead) {
        clog("mic_restart", "track ended");
        room.localParticipant.setMicrophoneEnabled(false).finally(() => turnMicOn());
      } else if (!room.localParticipant.isMicrophoneEnabled) {
        turnMicOn();
      }
    };
    tick("page_load");
    const id = window.setInterval(() => tick("retry"), RETRY_MS);
    const onVis = () => {
      clog("visibility", document.visibilityState);
      tick("visible_again");
    };
    const onShow = () => tick("page_shown");
    const onOnline = () => tick("network_back");
    document.addEventListener("visibilitychange", onVis);
    window.addEventListener("pageshow", onShow);
    window.addEventListener("online", onOnline);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", onVis);
      window.removeEventListener("pageshow", onShow);
      window.removeEventListener("online", onOnline);
    };
  }, [room, join, keepAwake, turnMicOn]);

  // Tell the Mac the page is alive. One small file is overwritten, so this
  // costs nothing to keep running all day.
  useEffect(() => {
    const beat = () => {
      const pub = room.localParticipant.getTrackPublications().find((p) => p.kind === "audio");
      const body = {
        event: "presence",
        presence: {
          identity: identity.current,
          room: room.name || "",
          conn: room.state,
          mic: room.localParticipant.isMicrophoneEnabled,
          micTrack: pub?.track?.mediaStreamTrack?.readyState ?? "none",
          micMax: Number(shared.current.micMax.toFixed(3)),
          visible: document.visibilityState === "visible",
          wakeLock: shared.current.wakeLock,
          sound: room.canPlaybackAudio,
          wanted: want.current,
          others: Array.from(room.remoteParticipants.values()).map((p) => p.identity),
          lastJoinWhy: shared.current.lastJoinWhy,
        },
      };
      shared.current.micMax = 0;
      fetch("/api/voice-log", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
        keepalive: true,
      }).catch(() => {});
    };
    const id = window.setInterval(beat, PRESENCE_MS);
    const t = window.setTimeout(beat, 1500);
    return () => {
      window.clearInterval(id);
      window.clearTimeout(t);
    };
  }, [room]);

  const hangUp = useCallback(() => {
    want.current = false;
    clog("hang_up");
    room.disconnect();
  }, [room]);

  // A tap is a user gesture: it may unlock sound and re-ask for the microphone.
  const connectNow = useCallback(async () => {
    want.current = true;
    micDenied.current = false;
    clog("connect_tapped");
    if (room.state === ConnectionState.Disconnected) {
      await join("tap");
    } else {
      try {
        await room.startAudio();
      } catch {}
      setSoundBlocked(!room.canPlaybackAudio);
      if (!room.localParticipant.isMicrophoneEnabled) await turnMicOn();
    }
  }, [room, join, turnMicOn]);

  const reconnect = useCallback(async () => {
    want.current = true;
    micDenied.current = false;
    clog("reconnect_tapped");
    await room.disconnect();
    await join("reconnect_tap");
  }, [room, join]);

  return (
    <main data-lk-theme="default" className="h-full grid content-center bg-[var(--lk-bg)]">
      <RoomContext.Provider value={room}>
        <div className="lk-room-container max-w-[1024px] w-[90vw] mx-auto max-h-[90vh]">
          <VoicePanel
            error={error}
            soundBlocked={soundBlocked}
            awake={awake}
            shared={shared.current}
            onConnect={connectNow}
            onReconnect={reconnect}
            onHangUp={hangUp}
          />
        </div>
      </RoomContext.Provider>
    </main>
  );
}

const btn: React.CSSProperties = {
  marginTop: 8,
  marginRight: 8,
  padding: "8px 14px",
  borderRadius: 8,
  border: "1px solid #888",
  background: "transparent",
  color: "inherit",
  fontSize: 15,
};

function VoicePanel(props: {
  error: string;
  soundBlocked: boolean;
  awake: boolean;
  shared: Shared;
  onConnect: () => void;
  onReconnect: () => void;
  onHangUp: () => void;
}) {
  const remotes = useRemoteParticipants();
  const connState = useConnectionState();
  const { isMicrophoneEnabled } = useLocalParticipant();
  const connected = connState === ConnectionState.Connected;
  const ok = (b: boolean) => (b ? "✅" : "⚪");
  // Claude's speech bot is an ordinary participant that dials in for one turn
  // and leaves, so it is recognised by its name, not by an "agent" state.
  const botOnLine = remotes.some((p) => p.identity.startsWith(BOT_IDENTITY));

  const connWords: Record<string, string> = {
    connected: "connected",
    connecting: "connecting…",
    reconnecting: "reconnecting…",
    signalReconnecting: "reconnecting…",
    disconnected: "not connected, retrying every few seconds",
  };

  return (
    <>
      {/* First thing on the page: am I on the line, and what to press if not. */}
      <div
        data-testid="voice-status"
        data-conn={connState}
        data-mic={isMicrophoneEnabled ? "on" : "off"}
        style={{ fontSize: 15, lineHeight: 1.6, marginBottom: 16, padding: 12, borderRadius: 8, border: "1px solid #888", color: "inherit" }}
      >
        {connected && isMicrophoneEnabled && !props.error && !props.soundBlocked ? (
          <div>{"✅"} All good: on the line, microphone sending audio</div>
        ) : (
          <>
            <div>{ok(connected)} Voice server: {connWords[connState] ?? connState}</div>
            <div>{ok(isMicrophoneEnabled)} Microphone: {isMicrophoneEnabled ? "on, sending audio" : "off"}</div>
          </>
        )}
        <div style={{ marginTop: 6, opacity: 0.85 }}>
          Talking to: <b>Claude Code on this MacBook</b>.{" "}
          {botOnLine
            ? "Its speech bot is on the line."
            : "Its speech bot joins only while Claude takes a turn; between turns this is normal."}
        </div>
        <div style={{ marginTop: 6, opacity: 0.85 }}>
          {props.awake
            ? "This page is keeping the screen awake. Leave it open and you stay on the line."
            : "Keep this page open on screen: a locked phone drops off the line. It rejoins by itself when you come back."}
        </div>
        {props.error && <div style={{ color: "#f55" }}>{"❌"} {props.error}</div>}
        {props.soundBlocked && (
          <button onClick={props.onConnect} style={{ ...btn, background: "#b45309", color: "#fff", fontSize: 18 }}>
            Tap to turn sound on
          </button>
        )}
        <div>
          {!connected && (
            <button onClick={props.onConnect} style={btn}>Connect now</button>
          )}
          {connected && !isMicrophoneEnabled && (
            <button onClick={props.onConnect} style={btn}>Turn microphone on</button>
          )}
          <button onClick={props.onReconnect} style={btn}>Reconnect</button>
          {connected && (
            <button onClick={props.onHangUp} style={btn}>Hang up</button>
          )}
        </div>
      </div>
      <MicAndLog connected={connected} shared={props.shared} />
      {/* Always mounted while connected: this is what plays Claude's voice. */}
      {connected && <RoomAudioRenderer />}
      {connected && botOnLine && (
        <div className="w-full">
          <TranscriptionView />
        </div>
      )}
    </>
  );
}

type LogLine = { t: string; who: string; text: string };

function MicAndLog({ connected, shared }: { connected: boolean; shared: Shared }) {
  const { microphoneTrack } = useLocalParticipant();
  const [level, setLevel] = useState(0);
  const [peak, setPeak] = useState(0);
  const [phase, setPhase] = useState("idle");
  const [lines, setLines] = useState<LogLine[]>([]);
  const [session, setSession] = useState<any>({});
  const [turns, setTurns] = useState<any[]>([]);

  // Live microphone volume, read from the phone's own mic track.
  useEffect(() => {
    const mst = microphoneTrack?.track?.mediaStreamTrack;
    if (!mst) { setLevel(0); return; }
    const ctx = new AudioContext();
    const src = ctx.createMediaStreamSource(new MediaStream([mst]));
    const an = ctx.createAnalyser();
    an.fftSize = 512;
    src.connect(an);
    const buf = new Uint8Array(an.fftSize);
    let raf = 0;
    let pk = 0;
    clog("mic_track_attached", mst.label || mst.readyState);
    // Joined without a tap, the meter's audio context can start suspended.
    const wake = () => { if (ctx.state === "suspended") ctx.resume().catch(() => {}); };
    wake();
    document.addEventListener("touchend", wake);
    document.addEventListener("click", wake);
    const tick = () => {
      an.getByteTimeDomainData(buf);
      let s = 0;
      for (let k = 0; k < buf.length; k++) { const d = (buf[k] - 128) / 128; s += d * d; }
      const rms = Math.min(1, Math.sqrt(s / buf.length) * 4);
      pk = Math.max(rms, pk * 0.97);
      if (rms > shared.micMax) shared.micMax = rms;
      setLevel(rms); setPeak(pk);
      raf = requestAnimationFrame(tick);
    };
    tick();
    return () => {
      cancelAnimationFrame(raf);
      document.removeEventListener("touchend", wake);
      document.removeEventListener("click", wake);
      ctx.close();
    };
  }, [microphoneTrack, shared]);

  // What Claude said and what the phone sent, plus the processing phase.
  useEffect(() => {
    let stop = false;
    const poll = async () => {
      try {
        const r = await fetch("/api/voice-log", { cache: "no-store" });
        const j = await r.json();
        if (!stop) { setPhase(j.phase); setLines(j.lines); setSession(j.session || {}); setTurns(j.turns || []); }
      } catch {}
    };
    poll();
    const id = setInterval(poll, 1000);
    return () => { stop = true; clearInterval(id); };
  }, []);

  const phaseText: Record<string, string> = {
    idle: "Ready: waiting for Claude's next turn", speaking: "Claude is speaking", listening: "Listening to you",
    thinking: "Claude is processing what you said",
  };
  const busy = phase === "thinking";
  return (
    <div style={{ marginBottom: 16, padding: 12, borderRadius: 8, border: "1px solid #888", fontSize: 15 }}>
      <div style={{ marginBottom: 10, padding: 8, borderRadius: 8, background: "#8882", fontSize: 14 }}>
        <div>Connected to chat: <b>{session.name || "no chat has spoken yet"}</b></div>
        {session.id && <div style={{ opacity: 0.8 }}>Session {String(session.id).slice(0, 8)} on {session.host}, in {session.cwd}</div>}
      </div>
      <div style={{ marginBottom: 6 }}>Mic volume {connected ? "" : "(not connected)"}</div>
      <div style={{ height: 14, background: "#8884", borderRadius: 7, overflow: "hidden", position: "relative" }}>
        <div style={{ width: `${Math.round(level * 100)}%`, height: "100%", background: level > 0.08 ? "#22c55e" : "#888" }} />
        <div style={{ position: "absolute", top: 0, bottom: 0, left: `${Math.round(peak * 100)}%`, width: 2, background: "#fff" }} />
      </div>
      <div style={{ marginTop: 6, fontSize: 13, opacity: 0.8 }}>
        {connected ? (level > 0.08 ? "Hearing you" : "Quiet: speak to see the bar move") : "Joining the line…"}
      </div>
      <div style={{ marginTop: 12, display: "flex", alignItems: "center", gap: 12, fontSize: 24 }}>
        <span style={{ display: "inline-block", width: 18, height: 18, borderRadius: 9, background: busy ? "#f59e0b" : phase === "idle" ? "#888" : "#22c55e", animation: busy ? "pulse 1s infinite" : undefined }} />
        <b>{!connected ? "Joining…" : level > 0.08 && phase === "idle" ? "Hearing you" : (phaseText[phase] ?? phase)}</b>
      </div>
      {turns.length > 0 && (() => { const t = turns[turns.length - 1]; const ic: Record<string, string> = { wait: "⚪", run: "⏳", ok: "✅", fail: "❌", skip: "➖" };
        const done = t.stages.filter((x: any) => x.status === "ok").length;
        return (
        <div style={{ marginTop: 12, fontSize: 14 }}>
          <div style={{ fontWeight: 700 }}>Last turn ({t.at}): {done} of {t.stages.length} steps done{typeof t.total_ms === "number" ? `, ${(t.total_ms / 1000).toFixed(1)}s in all` : ""}</div>
          {t.stages.map((x: any) => (
            <div key={x.key} style={{ display: "flex", gap: 8, padding: "3px 0", opacity: x.status === "wait" ? 0.5 : 1 }}>
              <span>{ic[x.status] || x.status}</span>
              <span style={{ flex: 1 }}>{x.label}{x.detail ? <span style={{ display: "block", fontSize: 12, opacity: 0.75, wordBreak: "break-word" }}>{x.detail}</span> : null}</span>
              <span style={{ fontSize: 12, opacity: 0.7, textAlign: "right" }}>{typeof x.ms === "number" ? <b>{(x.ms / 1000).toFixed(1)}s </b> : null}{x.at}</span>
            </div>
          ))}
        </div>); })()}
      <style>{"@keyframes pulse{0%,100%{opacity:1}50%{opacity:.25}}"}</style>
      <div style={{ marginTop: 12, maxHeight: 220, overflowY: "auto", fontSize: 14, lineHeight: 1.5 }}>
        {lines.length === 0 && <div style={{ opacity: 0.6 }}>Nothing sent yet. What you say will appear here as the text sent to Claude.</div>}
        {lines.map((l, i) => (
          <div key={i} style={{ display: "flex", justifyContent: l.who === "you" ? "flex-end" : "flex-start", marginBottom: 8 }}>
            <div style={{ maxWidth: "85%", padding: "8px 12px", borderRadius: 12, background: l.who === "you" ? "#1d4ed8" : l.who === "system" ? "#78350f" : "#3f3f46", color: "#fff" }}>
              <div style={{ fontSize: 11, opacity: 0.7 }}>{l.who === "you" ? "You" : l.who === "claude" ? "Claude" : "Problem"} {l.t}</div>
              {l.who === "system" ? "Didn't catch that: " + l.text : l.text}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
