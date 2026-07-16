// parlay CLI usage and per-command help text.

import { SERVER } from "./config"

export const USAGE = `parlay — talk to a Parlay chat server (${SERVER})

Usage:
  parlay                          Live status: subscribers, agents, last 3 messages
  parlay status                   Same as bare parlay
  parlay subscribers [--full]     Connection counts (--full: raw JSON)
  parlay agents [--full]          List registered agents (--full: raw JSON)
  parlay send                     List agents you can message
  parlay send --<agent-id> <text> Send attributed message to that agent's channel
  parlay say <text...>            Reply to YOUR OWN channel (spawned agents; identity from PARLAY_AGENT_ID)
  parlay reply <text...>          Alias for 'say' (bare 'reply' wraps this)
  parlay scratchpad [<note>]      Your durable task notes; no args = read (bare 'scratchpad' wraps this)
  parlay identity [<fact>]        Your durable self-knowledge; no args = read (bare 'identity' wraps this)
  parlay alert <text...>          Broadcast an alert to all pollers + agents
  parlay alert --agent <id> <text...>   Alert one agent channel
  parlay history [N] [--full]     Last N messages, truncated (default 20; --full: untruncated)
  parlay monitor --agent <id>     Relay-backed enroll + stream — emits CHAT_MSG lines (for Monitor{})
  parlay monitor --legacy-poll [--agent <id>]   Old independent poll loop (no relay)
  parlay stats                    Message memory/complexity stats (count, sizes, ages, images)
  parlay launch                   List all known agents + live status (from ~/.parlay/agents/)
  parlay launch <id>              Spawn an offline agent via parlay-spawn (uses identity frontmatter)
  parlay variant launch <id>      Fork a live agent into an isolated git-worktree variant
  parlay variant list [<id>]      List variants (optionally filtered to one primary)
  parlay variant merge <id>       Merge variant's insights back into primary (idempotent)
  parlay variant teardown <id>    Merge + remove worktree + unregister variant
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
  send: `parlay send — list or message another agent's channel (all messages visible to the human).\nUsage: parlay send                          → list agents you can message\n       parlay send --<agent-id> "text"     → send attributed message to that agent's channel\nExamples:\n  send                      # prints: send --mayor, send --shepherd, …\n  send --mayor "heads up"   # lands on mayor's channel; human sees it too\n  send --shepherd "done"    # attributed to PARLAY_AGENT_ID (auto-filled)\n  --from <id>               # override sender attribution (rarely needed)\nAll messages land on the target's SHARED human channel — no private side channels.\nThe receiving agent's monitor emits: CHAT_MSG|id|role|text|from:<sender>`,
  say: `parlay say — reply to your OWN Parlay channel with no boilerplate (for agents spawned by parlay-spawn).\nUsage: parlay say <text...>   |   echo "long reply" | parlay say\n  Identity comes from PARLAY_AGENT_ID (set at spawn); override with --agent <id>.\n  The server keeps your registered name/color, so you only supply the text.\n  Alias: parlay reply. Dead-simple wrapper on PATH: reply "<text>".`,
  scratchpad: `parlay scratchpad — your durable task notes, keyed by PARLAY_AGENT_ID (survives restarts).\nUsage: scratchpad '<note>'   append   |   scratchpad   read   |   scratchpad --clear | --path\n  Also: --agent <id>, and stdin (echo 'note' | scratchpad). Store: ~/.parlay/agents/<id>/scratchpad.md`,
  identity: `parlay identity — your durable self-knowledge (traits, failure modes, lessons), keyed by PARLAY_AGENT_ID.\nUsage: identity '<fact>'   add   |   identity   read   |   identity --clear | --path\n  identity --handoff <handoff-id>   pin a pointer to your handoff bead (pin only — does not restart).\n  identity --submit <handoff-id>    PERSISTENT agent: pin it AND reincarnate — ENDS the session and relaunches you with a\n                                     fresh context that recovers via identity -> handoff -> scratchpad. Add --dry to preview.\n  identity --complete <store-item>  SINGLE-USE agent: close the work item you finished (e.g. task-abc → 'task close') and\n                                     END for good — no restart. The counterpart to --submit. Add --dry to preview.\n  Both are the whole shutdown — no separate sudoku/reincarnate step.\n  Also: --agent <id>, and stdin. Read identity on every startup to recover yourself. Store: ~/.parlay/agents/<id>/identity.md`,
  alert: `parlay alert — broadcast an alert to pollers + agents.\nUsage: parlay alert [--agent <id>] <text...>\n  --agent <id>   Deliver only to one agent channel`,
  history: `parlay history — print recent messages (server-bounded).\nUsage: parlay history [N] [--full]\n  N        How many messages (default 20)\n  --full   Untruncated text plus id and channel per message`,
  monitor: `parlay monitor — enroll with the relay and stream CHAT_MSG|id|role|text lines on stdout.\nUsage: parlay monitor --agent <id> [--legacy-poll]\n  --agent <id>    Agent channel to enroll + stream (required unless --legacy-poll)\n  --legacy-poll   Use the old independent poll loop with no relay (global feed if no --agent)\n\nDefault path registers <id> with the central relay (tools/relay/parlay-relay must\nbe running) and execs 'tail -F' on the agent's spool — ~1.2MB per agent, one relay\nfans out to all. See tools/RELAY_MONITOR.md.`,
  stats: `parlay stats — message memory and complexity snapshot from the server.\nFetches up to 2000 messages and reports: count, total estimated size, avg/largest message, user vs agent split, image attachments, action cards, oldest/newest timestamps.\nPair with Ctrl+Shift+D in the panel for bundle load timing and live memory breakdown.`,
  launch: `parlay launch — discover and launch agents defined in ~/.parlay/agents/.\nUsage: parlay launch          → list all known agents with live status\n       parlay launch <id>    → spawn the named agent via parlay-spawn (reads identity.md for name/color/cwd/model)\nOffline agents are clearly marked; spawning fires the standard identity→handoff→scratchpad recovery chain.`,
  variant: `parlay variant — manage variant agents (isolated git-worktree forks of an existing agent).\nSubcommands:\n  launch <primary-id> [--label <suffix>] [--model MODEL]\n      Fork a primary into a new variant. Creates a git worktree at ~/.parlay/worktrees/<variant-id>.\n      Label defaults to wt1, wt2, … (auto-incremented). Variant id: <primary>-<label>.\n  list [<primary-id>]\n      List all variants, optionally filtered to a specific primary.\n  merge <variant-id>\n      Append novel identity facts + scratchpad notes from the variant into the primary.\n      Deduplicated by content; merged lines tagged [from: <variant-id>]. Idempotent.\n  teardown <variant-id> [--force]\n      Merge insights (unless --force), remove the git worktree, unregister from Parlay,\n      and delete the variant home. Refuses if unmerged insights exist unless --force.`,
  "lavish-import": `parlay lavish-import — import lavish sessions via the bundled importer.\nUsage: parlay lavish-import`,
}

export function helpWanted(cmd: string, args: string[]): boolean {
  if (!args.includes("--help") && !args.includes("-h")) return false
  console.log(HELP[cmd] ?? USAGE)
  return true
}
