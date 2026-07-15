// parlay CLI usage and per-command help text.

import { SERVER } from "./config"

export const USAGE = `parlay — talk to a Parlay chat server (${SERVER})

Usage:
  parlay                          Live status: subscribers, agents, last 3 messages
  parlay status                   Same as bare parlay
  parlay subscribers [--full]     Connection counts (--full: raw JSON)
  parlay agents [--full]          List registered agents (--full: raw JSON)
  parlay send <text...>           Send a message from the human to agents
  parlay alert <text...>          Broadcast an alert to all pollers + agents
  parlay alert --agent <id> <text...>   Alert one agent channel
  parlay history [N] [--full]     Last N messages, truncated (default 20; --full: untruncated)
  parlay monitor --agent <id>     Relay-backed enroll + stream — emits CHAT_MSG lines (for Monitor{})
  parlay monitor --legacy-poll [--agent <id>]   Old independent poll loop (no relay)
  parlay lavish-import            Import lavish sessions
  parlay help                     Show this help

Any subcommand accepts --help. Exit codes: 0 ok, 1 server/runtime error, 2 usage error.

Env:
  PARLAY_SERVER   Server base URL (default http://localhost:4242)
`

const HELP: Record<string, string> = {
  status: `parlay status — live snapshot: subscriber counts, agent list, last 3 messages.\nUsage: parlay [status]`,
  subscribers: `parlay subscribers — connection counts (panel clients, pollers, registered agents).\nUsage: parlay subscribers [--full]\n  --full   Print the raw JSON from /api/chat/subscribers`,
  agents: `parlay agents — registered agents, one per line (id, name, color).\nUsage: parlay agents [--full]\n  --full   Print the raw JSON from /api/chat/agents`,
  send: `parlay send — send a message from the human to agents.\nUsage: parlay send <text...>`,
  alert: `parlay alert — broadcast an alert to pollers + agents.\nUsage: parlay alert [--agent <id>] <text...>\n  --agent <id>   Deliver only to one agent channel`,
  history: `parlay history — print recent messages (server-bounded).\nUsage: parlay history [N] [--full]\n  N        How many messages (default 20)\n  --full   Untruncated text plus id and channel per message`,
  monitor: `parlay monitor — enroll with the relay and stream CHAT_MSG|id|role|text lines on stdout.\nUsage: parlay monitor --agent <id> [--legacy-poll]\n  --agent <id>    Agent channel to enroll + stream (required unless --legacy-poll)\n  --legacy-poll   Use the old independent poll loop with no relay (global feed if no --agent)\n\nDefault path registers <id> with the central relay (tools/relay/parlay-relay must\nbe running) and execs 'tail -F' on the agent's spool — ~1.2MB per agent, one relay\nfans out to all. See tools/RELAY_MONITOR.md.`,
  "lavish-import": `parlay lavish-import — import lavish sessions via the bundled importer.\nUsage: parlay lavish-import`,
}

export function helpWanted(cmd: string, args: string[]): boolean {
  if (!args.includes("--help") && !args.includes("-h")) return false
  console.log(HELP[cmd] ?? USAGE)
  return true
}
