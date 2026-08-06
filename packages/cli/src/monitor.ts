// `parlay monitor` implementation, extracted from index.ts.
//
// Default path is relay-backed: enroll with the central relay and exec the
// `tail -F` monitor wrapper (~1.2MB per agent) instead of an independent ~40MB
// bun poll loop. --legacy-poll keeps the old independent poller for the global
// feed or environments without the relay running.
//
// ── Deregistration watchdog (robots-ycfa) ────────────────────────────────────
// A monitor outlives whatever launched it. When the owning session dies without
// tearing down — a test fixture that spawned an agent, a harness that was
// killed — the monitor is reparented to init and keeps streaming forever, and
// nothing on the machine ever reaps it. 82 such orphans accumulated, each
// holding a bun process, a `tail -F`, and a live channel in the captain's
// panel.
//
// The server side of that fix removes the leaked channel and refuses to let its
// own poll re-create it. This is the other half: a monitor periodically asks the
// registry whether it still exists, and exits when the answer is a clear no. The
// two halves compose — prune the channel, and the process that fed it retires
// itself within a minute — so a leak self-heals instead of accumulating.
//
// Every ambiguity resolves toward STAYING ALIVE, because a monitor that quits
// while its agent is real goes registered-but-deaf (robots-dcag): a failed
// request, a non-2xx, an unparseable body, or a body that is not an array all
// reset the evidence. Only repeated, successful, well-formed answers that omit
// this agent count — the server has to say "you are gone" twice in a row.

export interface MonitorDeps {
  server: string
  exitUsage: number
  die: (msg: string, code?: number) => never
  helpWanted: (cmd: string, args: string[]) => boolean
  parseArgs: (
    cmd: string,
    args: string[],
    flags?: string[],
    valueFlags?: string[],
  ) => { positionals: string[]; opts: Record<string, string | true> }
}

/** How often the watchdog asks the registry whether this agent still exists. */
export const REGISTRY_CHECK_MS = 60_000

/**
 * Consecutive clean "you are not in the registry" answers required before a
 * monitor retires itself. Two, so a single sweep landing between an agent's
 * unregister and its re-register cannot evict a live monitor.
 */
export const MISSING_STRIKES_TO_RETIRE = 2

/**
 * Classify one registry response. Pure, so the retire/stay decision is testable
 * without a server, a clock, or a child process.
 *
 * `body` is whatever came back from GET /api/chat/agents: the parsed JSON on a
 * 2xx, or null/undefined when the request failed or could not be parsed.
 * Returns the strike count carried forward — 0 means "evidence reset".
 */
export function registryStrike(
  agent: string,
  body: unknown,
  ok: boolean,
  priorStrikes: number,
): number {
  // Anything short of a well-formed answer is not evidence of anything.
  if (!ok || !Array.isArray(body)) return 0
  const present = body.some(
    entry => typeof entry === "object" && entry !== null && (entry as { id?: unknown }).id === agent,
  )
  // An empty registry means the server just restarted and has not been
  // re-populated, not that this agent was evicted. Never a strike.
  if (body.length === 0) return 0
  return present ? 0 : priorStrikes + 1
}

/**
 * Poll the registry in the background and call `retire` once the server has
 * cleanly reported this agent missing MISSING_STRIKES_TO_RETIRE times running.
 * Returns a function that stops the watchdog.
 *
 * Set PARLAY_NO_REGISTRY_WATCHDOG=1 to disable — for a monitor deliberately
 * armed before its channel is registered, and as the escape hatch if this ever
 * misjudges a live agent.
 */
export function startRegistryWatchdog(
  server: string,
  agent: string,
  retire: () => void,
  intervalMs: number = REGISTRY_CHECK_MS,
): () => void {
  if (process.env.PARLAY_NO_REGISTRY_WATCHDOG === "1") return () => {}
  let strikes = 0
  const timer = setInterval(async () => {
    let ok = false
    let body: unknown = null
    try {
      const res = await fetch(`${server}/api/chat/agents`)
      ok = res.ok
      if (ok) body = await res.json()
    } catch { /* unreachable server — fail open, handled by registryStrike */ }
    strikes = registryStrike(agent, body, ok, strikes)
    if (strikes < MISSING_STRIKES_TO_RETIRE) return
    clearInterval(timer)
    process.stderr.write(
      `parlay monitor: '${agent}' is no longer in the server's registry — it was unregistered.\n` +
      `parlay monitor:   Retiring this monitor rather than streaming a channel nobody owns.\n` +
      `parlay monitor:   Re-arm with 'parlay listen --agent ${agent}' if this was wrong.\n`,
    )
    retire()
  }, intervalMs)
  // Never hold the process open on the watchdog alone — the child's exit is what
  // ends this command.
  timer.unref?.()
  return () => clearInterval(timer)
}

export async function runMonitor(args: string[], deps: MonitorDeps): Promise<void> {
  const { server, exitUsage, die, helpWanted, parseArgs } = deps
  if (helpWanted("monitor", args)) return
  const { opts } = parseArgs("monitor", args, ["--legacy-poll", "--notify-safe"], ["--agent"])
  const agent = opts["--agent"] as string | undefined
  // --notify-safe: cap long CHAT_MSG lines so a harness Monitor tool cannot cut
  // them mid-word silently. See tools/monitor/parlay-monitor.sh for the why.
  const notifySafe = opts["--notify-safe"] === true
  const notifyBudget = Number(process.env.PARLAY_NOTIFY_BUDGET) || 400

  // Default: relay-backed. `parlay monitor --agent <id>` enrolls with the central
  // relay and execs `tail -F` on the agent's spool — one process per agent at the
  // ~1.2MB tail floor. The relay (tools/relay/parlay-relay) must be running; a
  // single relay fans out to every agent (the N×monitor half of the 1+N split).
  if (!opts["--legacy-poll"]) {
    if (!agent) {
      return die("parlay monitor: --agent <id> is required (or use --legacy-poll for the global feed)", exitUsage)
    }
    const script = new URL("../../../tools/monitor/parlay-monitor.sh", import.meta.url).pathname
    // Bun.spawn with inherited stdio → the harness Monitor sees CHAT_MSG lines on
    // stdout exactly as before. The wrapper handles enroll + tail -F.
    const scriptArgs = ["--agent", agent, ...(notifySafe ? ["--notify-safe"] : [])]
    const proc = Bun.spawn(["bash", script, ...scriptArgs], {
      stdio: ["inherit", "inherit", "inherit"],
      env: { ...process.env, PARLAY_SERVER: server },
    })
    const stopWatchdog = startRegistryWatchdog(server, agent, () => proc.kill())
    const code = await proc.exited
    stopWatchdog()
    process.exit(code)
  }

  // Legacy independent poll loop — no relay.
  const channelParam = agent ? `&channel=${encodeURIComponent(agent)}` : ""
  let lastId = ""
  process.stderr.write(`parlay monitor (legacy poll) — server ${server}${agent ? ` channel ${agent}` : " (global)"}\n`)
  process.stderr.write(`Next (from another shell): parlay send <text...>\n`)
  while (true) {
    try {
      const res = await fetch(`${server}/api/chat/poll?after=${lastId}${channelParam}`)
      // 410 Gone: the channel was deliberately unregistered. Retrying would
      // re-create it and poll forever (robots-ycfa) — stop instead.
      if (res.status === 410) {
        process.stderr.write(
          `parlay monitor: channel '${agent}' was unregistered (410) — stopping.\n` +
          `parlay monitor:   Re-arm with 'parlay listen --agent ${agent}' if this was wrong.\n`,
        )
        return
      }
      if (!res.ok) { await Bun.sleep(2000); continue }
      const msg = await res.json() as { timeout?: boolean; id?: string; role?: string; text?: string; from?: string }
      if (msg.timeout) continue
      if (msg.id && msg.role && msg.text != null) {
        lastId = msg.id
        const fromSuffix = msg.from ? `|from:${msg.from}` : ""
        let line = `CHAT_MSG|${msg.id}|${msg.role}|${msg.text}${fromSuffix}`
        if (notifySafe && line.length > notifyBudget) {
          line = `${line.slice(0, notifyBudget)} ⟪+${line.length - notifyBudget} chars truncated for notification — run: parlay history 30 --full⟫`
        }
        process.stdout.write(`${line}\n`)
      }
    } catch {
      await Bun.sleep(3000)
    }
  }
}
