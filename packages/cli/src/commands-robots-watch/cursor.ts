// Persisted per-bead status cursor for the poll-daemon: the "what we last saw"
// half of the diff. { "<store>": { "<bead-id>": "<status>" } }.

import { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import type { StoreState } from "./detect"

export type Cursor = Record<string, StoreState>

function stateDir(): string {
  const base = process.env.PARLAY_STATE_HOME || join(homedir(), ".parlay")
  return join(base, "robots-watch")
}
function cursorPath(): string {
  return join(stateDir(), "cursor.json")
}

export function readCursor(): Cursor {
  const p = cursorPath()
  if (!existsSync(p)) return {}
  try {
    const parsed = JSON.parse(readFileSync(p, "utf8"))
    return parsed && typeof parsed === "object" ? parsed : {}
  } catch {
    // A corrupt cursor is treated as empty → every store re-seeds (fires nothing),
    // which is the safe failure: we never replay history, we just lose one diff.
    return {}
  }
}

export function writeCursor(cursor: Cursor): void {
  const dir = stateDir()
  mkdirSync(dir, { recursive: true })
  const tmp = join(dir, `.cursor.${process.pid}.tmp`)
  writeFileSync(tmp, JSON.stringify(cursor, null, 2) + "\n")
  renameSync(tmp, cursorPath()) // atomic swap
}
