// parlay CLI usage and per-command help text.

import { SERVER } from "./config"

export const USAGE = `parlay — talk to a Parlay chat server (${SERVER})

Usage:
  parlay                          Live status: subscribers, agents, last 3 messages
  parlay status                   Same as bare parlay
  parlay subscribers [--full]     Connection counts (--full: raw JSON)
  parlay agents [--full]          List registered agents (--full: raw JSON)
  parlay send <text...>           Send a message from the human to agents
  parlay say <text...>            Reply to YOUR OWN channel (spawned agents; identity from PARLAY_AGENT_ID)
  parlay reply <text...>          Alias for 'say' (bare 'reply' wraps this)
  parlay scratchpad [<note>]      Your durable task notes; no args = read (bare 'scratchpad' wraps this)
  parlay identity [<fact>]        Your durable self-knowledge; no args = read (bare 'identity' wraps this)
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
  say: `parlay say — reply to your OWN Parlay channel with no boilerplate (for agents spawned by parlay-spawn).\nUsage: parlay say <text...>   |   echo "long reply" | parlay say\n  Identity comes from PARLAY_AGENT_ID (set at spawn); override with --agent <id>.\n  The server keeps your registered name/color, so you only supply the text.\n  Alias: parlay reply. Dead-simple wrapper on PATH: reply "<text>".`,
  scratchpad: `parlay scratchpad — your durable task notes, keyed by PARLAY_AGENT_ID (survives restarts).\nUsage: scratchpad '<note>'   append   |   scratchpad   read   |   scratchpad --clear | --path\n  Also: --agent <id>, and stdin (echo 'note' | scratchpad). Store: ~/.parlay/agents/<id>/scratchpad.md`,
  identity: `parlay identity — your durable self-knowledge (traits, failure modes, lessons), keyed by PARLAY_AGENT_ID.\nUsage: identity '<fact>'   add   |   identity   read   |   identity --clear | --path\n  identity --handoff [<handoff-id>] pin a pointer to your handoff bead (pin only — does not restart).\n  identity --submit [<handoff-id>]  PERSISTENT agent: pin it AND reincarnate — ENDS the session and relaunches you with a\n                                     fresh context that recovers via identity -> handoff -> scratchpad. Add --dry to preview.\n  The handoff id is OPTIONAL: omit it and the current open handoff is resolved from the store, so\n                                     'handoff create … && identity --submit' is ONE atomic act — no room for an interposed\n                                     step to strand your shutdown, and a bare submit recovers a create that already landed.\n  identity --complete <store-item>  SINGLE-USE agent: close the work item you finished (e.g. task-abc → 'task close') and\n                                     END for good — no restart. The counterpart to --submit. Add --dry to preview.\n  Both are the whole shutdown — no separate sudoku/reincarnate step.\n  Also: --agent <id>, and stdin. Read identity on every startup to recover yourself. Store: ~/.parlay/agents/<id>/identity.md`,
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
