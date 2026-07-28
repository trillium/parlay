// `parlay listen` — one-call agent self-enrollment (task-fold: register +
// announce + monitor used to be three separate agent-driven steps, fragile
// across restarts). This collapses them into a single atomic call:
//   1. add-self-to-agent-registry: POST /api/chat/register-agent (identity +
//      optional --caps), so the tab/registry entry exists under this id.
//   2. Announce "listening" on the agent's own channel via /api/chat/reply.
//   3. exec into the poll loop — the SAME relay-backed monitor as
//      `parlay monitor`, reused (not duplicated) via runMonitor.
//
// Idempotent: register-agent is an upsert and reply just posts a message, so
// re-running `parlay listen --agent <id>` on every restart (e.g. from a
// SessionStart hook) re-registers and re-announces with no side effects.

import { SERVER, EXIT_USAGE } from "./config"
import { die, postJSON } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"
import { colorFromId } from "./identity-ephemeral"
import { runMonitor, type MonitorDeps } from "./monitor"

export interface ListenDeps extends MonitorDeps {
  postJSON: <T>(path: string, body: unknown) => Promise<T>
  // Injectable for tests; defaults to the real runMonitor (which never returns).
  runMonitor?: (args: string[], deps: MonitorDeps) => Promise<void>
}

export async function runListen(args: string[], deps: ListenDeps): Promise<void> {
  const { exitUsage, die, helpWanted, parseArgs, postJSON } = deps
  if (helpWanted("listen", args)) return

  const { opts } = parseArgs("listen", args, ["--legacy-poll"], ["--agent", "--name", "--color", "--caps"])
  const agent = (opts["--agent"] as string | undefined)?.trim()
  if (!agent) return die("parlay listen: --agent <id> is required", exitUsage)

  const name = (opts["--name"] as string | undefined)?.trim() || agent
  const color = (opts["--color"] as string | undefined)?.trim() || colorFromId(agent)

  let caps: unknown
  if (opts["--caps"]) {
    try { caps = JSON.parse(opts["--caps"] as string) }
    catch { return die(`parlay listen: --caps must be valid JSON (got '${opts["--caps"]}')`, exitUsage) }
  }

  // 1. add-self-to-agent-registry — identity + capabilities.
  process.stderr.write(`parlay listen: registering '${agent}' …\n`)
  await postJSON<{ ok?: boolean; error?: string }>("/api/chat/register-agent", {
    id: agent, name, color, ...(caps !== undefined ? { caps } : {}),
  })

  // 2. Announce presence on the agent's own channel.
  await postJSON<{ ok?: boolean; error?: string }>("/api/chat/reply", {
    text: `listening — monitor armed, ready for messages.`, agent,
  })
  process.stderr.write(`parlay listen: announced — arming monitor …\n`)

  // 3. exec into the poll loop. Reuses runMonitor verbatim — same mechanism as
  // `parlay monitor --agent <id>`, so a harness Monitor{} wakes on CHAT_MSG lines.
  // Never returns (runMonitor calls process.exit on the relay path).
  const monitorArgs = ["--agent", agent, ...(opts["--legacy-poll"] ? ["--legacy-poll"] : [])]
  const monitorImpl = deps.runMonitor ?? runMonitor
  await monitorImpl(monitorArgs, { server: deps.server, exitUsage, die, helpWanted, parseArgs })
}

export async function cmdListen(args: string[]) {
  return runListen(args, { server: SERVER, exitUsage: EXIT_USAGE, die, helpWanted, parseArgs, postJSON })
}
