// Fold §3.6 — the agent→supervisor keyed status verb.
//
// The fold (design doc §3.6, captain-private, not in this repo) ports firstmate's supervision
// signal into a thin parlay verb — a sibling to reply/scratchpad/identity. It
// COLLIDED with the historical `status` name, but that name was only a redundant
// fall-through alias of bare `parlay` (`case undefined: case "status" →
// cmdStatus`), carrying zero unique behavior. So the resolution (task-ve2v)
// RETIRES the alias and binds `status` to this verb: the panel/fleet snapshot
// stays on bare `parlay` (cmdStatus in ./commands); `parlay status` now
// emits/reads the keyed stream.
//   parlay status <verb> [--key <slug>] "<note>"   → APPEND a keyed status line
//   parlay status                                  → READ this agent's status file

import { appendFileSync, existsSync, mkdirSync, readFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { EXIT_USAGE } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

const STATUS_VERBS = ["working", "needs-decision", "blocked", "paused", "done", "failed", "resolved"] as const
const KEY_SLUG_RE = /^[A-Za-z0-9._-]+$/

// Sink resolution — the whole point of the env indirection (fold §3.6): write to
// $PARLAY_STATUS_FILE when set (firstmate injects it at spawn, and its fm-watch
// loop reads that exact file), else the parlay-native default
// ~/.parlay/agents/<id>/status keyed off PARLAY_AGENT_ID. Same verb, two homes —
// the agent code is identical whoever launched it.
export function statusSink(): { agent: string; file: string } {
  const env = process.env.PARLAY_STATUS_FILE?.trim()
  const agent = (process.env.PARLAY_AGENT_ID ?? "").trim()
  if (env) return { agent, file: env }
  if (!agent)
    return die("parlay status: no agent identity — set PARLAY_STATUS_FILE, or run inside a parlay-spawn'd agent (sets PARLAY_AGENT_ID)", EXIT_USAGE) as never
  const dir = join(homedir(), ".parlay", "agents", agent)
  mkdirSync(dir, { recursive: true })
  return { agent, file: join(dir, "status") }
}

// Build the append line in firstmate's exact grammar (fm-classify-lib.sh): the
// optional "[key=<slug>]" token sits between the verb and the colon, so fm's
// status_line_verb / _fm_decision_key parse both cleanly. Exported for tests.
export function statusLine(verb: string, key: string | undefined, note: string): string {
  return key
    ? `${verb} [key=${key}]:${note ? ` ${note}` : ""}\n`
    : `${verb}:${note ? ` ${note}` : ""}\n`
}

export function cmdStatusVerb(args: string[]) {
  if (helpWanted("status", args)) return
  const { positionals, opts } = parseArgs("status", args, [], ["--key"])

  // Bare `parlay status` → READ this agent's own keyed status file. (The panel/
  // fleet snapshot that this name used to alias now lives only on bare `parlay`.)
  if (positionals.length === 0) {
    const { file } = statusSink()
    if (!existsSync(file)) {
      console.log('(no status yet — write one with: parlay status working "<note>")')
      return
    }
    process.stdout.write(readFileSync(file, "utf8"))
    return
  }

  const verb = positionals[0]
  if (!STATUS_VERBS.includes(verb as (typeof STATUS_VERBS)[number]))
    return die(`parlay status: unknown verb "${verb}" — one of: ${STATUS_VERBS.join(", ")}`, EXIT_USAGE)

  const key = (opts["--key"] as string | undefined)?.trim()
  if (key !== undefined && !KEY_SLUG_RE.test(key))
    return die(`parlay status: invalid --key "${key}" — slug chars are [A-Za-z0-9._-]`, EXIT_USAGE)

  const note = positionals.slice(1).join(" ").trim()
  const { file } = statusSink()
  appendFileSync(file, statusLine(verb, key, note))
  console.log(`status ${verb}${key ? ` [key=${key}]` : ""} → ${file}`)
}
