import { readFile, writeFile, mkdir } from "fs/promises"
import { dirname } from "path"
import { CORS } from "./sse"

export interface ParlaySettings {
  panelSide:           "left" | "right"
  triggerSide:         "left" | "right"
  enabledProjects:     "all" | string[]
  voiceEnabled:        boolean
  voiceSubmitPhrases:  string[]
  voiceClearPhrases:   string[]
  voiceStopPhrase:     string
  hybridVoice:         boolean
  localOnlyVoice:      boolean   // always use browser speechSynthesis; never contact Kokoro
  textScale:           number
  commandPhrases:      Record<string, string[]>
  voiceSettleMs:       number    // eval up-channel debounce tuned to the dictation settle time
}

import { SETTINGS_FILE as SETTINGS_PATH } from "./paths"

const DEFAULTS: ParlaySettings = {
  panelSide:           "left",
  triggerSide:         "right",
  enabledProjects:     "all",
  voiceEnabled:        true,
  voiceSubmitPhrases:  ["bravely", "gravely", "briefly", "lap"],
  voiceClearPhrases:   ["change inside in input"],
  voiceStopPhrase:     "spoken pause",
  hybridVoice:         false,
  localOnlyVoice:      false,
  textScale:           100,
  commandPhrases:      {},
  voiceSettleMs:       450,     // ~450ms: iOS live-dictation correction settle window
}

export async function readSettings(): Promise<ParlaySettings> {
  try {
    const raw = await readFile(SETTINGS_PATH, "utf8")
    const parsed = JSON.parse(raw)
    // Migration: voiceClearPhrase (single string, pre-2026-07-13) → voiceClearPhrases[]
    if (typeof parsed.voiceClearPhrase === "string" && !Array.isArray(parsed.voiceClearPhrases)) {
      parsed.voiceClearPhrases = parsed.voiceClearPhrase.trim() ? [parsed.voiceClearPhrase.trim()] : []
    }
    delete parsed.voiceClearPhrase
    return { ...DEFAULTS, ...parsed }
  } catch {
    return { ...DEFAULTS }
  }
}

async function writeSettings(s: ParlaySettings): Promise<void> {
  await mkdir(dirname(SETTINGS_PATH), { recursive: true })
  await writeFile(SETTINGS_PATH, JSON.stringify(s, null, 2), "utf8")
}

export function handleParlaySettings(req: Request, pathname: string): Response | null {
  if (pathname !== "/api/chat/parlay/settings") return null

  if (req.method === "GET") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const s = await readSettings()
          controller.enqueue(enc.encode(JSON.stringify(s)))
        } catch {
          controller.enqueue(enc.encode(JSON.stringify(DEFAULTS)))
        }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  if (req.method === "PUT") {
    return new Response(new ReadableStream({
      async start(controller) {
        const enc = new TextEncoder()
        try {
          const body = await req.json() as Partial<ParlaySettings>
          const current = await readSettings()
          const merged: ParlaySettings = {
            panelSide:          (body.panelSide    === "right" ? "right" : "left"),
            triggerSide:        (body.triggerSide   === "left"  ? "left"  : "right"),
            enabledProjects:    body.enabledProjects    ?? current.enabledProjects,
            voiceEnabled:       body.voiceEnabled != null ? Boolean(body.voiceEnabled) : current.voiceEnabled,
            voiceSubmitPhrases: Array.isArray(body.voiceSubmitPhrases) ? body.voiceSubmitPhrases.map(String) : current.voiceSubmitPhrases,
            voiceClearPhrases:  Array.isArray(body.voiceClearPhrases) ? body.voiceClearPhrases.map(String) : current.voiceClearPhrases,
            voiceStopPhrase:    body.voiceStopPhrase != null ? String(body.voiceStopPhrase) : current.voiceStopPhrase,
            hybridVoice:        body.hybridVoice != null ? Boolean(body.hybridVoice) : current.hybridVoice,
            localOnlyVoice:     body.localOnlyVoice != null ? Boolean(body.localOnlyVoice) : current.localOnlyVoice,
            textScale:          typeof body.textScale === "number" && isFinite(body.textScale)
                                  ? Math.min(160, Math.max(85, body.textScale)) : current.textScale,
            commandPhrases:     body.commandPhrases && typeof body.commandPhrases === "object" && !Array.isArray(body.commandPhrases)
                                  ? Object.fromEntries(Object.entries(body.commandPhrases)
                                      .filter(([, v]) => Array.isArray(v))
                                      .map(([k, v]) => [String(k), (v as unknown[]).map(String)]))
                                  : current.commandPhrases,
            voiceSettleMs:      typeof body.voiceSettleMs === "number" && isFinite(body.voiceSettleMs)
                                  ? Math.min(3000, Math.max(0, body.voiceSettleMs)) : current.voiceSettleMs,
          }
          await writeSettings(merged)
          controller.enqueue(enc.encode(JSON.stringify({ ok: true, settings: merged })))
        } catch (err) {
          controller.enqueue(enc.encode(JSON.stringify({ error: String(err) })))
        }
        controller.close()
      },
    }), { headers: { "Content-Type": "application/json", ...CORS } })
  }

  return null
}
