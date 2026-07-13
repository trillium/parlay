import { existsSync, mkdirSync, appendFileSync, readFileSync, writeFileSync } from "fs"
import { join } from "path"
import { homedir } from "os"
import type { ChatMessage } from "./types"

// ── Constants ───────────────────────────────────────────────────────────────

export const HISTORY_DIR  = process.env.PARLAY_DATA_DIR ?? join(homedir(), ".parlay")
export const HISTORY_FILE = join(HISTORY_DIR, "chat-history.jsonl")
export const DRAFT_FILE   = join(HISTORY_DIR, "chat-draft.txt")

// ── In-memory history (shared across modules) ───────────────────────────────

export const history: ChatMessage[] = []

// ── Draft ───────────────────────────────────────────────────────────────────

export let currentDraft = ""

export function loadDraftFromDisk() {
  try { if (existsSync(DRAFT_FILE)) currentDraft = readFileSync(DRAFT_FILE, "utf8") } catch { /* start empty */ }
}

export function saveDraftToDisk(text: string) {
  currentDraft = text
  try { writeFileSync(DRAFT_FILE, text, "utf8") } catch { /* best-effort */ }
}

// ── Persistence ─────────────────────────────────────────────────────────────

export function loadHistory() {
  mkdirSync(HISTORY_DIR, { recursive: true })
  if (!existsSync(HISTORY_FILE)) return
  try {
    const lines = readFileSync(HISTORY_FILE, "utf8").split("\n").filter(Boolean)
    for (const line of lines) {
      try { history.push(JSON.parse(line)) } catch { /* skip corrupt lines */ }
    }
  } catch { /* file unreadable — start fresh */ }
}

export function persistMessage(msg: ChatMessage) {
  try { appendFileSync(HISTORY_FILE, JSON.stringify(msg) + "\n", "utf8") } catch { /* best-effort */ }
}
