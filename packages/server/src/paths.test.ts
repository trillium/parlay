// Regression tests for the PARLAY_DATA_DIR redirect contract (robots-jcjj).
//
// The bug: only chat history + draft honored PARLAY_DATA_DIR. Every other
// persisted file was hardcoded to the user's home, so Pulse's boot-smoke test
// — which imports this module and calls startChat() — ran the startup prune
// sweep against the REAL agent registry and deleted two live agent channels.
//
// The contract these tests pin down:
//   1. with PARLAY_DATA_DIR set, NO persisted path escapes that directory;
//   2. with it unset, every path stays exactly where production has it.
// (1) is the one that matters — it is what makes "point it at a scratch dir"
// an actual guarantee rather than a partial one.

import { describe, expect, test } from "bun:test"
import { join } from "path"
import { resolvePaths, type ResolvedPaths } from "./paths"

const HOME = "/home/tester"
const PAI_DIR = join(HOME, ".claude", "PAI")

/** Every path-valued field of the resolver's output, as [name, value] pairs. */
function pathEntries(p: ResolvedPaths): [string, string][] {
  return Object.entries(p).filter(([, v]) => typeof v === "string") as [string, string][]
}

describe("resolvePaths — PARLAY_DATA_DIR redirect", () => {
  test("redirects EVERY persisted path into the scratch dir", () => {
    const scratch = "/tmp/parlay-scratch-xyz"
    const p = resolvePaths({ HOME, PAI_DIR, PARLAY_DATA_DIR: scratch })

    expect(p.redirected).toBe(true)

    // The load-bearing assertion: nothing escapes. A new persisted file added
    // without going through paths.ts fails here rather than in production.
    const escaped = pathEntries(p).filter(([, v]) => !v.startsWith(scratch + "/") && v !== scratch)
    expect(escaped).toEqual([])
  })

  test("redirect covers the agent registry that the prune sweep deletes from", () => {
    const scratch = "/tmp/parlay-scratch-xyz"
    const p = resolvePaths({ HOME, PAI_DIR, PARLAY_DATA_DIR: scratch })

    // These three are the files the boot-smoke test actually mutated.
    expect(p.agentsFile).toBe(join(scratch, "parlay-agents.json"))
    expect(p.sessionChannelsFile).toBe(join(scratch, "parlay-session-channels.json"))
    expect(p.agentChannelsFile).toBe(join(scratch, "parlay-agent-channels.json"))

    // Explicitly: the live locations must not appear anywhere in the result.
    const live = [
      join(PAI_DIR, "MEMORY", "STATE", "parlay-agents.json"),
      join(HOME, "exchange", "parlay-agent-channels.json"),
    ]
    for (const f of live) expect(pathEntries(p).map(([, v]) => v)).not.toContain(f)
  })

  test("an empty PARLAY_DATA_DIR is treated as unset, not as the filesystem root", () => {
    const p = resolvePaths({ HOME, PAI_DIR, PARLAY_DATA_DIR: "" })
    expect(p.redirected).toBe(false)
    expect(p.historyFile).toBe(join(HOME, "exchange", "chat-history.jsonl"))
  })
})

describe("resolvePaths — production defaults are unchanged", () => {
  const p = resolvePaths({ HOME, PAI_DIR })

  test("not redirected", () => {
    expect(p.redirected).toBe(false)
  })

  test("~/exchange keeps history, draft, settings, declarations, uploads", () => {
    const exchange = join(HOME, "exchange")
    expect(p.dataDir).toBe(exchange)
    expect(p.historyFile).toBe(join(exchange, "chat-history.jsonl"))
    expect(p.draftFile).toBe(join(exchange, "chat-draft.txt"))
    expect(p.settingsFile).toBe(join(exchange, "parlay-settings.json"))
    expect(p.agentChannelsFile).toBe(join(exchange, "parlay-agent-channels.json"))
    expect(p.uploadDir).toBe(join(exchange, "parlay-uploads"))
  })

  test("PAI MEMORY/STATE keeps the agent registry + session→channel map", () => {
    const state = join(PAI_DIR, "MEMORY", "STATE")
    expect(p.agentsFile).toBe(join(state, "parlay-agents.json"))
    expect(p.sessionChannelsFile).toBe(join(state, "parlay-session-channels.json"))
  })

  test("PAI_DIR defaults to ~/.claude/PAI when unset", () => {
    const q = resolvePaths({ HOME })
    expect(q.agentsFile).toBe(join(HOME, ".claude", "PAI", "MEMORY", "STATE", "parlay-agents.json"))
  })
})
