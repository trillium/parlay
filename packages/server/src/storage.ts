import { existsSync, mkdirSync, appendFileSync, readFileSync, writeFileSync, statSync, renameSync } from "fs"
import { join } from "path"
import { homedir } from "os"
import type { ChatMessage } from "./types"

// ── Constants ───────────────────────────────────────────────────────────────

export const HISTORY_DIR  = process.env.PARLAY_DATA_DIR ?? join(homedir(), ".parlay")
export const HISTORY_FILE = join(HISTORY_DIR, "chat-history.jsonl")
export const DRAFT_FILE   = join(HISTORY_DIR, "chat-draft.txt")

// ── In-memory history (shared across modules) ───────────────────────────────

export const history: ChatMessage[] = []

// id → index into history. The poll endpoint resolves after-id cursors on
// every long-poll cycle; a Map keeps that O(1) instead of an O(n) scan.
export const historyIndex = new Map<string, number>()

export function pushToHistory(msg: ChatMessage) {
  historyIndex.set(msg.id, history.length)
  history.push(msg)
}

// Any bulk mutation of history (clear, rotation trim) invalidates indices.
export function rebuildHistoryIndex() {
  historyIndex.clear()
  history.forEach((m, i) => historyIndex.set(m.id, i))
}

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
      try { pushToHistory(JSON.parse(line)) } catch { /* skip corrupt lines */ }
    }
  } catch { /* file unreadable — start fresh */ }
}

const MAX_HISTORY_BYTES = 5 * 1024 * 1024
const ROTATE_KEEP = 200

// Rotate the jsonl once it exceeds 5 MB: rename with a date suffix, keep only
// the most recent ROTATE_KEEP messages in memory, and start a fresh file
// seeded with those messages.
function rotateHistoryIfNeeded() {
  try {
    if (statSync(HISTORY_FILE).size <= MAX_HISTORY_BYTES) return
    const stamp = new Date().toISOString().slice(0, 10)
    let dest = join(HISTORY_DIR, `chat-history.${stamp}.jsonl`)
    for (let n = 1; existsSync(dest); n++) dest = join(HISTORY_DIR, `chat-history.${stamp}.${n}.jsonl`)
    renameSync(HISTORY_FILE, dest)
    history.splice(0, Math.max(0, history.length - ROTATE_KEEP))
    rebuildHistoryIndex()
    writeFileSync(HISTORY_FILE, history.map(m => JSON.stringify(m)).join("\n") + (history.length ? "\n" : ""), "utf8")
  } catch { /* best-effort */ }
}

export function persistMessage(msg: ChatMessage) {
  try {
    appendFileSync(HISTORY_FILE, JSON.stringify(msg) + "\n", "utf8")
    rotateHistoryIfNeeded()
  } catch { /* best-effort */ }
}
