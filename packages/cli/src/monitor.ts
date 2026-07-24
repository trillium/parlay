// `parlay monitor` implementation, extracted from index.ts.
//
// Default path is relay-backed: enroll with the central relay and exec the
// `tail -F` monitor wrapper (~1.2MB per agent) instead of an independent ~40MB
// bun poll loop. --legacy-poll keeps the old independent poller for the global
// feed or environments without the relay running.

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
    const code = await proc.exited
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
