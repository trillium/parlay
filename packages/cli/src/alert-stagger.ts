// parlay alert delivery. Default is an immediate broadcast to every poller/agent
// (unchanged — urgent alerts must not be slowed). `--stagger` spreads delivery
// over time instead, to avoid a thundering-herd API spike when a large fleet all
// wakes and fires calls at once (task-j870). Pure decision/chunk/jitter helpers
// are unit-tested; cmdAlert wires them to the network.

import { EXIT_USAGE } from "./config"
import { die, getJSON, postJSON } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"
import { nextStep } from "./format"
import type { AgentInfo } from "./types"

const DEFAULT_INTERVAL_SEC = 1.5
const DEFAULT_BATCH = 1
const DEFAULT_AUTO_THRESHOLD = 8 // fleet size above which a bare alert auto-staggers

type AlertResult = { ok?: boolean; channels?: number; delivered?: number; error?: string }

export type AlertMode = "single" | "immediate" | "stagger"

// Decide how to deliver, given the flags and the live fleet size. Pure → unit-tested
// so this never needs a live fleet to verify. A large fleet auto-staggers even
// without --stagger, so a routine broadcast can't accidentally thunder the herd;
// --no-stagger always forces immediate; a single --agent has nothing to stagger.
export function resolveAlertMode(o: {
  stagger: boolean
  noStagger: boolean
  hasAgent: boolean
  fleetSize: number
  threshold: number
}): AlertMode {
  if (o.hasAgent) return "single"
  if (o.noStagger) return "immediate"
  if (o.stagger) return o.fleetSize > 0 ? "stagger" : "immediate"
  return o.fleetSize > o.threshold ? "stagger" : "immediate" // auto
}

// A positive number from an env var, or the fallback (silently ignores a bad value).
function envNum(name: string, fallback: number): number {
  const raw = process.env[name]
  if (!raw) return fallback
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

// Split ids into batches of `size` (>=1). Pure.
export function chunk<T>(arr: T[], size: number): T[][] {
  const n = Math.max(1, Math.floor(size))
  const out: T[][] = []
  for (let i = 0; i < arr.length; i += n) out.push(arr.slice(i, i + n))
  return out
}

// Jitter a base delay by ±25% so batches don't land on an exact grid. Pure given rnd.
export function jitterMs(baseMs: number, rnd: () => number = Math.random): number {
  return Math.round(baseMs * (0.75 + rnd() * 0.5)) // 0.75×–1.25×
}

// Parse a positive-number flag value, or fall back. dies on a malformed explicit value.
function posNum(raw: string | undefined, fallback: number, label: string): number {
  if (raw === undefined) return fallback
  const n = Number(raw)
  if (!Number.isFinite(n) || n <= 0) die(`parlay alert: ${label} must be a positive number (got '${raw}')`, EXIT_USAGE)
  return n
}

async function postAlert(body: { text: string; agents?: string[] }): Promise<AlertResult> {
  return postJSON<AlertResult>("/api/chat/alert", body)
}

// Immediate broadcast to everyone — the historical default path, byte-identical.
async function immediateBroadcast(text: string): Promise<void> {
  const r = await postAlert({ text })
  if (r.error) return die(`alert failed: ${r.error}`)
  console.log(`alert sent to ${r.channels} channel(s), delivered to ${r.delivered} live poller(s)`)
  nextStep("parlay subscribers")
}

// Staggered fan-out: deliver to agents in batches of `batchSize`, sleeping ~interval
// (jittered) between batches. sleep/rnd are injectable for tests.
export async function staggerDeliver(
  text: string,
  ids: string[],
  intervalSec: number,
  batchSize: number,
  mode: string,
  sleep: (ms: number) => Promise<unknown> = (ms) => Bun.sleep(ms),
  rnd: () => number = Math.random,
): Promise<void> {
  const batches = chunk(ids, batchSize)
  const baseMs = intervalSec * 1000
  let delivered = 0
  const channels = new Set<string>()
  process.stderr.write(
    `parlay alert: staggering to ${ids.length} agent(s) in ${batches.length} batch(es) of ${batchSize}, ~${intervalSec}s apart (${mode})\n`,
  )
  for (let i = 0; i < batches.length; i++) {
    const r = await postAlert({ text, agents: batches[i] })
    if (r.error) {
      process.stderr.write(`  batch ${i + 1}/${batches.length} error: ${r.error}\n`)
    } else {
      delivered += r.delivered ?? 0
      batches[i].forEach((id) => channels.add(id))
      process.stderr.write(`  batch ${i + 1}/${batches.length} [${batches[i].join(",")}] → ${r.delivered ?? 0} delivered\n`)
    }
    if (i < batches.length - 1) await sleep(jitterMs(baseMs, rnd))
  }
  console.log(`staggered alert: ${delivered} delivered across ${channels.size} agent channel(s) in ${batches.length} batch(es)`)
  nextStep("parlay subscribers")
}

export async function cmdAlert(args: string[]): Promise<void> {
  if (helpWanted("alert", args)) return
  const { positionals, opts } = parseArgs("alert", args, ["--stagger", "--no-stagger"], ["--agent", "--interval", "--batch"])
  const text = positionals.join(" ").trim()
  if (!text) return die("parlay alert: message text required", EXIT_USAGE)

  const agent = opts["--agent"] as string | undefined
  // A single named agent has nothing to stagger — deliver immediately (no fetch).
  if (agent) {
    const r = await postAlert({ text, agents: [agent] })
    if (r.error) return die(`alert failed: ${r.error}`)
    console.log(`alert sent to ${r.channels} channel(s), delivered to ${r.delivered} live poller(s)`)
    return nextStep("parlay subscribers")
  }

  const forceStagger = opts["--stagger"] === true
  const forceImmediate = opts["--no-stagger"] === true
  // --no-stagger forces immediate without even enumerating the fleet.
  if (forceImmediate) return immediateBroadcast(text)

  const interval = posNum(opts["--interval"] as string | undefined, envNum("PARLAY_ALERT_STAGGER_INTERVAL", DEFAULT_INTERVAL_SEC), "--interval")
  const batch = posNum(opts["--batch"] as string | undefined, envNum("PARLAY_ALERT_STAGGER_BATCH", DEFAULT_BATCH), "--batch")
  const threshold = envNum("PARLAY_ALERT_STAGGER_THRESHOLD", DEFAULT_AUTO_THRESHOLD)

  // Auto-stagger above the fleet threshold, even without --stagger, so a routine
  // broadcast to a big fleet can't thunder the herd. Enumerate to know the size.
  const agents = await getJSON<AgentInfo[]>("/api/chat/agents")
  const mode = resolveAlertMode({ stagger: forceStagger, noStagger: false, hasAgent: false, fleetSize: agents.length, threshold })
  if (mode === "stagger") {
    return staggerDeliver(text, agents.map((a) => a.id), interval, batch, forceStagger ? "manual" : `auto: fleet ${agents.length} > ${threshold}`)
  }
  return immediateBroadcast(text)
}
