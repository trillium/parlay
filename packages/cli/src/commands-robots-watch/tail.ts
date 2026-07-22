// robots-tail — the PUSH fast path (task-jif2). A byte-offset tailer of
// ~/data/robots/events.jsonl (the emit stream the robots create-wrapper appends
// to), modeled on the server's hook-tailer.ts: every ~1s it reads only the bytes
// past a persisted offset, parses each new line for a robots bead id, and calls
// mechanic-dispatch immediately — sub-~1s create→dispatch latency instead of the
// poll interval. The poll daemon (robots-watch) stays the reconciler fallback for
// any emit that was missed; mechanic-dispatch idempotency makes a double-fire safe.

import { existsSync, statSync, openSync, readSync, closeSync, mkdirSync, readFileSync, writeFileSync, renameSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { parseArgs } from "../args"
import { helpWanted } from "../help"
import { stateDir } from "./cursor"
import { dispatchMechanic } from "./handlers"

function eventsPath(): string {
  return process.env.ROBOTS_EVENTS_FILE || join(homedir(), "data", "robots", "events.jsonl")
}
function offsetPath(): string {
  return join(stateDir(), "tail-offset")
}

// Parse one emit line → a robots bead id, or null (malformed / not a robots id).
export function parseCreatedId(line: string): string | null {
  try {
    const ev = JSON.parse(line)
    const id = typeof ev.id === "string" ? ev.id.trim() : ""
    return /^robots-[a-z0-9]+$/.test(id) ? id : null
  } catch {
    return null
  }
}

// Read the bytes of `path` past `offset`. Returns the new lines and the new offset.
// Handles truncation/rotation (size < offset → restart from 0). Pure I/O, no dispatch.
export function readNewLines(path: string, offset: number): { lines: string[]; offset: number } {
  if (!existsSync(path)) return { lines: [], offset }
  const { size } = statSync(path)
  if (size < offset) offset = 0 // rotated/truncated — restart
  if (size <= offset) return { lines: [], offset }
  const fd = openSync(path, "r")
  const buf = Buffer.alloc(size - offset)
  readSync(fd, buf, 0, buf.length, offset)
  closeSync(fd)
  return { lines: buf.toString("utf8").split("\n").filter(Boolean), offset: size }
}

function readOffset(fallback: number): number {
  try {
    const raw = readFileSync(offsetPath(), "utf8").trim()
    const n = Number(raw)
    return Number.isFinite(n) && n >= 0 ? n : fallback
  } catch {
    return fallback
  }
}
function writeOffset(n: number): void {
  const dir = stateDir()
  mkdirSync(dir, { recursive: true })
  const tmp = join(dir, `.tail-offset.${process.pid}.tmp`)
  writeFileSync(tmp, String(n))
  renameSync(tmp, offsetPath())
}

// One tail pass: dispatch every new robots-created id, persist the advanced offset.
function tick(verbose: boolean): void {
  const path = eventsPath()
  const start = readOffset(existsSync(path) ? statSync(path).size : 0)
  const { lines, offset } = readNewLines(path, start)
  for (const line of lines) {
    const id = parseCreatedId(line)
    if (id) dispatchMechanic(id, verbose)
    else if (verbose) process.stderr.write(`robots-tail: skip unparseable line\n`)
  }
  if (offset !== start) writeOffset(offset)
}

export async function cmdRobotsTail(args: string[]): Promise<void> {
  if (helpWanted("robots-tail", args)) return
  const { opts } = parseArgs("robots-tail", args, ["--once", "--verbose"], [])
  const verbose = opts["--verbose"] === true
  const once = opts["--once"] === true

  const path = eventsPath()
  // First-ever run (no persisted offset) starts at EOF so history is not replayed;
  // a persisted offset resumes there, catching emits that landed while we were down.
  if (!existsSync(offsetPath())) writeOffset(existsSync(path) ? statSync(path).size : 0)

  process.stderr.write(`parlay robots-tail — ${once ? "single pass" : "tailing every 1s"} ${path} (fast path → mechanic-dispatch)\n`)
  tick(verbose)
  if (once) return
  // eslint-disable-next-line no-constant-condition
  while (true) {
    await Bun.sleep(1000)
    try {
      tick(verbose)
    } catch (err) {
      process.stderr.write(`robots-tail: pass failed (continuing): ${String(err)}\n`)
    }
  }
}
