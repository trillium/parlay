import { EXIT_USAGE } from "../config"
import { die } from "../http"
import { parseArgs } from "../args"
import { helpWanted } from "../help"
import { statusSink, statusLine } from "../commands-status"
import { appendFileSync } from "fs"

// ── parlay idle [hours] ───────────────────────────────────────────────────────
// Signals the agent is going idle for a given duration. Posts a 'paused' status
// with an estimated resume time, then prints shutdown guidance.
export function cmdIdle(args: string[]) {
  if (helpWanted("idle", args)) return
  const { positionals } = parseArgs("idle", args, [])
  const raw = positionals[0] ?? "1"
  const hours = parseFloat(raw)
  if (!Number.isFinite(hours) || hours <= 0)
    return die(`parlay idle: hours must be a positive number (got: '${raw}')`, EXIT_USAGE)

  const resumeMs = Date.now() + hours * 60 * 60 * 1000
  const resumeISO = new Date(resumeMs).toISOString().slice(0, 16) + "Z"
  const label = hours === Math.floor(hours) ? `${hours}h` : `${hours.toFixed(1)}h`
  const note = `idle for ${label} — resume ~${resumeISO}`

  const { file, agent } = statusSink()
  appendFileSync(file, statusLine("paused", undefined, note))
  console.log(`status paused → ${file}`)
  console.log(`idle: ${agent} going quiet for ${label} (resume ~${resumeISO})`)
  console.log(`\nWhen resuming: run 'parlay status working "resuming"' to signal activity.`)
  console.log(`To park with a handoff instead: run 'parlay drawdown' then 'identity --park'.`)
}
