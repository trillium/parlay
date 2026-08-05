// ── Where the server persists things ─────────────────────────────────────────
//
// Every file this server WRITES resolves its path here, so one env var can point
// the entire persistence surface at a scratch directory.
//
// PARLAY_DATA_DIR unset (production): each file keeps its historical location —
// chat history, draft, settings, uploads and the channel-declaration file under
// `~/exchange`; the agent registry and the session→channel map under
// `$PAI_DIR/MEMORY/STATE`. Nothing moves.
//
// PARLAY_DATA_DIR set: all of them relocate, flat, into that one directory.
//
// WHY THIS FILE EXISTS (robots-jcjj). Only the chat history and draft used to
// honor PARLAY_DATA_DIR; every other path was hardcoded to the user's home. So a
// test that merely imported this module and called startChat() mutated live
// data — Pulse's boot-smoke test ran the startup prune sweep against the REAL
// agent registry and deleted two of the captain's actual channels. A caller
// cannot opt out of a hardcoded path, so there are no hardcoded write paths any
// more. Add new persisted files HERE, never inline in the module that uses them.

import { join } from "path"
import { homedir } from "os"

export interface ResolvedPaths {
  /** true when PARLAY_DATA_DIR is redirecting the whole surface */
  redirected: boolean
  /** directory holding chat history + draft; index.ts mkdirs this at boot */
  dataDir: string
  historyFile: string
  draftFile: string
  settingsFile: string
  agentChannelsFile: string
  uploadDir: string
  agentsFile: string
  sessionChannelsFile: string
}

/**
 * Pure resolver — every input comes from `env`, nothing is read from module
 * state, so the redirect contract is unit-testable without spawning a process.
 */
export function resolvePaths(env: Record<string, string | undefined> = process.env): ResolvedPaths {
  const home = env.HOME || homedir()
  const override = env.PARLAY_DATA_DIR
  const redirected = typeof override === "string" && override.length > 0

  const exchange = join(home, "exchange")
  const paiState = join(env.PAI_DIR ?? join(home, ".claude", "PAI"), "MEMORY", "STATE")

  // Redirected: everything lands flat in the override dir. Otherwise each file
  // stays exactly where production has always kept it.
  const at = (name: string, productionDir: string) =>
    join(redirected ? (override as string) : productionDir, name)

  return {
    redirected,
    dataDir:             redirected ? (override as string) : exchange,
    historyFile:         at("chat-history.jsonl", exchange),
    draftFile:           at("chat-draft.txt", exchange),
    settingsFile:        at("parlay-settings.json", exchange),
    agentChannelsFile:   at("parlay-agent-channels.json", exchange),
    uploadDir:           at("parlay-uploads", exchange),
    agentsFile:          at("parlay-agents.json", paiState),
    sessionChannelsFile: at("parlay-session-channels.json", paiState),
  }
}

const P = resolvePaths()

export const IS_REDIRECTED        = P.redirected
export const DATA_DIR             = P.dataDir
export const HISTORY_FILE         = P.historyFile
export const DRAFT_FILE           = P.draftFile
export const SETTINGS_FILE        = P.settingsFile
export const AGENT_CHANNELS_FILE  = P.agentChannelsFile
export const UPLOAD_DIR           = P.uploadDir
export const AGENTS_FILE          = P.agentsFile
export const SESSION_CHANNELS_FILE = P.sessionChannelsFile
