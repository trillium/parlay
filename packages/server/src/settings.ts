import { readFile, writeFile, mkdir } from "fs/promises"
import { join } from "path"
import { homedir } from "os"
import { CORS } from "./sse"

export interface ParlaySettings {
  panelSide:           "left" | "right"
  triggerSide:         "left" | "right"
  enabledProjects:     "all" | string[]
  voiceEnabled:        boolean
  voiceSubmitPhrases:  string[]
  voiceClearPhrase:    string
}

const DATA_DIR      = process.env.PARLAY_DATA_DIR ?? join(homedir(), ".parlay")
const SETTINGS_PATH = join(DATA_DIR, "parlay-settings.json")

const DEFAULTS: ParlaySettings = {
  panelSide:           "left",
  triggerSide:         "right",
  enabledProjects:     "all",
  voiceEnabled:        true,
  voiceSubmitPhrases:  ["bravely", "gravely", "briefly", "lap"],
  voiceClearPhrase:    "change inside in input",
}

export async function readSettings(): Promise<ParlaySettings> {
  try {
    const raw = await readFile(SETTINGS_PATH, "utf8")
    return { ...DEFAULTS, ...JSON.parse(raw) }
  } catch {
    return { ...DEFAULTS }
  }
}

async function writeSettings(s: ParlaySettings): Promise<void> {
  await mkdir(DATA_DIR, { recursive: true })
  await writeFile(SETTINGS_PATH, JSON.stringify(s, null, 2), "utf8")
}

export function handleSettings(req: Request, pathname: string): Response | null {
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
            voiceClearPhrase:   body.voiceClearPhrase != null ? String(body.voiceClearPhrase) : current.voiceClearPhrase,
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
