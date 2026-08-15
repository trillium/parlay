import { connect } from "node:net"
import { readFileSync, statSync, appendFileSync } from "node:fs"
import { join } from "node:path"
import { userInfo } from "node:os"
import { CORS } from "./sse"

// ── Pronunciation substitutions ─────────────────────────────────────────────
// word/phrase → phonetic respelling, applied BEFORE synth so reported fixes
// take effect without touching the speak daemon. Edit tts-substitutions.json
// next to this file; reload is automatic (mtime-cached). The map version is
// part of the clip-cache key so fixes aren't masked by cached audio.
const SUBS_PATH = join(import.meta.dir, "tts-substitutions.json")
let _subs: { mtime: number; map: Record<string, string> } = { mtime: 0, map: {} }
function substitutions(): { map: Record<string, string>; version: number } {
  try {
    const mtime = statSync(SUBS_PATH).mtimeMs
    if (mtime !== _subs.mtime) _subs = { mtime, map: JSON.parse(readFileSync(SUBS_PATH, "utf8")) }
  } catch { _subs = { mtime: 0, map: {} } }
  return { map: _subs.map, version: _subs.mtime }
}
// Built-in speech normalization (pre-substitutions). Version strings read as
// "v 3 point 7 point 1" (captain's rule); lookarounds keep IPs/longer dotted
// sequences untouched (127.0.0.1 has a .digit continuation and won't match).
function normalizeForSpeech(text: string): string {
  return text
    .replace(/(?<![.\d])(v?)(\d+)\.(\d+)\.(\d+)(?![.\d])/g, (_m, v, a, b, c) => `${v ? "v " : ""}${a} point ${b} point ${c}`)
    .replace(/(?<![.\d])v(\d+)\.(\d+)(?![.\d])/gi, (_m, a, b) => `v ${a} point ${b}`)
}

function applySubstitutions(text: string): { text: string; version: number } {
  const { map, version } = substitutions()
  let out = normalizeForSpeech(text)
  for (const [from, to] of Object.entries(map)) {
    const esc = from.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
    out = out.replace(new RegExp(`\\b${esc}\\b`, "gi"), to)
  }
  return { text: out, version }
}

const PAI_DIR = process.env.PAI_DIR ?? join(process.env.HOME ?? "", ".claude", "PAI")

const REPORTS_PATH = join(PAI_DIR, "MEMORY", "OBSERVABILITY", "tts-pronunciation-reports.jsonl")

// ── Disk clip cache (#15) ────────────────────────────────────────────────────
// Every synthesized clip persists to disk so recordings survive Pulse restarts
// and serve any device (the panel's IndexedDB cache is per-browser). Serving
// order: memory → disk → daemon. Retention: the 100 most-recently-used clips;
// older ones are deleted at write time. Hits touch mtime (mtime IS the index).
const DISK_CACHE_DIR = join(PAI_DIR, "MEMORY", "STATE", "tts-cache")
const DISK_CACHE_MAX = 100

function diskKey(key: string): string {
  const { createHash } = require("crypto") as typeof import("crypto")
  return createHash("sha1").update(key).digest("hex").slice(0, 24) + ".wav"
}

function diskGet(key: string): Uint8Array | null {
  try {
    const { readFileSync, utimesSync } = require("fs") as typeof import("fs")
    const p = join(DISK_CACHE_DIR, diskKey(key))
    const bytes = readFileSync(p)
    const now = new Date()
    utimesSync(p, now, now)   // LRU touch
    return bytes
  } catch { return null }
}

function diskPut(key: string, wav: Uint8Array): void {
  try {
    const { mkdirSync, writeFileSync, readdirSync, statSync, unlinkSync } = require("fs") as typeof import("fs")
    mkdirSync(DISK_CACHE_DIR, { recursive: true })
    writeFileSync(join(DISK_CACHE_DIR, diskKey(key)), wav)
    // Prune: keep exactly the DISK_CACHE_MAX most recent
    const entries = readdirSync(DISK_CACHE_DIR)
      .filter(f => f.endsWith(".wav"))
      .map(f => ({ f, mtime: statSync(join(DISK_CACHE_DIR, f)).mtimeMs }))
      .sort((a, b) => b.mtime - a.mtime)
    for (const e of entries.slice(DISK_CACHE_MAX)) {
      try { unlinkSync(join(DISK_CACHE_DIR, e.f)) } catch {}
    }
  } catch { /* disk cache is best-effort */ }
}

// ── Server TTS via the speak daemon ─────────────────────────────────────────
// POST /api/tts {text, voice?, speed?} → audio/wav
// Bridges to the local speak daemon's `synth` command over its Unix socket
// (length-prefixed JSON in, length-prefixed JSON out with base64 WAV). The
// daemon reuses its kokoro cache + voice pool; caller is pinned to "parlay"
// so the panel keeps one consistent voice.

// The daemon names its socket after the account it runs as. $USER is not set
// under launchd — where this server actually runs — so fall through $LOGNAME and
// then the process's own uid rather than a placeholder: a placeholder resolves to
// a socket nothing is listening on, which reads as "audio is broken", not as
// "the account could not be determined". userInfo() throws on a uid with no
// passwd entry, so a failure here degrades the same way an unset account does
// instead of taking the module down at import time.
function currentAccount(): string {
  const fromEnv = process.env.USER || process.env.LOGNAME
  if (fromEnv) return fromEnv
  try { return userInfo().username } catch { return "" }
}

const SOCKET_PATH = `/tmp/speak-${currentAccount()}.sock`
const SYNTH_TIMEOUT_MS = 30_000

// Small in-memory clip cache — chat replies repeat rarely, but replays are free
const clipCache = new Map<string, Uint8Array>()
const CLIP_CACHE_MAX = 40

function synthViaDaemon(payload: Record<string, unknown>): Promise<{ ok: boolean; error?: string; wav?: Uint8Array; seconds?: number; voice?: string }> {
  return new Promise((resolve) => {
    const chunks: Buffer[] = []
    let settled = false
    const done = (result: { ok: boolean; error?: string; wav?: Uint8Array; seconds?: number; voice?: string }) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      try { sock.destroy() } catch {}
      resolve(result)
    }
    const timer = setTimeout(() => done({ ok: false, error: "speak daemon timeout" }), SYNTH_TIMEOUT_MS)

    const sock = connect(SOCKET_PATH)
    sock.on("connect", () => {
      const body = Buffer.from(JSON.stringify({ command: "synth", ...payload }))
      const len = Buffer.alloc(4)
      len.writeUInt32BE(body.length)
      sock.write(Buffer.concat([len, body]))
    })
    sock.on("data", (d) => {
      chunks.push(d)
      const buf = Buffer.concat(chunks)
      if (buf.length < 4) return
      const msgLen = buf.readUInt32BE(0)
      if (buf.length < 4 + msgLen) return
      try {
        const resp = JSON.parse(buf.subarray(4, 4 + msgLen).toString("utf8"))
        if (!resp.ok) { done({ ok: false, error: String(resp.error ?? "synth failed") }); return }
        done({ ok: true, wav: Buffer.from(String(resp.wav_b64), "base64"), seconds: resp.seconds, voice: resp.voice })
      } catch (e) { done({ ok: false, error: `bad daemon response: ${e}` }) }
    })
    sock.on("error", (e) => done({ ok: false, error: `speak daemon unreachable: ${e.message}` }))
  })
}

// NOTE: errors stream back as JSON inside an audio/wav-typed response (the
// stream pattern fixes headers up front) — the panel sniffs the RIFF magic
// and treats anything else as an error payload.
export function handleTTSRequest(req: Request, pathname: string): Response | null {
  // Corrector UI (#19): persist a pronunciation substitution. The map version
  // is part of every clip-cache key, so the fix takes effect immediately.
  if (req.method === "POST" && pathname === "/api/chat/tts-correction") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        const reply = (obj: unknown) => { controller.enqueue(enc.encode(JSON.stringify(obj))); controller.close() }
        try {
          const body = await req.json()
          const from = String(body.from ?? "").trim().slice(0, 100)
          const to = String(body.to ?? "").trim().slice(0, 200)
          if (!from || !to) { reply({ error: "from and to required" }); return }
          const { map } = substitutions()
          const next = { ...map, [from]: to }
          const { writeFileSync } = require("fs") as typeof import("fs")
          writeFileSync(SUBS_PATH, JSON.stringify(next, null, 2) + "\n", "utf-8")
          appendFileSync(REPORTS_PATH, JSON.stringify({
            ts: new Date().toISOString(), sentence: String(body.sentence ?? "").slice(0, 500),
            voice: "parlay-pool", clipMeta: { kind: "correction", from, to },
          }) + "\n", "utf-8")
          reply({ ok: true, substitutions: Object.keys(next).length })
        } catch { reply({ error: "bad request" }) }
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  // Mispronunciation reports from the panel (🚩 / "flag speech" command)
  if (req.method === "POST" && pathname === "/api/chat/tts-report") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json()
          const sentence = String(body.sentence ?? "").trim().slice(0, 500)
          if (!sentence) { controller.enqueue(enc.encode(JSON.stringify({ error: "sentence required" }))); controller.close(); return }
          const entry = {
            ts: new Date().toISOString(),
            sentence,
            voice: String(body.voice ?? "parlay-pool"),
            clipMeta: body.clipMeta ?? null,
          }
          appendFileSync(REPORTS_PATH, JSON.stringify(entry) + "\n", "utf-8")
          controller.enqueue(enc.encode(JSON.stringify({ ok: true })))
        } catch { controller.enqueue(enc.encode(JSON.stringify({ error: "bad request" }))) }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (!(req.method === "POST" && pathname === "/api/chat/tts")) return null
  return new Response(new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      const fail = (status: string) => {
        controller.enqueue(enc.encode(JSON.stringify({ error: status })))
        controller.close()
      }
      try {
        const body = await req.json()
        const rawText = String(body.text ?? "").trim().slice(0, 2000)
        if (!rawText) { fail("text required"); return }
        const { text, version: subVersion } = applySubstitutions(rawText)
        const voice = body.voice ? String(body.voice) : undefined
        const speed = body.speed ? Number(body.speed) : undefined
        const key = `${voice ?? ""}|${speed ?? ""}|${subVersion}|${text}`

        // Serving order: memory (hot) → disk (survives restarts, #15) → daemon
        let wav = clipCache.get(key)
        if (!wav) {
          const fromDisk = diskGet(key)
          if (fromDisk) wav = fromDisk
        }
        if (!wav) {
          const result = await synthViaDaemon({ text, caller: "parlay", ...(voice ? { voice } : {}), ...(speed ? { speed } : {}) })
          if (!result.ok || !result.wav) { fail(result.error ?? "synthesis failed"); return }
          wav = result.wav
          diskPut(key, wav)
        }
        clipCache.set(key, wav)
        if (clipCache.size > CLIP_CACHE_MAX) clipCache.delete(clipCache.keys().next().value!)
        controller.enqueue(wav)
        controller.close()
      } catch { fail("bad request") }
    },
  }), { headers: { "Content-Type": "audio/wav", "Cache-Control": "no-store", ...CORS } })
}
