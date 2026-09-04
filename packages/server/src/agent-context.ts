import type { AgentInfo } from "./types"
import { join } from "path"
import { existsSync, readFileSync } from "fs"

// Decide, for a POST /api/chat/reply, whether/how the server will route to an
// agent channel. Returns the routable id (plus any on-disk context) or null,
// in which case the caller files the message on the global thread.
//
// The id may come from the request body (`idFromBody`) or, when absent, from
// the server's own `PARLAY_AGENT_ID`. It is routable if it is backed by:
//   1. an on-disk context.json under the server's own $HOME, or
//   2. the server's agent registry (`registered` — parlay-agents.json / the
//      in-memory map, which `parlay listen` / POST /api/chat/register-agent
//      write), or
//   3. the server's own designated id, when the request named no id and the
//      id equals PARLAY_AGENT_ID.
// Case 3 is deliberately narrow: a request that NAMES an id is only ever
// routed by mechanism 1 or 2 — never because some unrelated PARLAY_AGENT_ID
// happens to be in the environment (issue #174).
export function loadAgentContext(
  registered: ReadonlyMap<string, AgentInfo>,
  idFromBody?: string,
): { id: string; context?: Record<string, unknown> } | null {
  const id = (idFromBody || process.env.PARLAY_AGENT_ID || "").trim()
  if (!id) return null

  // Mechanism 1: on-disk context file (unchanged — a valid source).
  const home = process.env.HOME || process.env.USERPROFILE || ""
  const contextPath = join(home, ".parlay", "agents", id, "context.json")
  if (existsSync(contextPath)) {
    try {
      const context = JSON.parse(readFileSync(contextPath, "utf8"))
      return { id, context }
    } catch {
      // Unreadable context file — fall through to registry.
    }
  }

  // Mechanism 2: the server's own agent registry. An id the server knows
  // (a `parlay listen` agent registered over HTTP, or a persisted
  // parlay-agents.json entry) is routable. The registry is the source of truth
  // for "who gets a tab", so its presence is more truthful than the global
  // thread, and strictly narrower than the old env-var presence check.
  if (registered.has(id)) return { id, context: undefined }

  // Mechanism 3: the server's designated agent id. Only the id that IS
  // PARLAY_AGENT_ID routes here — never an arbitrary body id merely because
  // some PARLAY_AGENT_ID exists in the environment.
  const designated = (process.env.PARLAY_AGENT_ID || "").trim()
  if (designated && designated === id) return { id, context: undefined }

  return null
}
