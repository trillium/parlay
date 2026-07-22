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

type AlertResult = { ok?: boolean; channels?: number; delivered?: number; error?: string }

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
  const { positionals, opts } = parseArgs("alert", args, ["--stagger"], ["--agent", "--interval", "--batch"])
  const text = positionals.join(" ").trim()
  if (!text) return die("parlay alert: message text required", EXIT_USAGE)

  const agent = opts["--agent"] as string | undefined
  // A single named agent has nothing to stagger — deliver immediately.
  if (agent) {
    const r = await postAlert({ text, agents: [agent] })
    if (r.error) return die(`alert failed: ${r.error}`)
    console.log(`alert sent to ${r.channels} channel(s), delivered to ${r.delivered} live poller(s)`)
    return nextStep("parlay subscribers")
  }

  const interval = posNum(opts["--interval"] as string | undefined, DEFAULT_INTERVAL_SEC, "--interval")
  const batch = posNum(opts["--batch"] as string | undefined, DEFAULT_BATCH, "--batch")

  // Default: immediate broadcast (unchanged). --stagger opts into spread delivery.
  if (opts["--stagger"] !== true) return immediateBroadcast(text)

  const agents = await getJSON<AgentInfo[]>("/api/chat/agents")
  if (agents.length === 0) return immediateBroadcast(text) // nobody enrolled — nothing to stagger
  return staggerDeliver(text, agents.map((a) => a.id), interval, batch, "manual")
}
