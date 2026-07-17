// parlay doctor + health: glanceable diagnosis surfaces.
//
// `health` is the SERVER'S vitals (relay, subscribers, memory, eval-engine) —
// same view for every caller. `doctor` is THIS AGENT'S self-diagnosis: each
// check prints PASS/WARN/FAIL with the fix command for anything broken, keeps
// going past failures (a dead server must not hide a corrupt identity file),
// and exits 1 if anything FAILed so scripts can gate on it.

import { existsSync, readFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { SERVER } from "./config"
import { helpWanted } from "./help"
import type { AgentInfo, SubscribersInfo } from "./types"

// Engine URL mirrors the server-side default (eval-relay.ts) — same-host deploy.
const ENGINE = process.env.PARLAY_EVAL_ENGINE_URL ?? "http://127.0.0.1:4343"

// Fetch that reports failure instead of exiting — doctor must run every check.
async function tryJSON<T>(base: string, path: string): Promise<{ ok: true; data: T } | { ok: false; err: string }> {
  try {
    const res = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(3_000) })
    if (!res.ok) return { ok: false, err: `${res.status} ${res.statusText}` }
    return { ok: true, data: (await res.json()) as T }
  } catch (err) {
    return { ok: false, err: String(err) }
  }
}

// ── parlay health — server vitals ───────────────────────────────────────────────

export async function cmdHealth(args: string[]) {
  if (helpWanted("health", args)) return
  let sick = false

  const subs = await tryJSON<SubscribersInfo & { memory?: Record<string, number>; history?: Record<string, number> }>(SERVER, "/api/chat/subscribers")
  if (!subs.ok) {
    sick = true
    console.log(`FAIL  relay ${SERVER} — ${subs.err}`)
    console.log(`      fix: is Pulse running? curl ${SERVER}/api/chat/subscribers`)
  } else {
    const d = subs.data
    console.log(`ok    relay ${SERVER} — ${d.parlay?.clients ?? 0} client(s), ${d.poll?.count ?? 0} poller(s), ${d.registered?.count ?? 0} agent(s)`)
    if (d.memory) console.log(`ok    memory — rss ${d.memory.rssMB}MB, heap ${d.memory.heapUsedMB}MB; history ${d.history?.count ?? "?"} msgs (${d.history?.approxKB ?? "?"}KB)`)
  }

  // Pulse wrapper health (present when the relay runs inside Pulse on :31337).
  const pulse = await tryJSON<{ status?: string; uptime?: number; pid?: number }>(SERVER, "/api/pulse/health")
  if (pulse.ok) {
    const up = pulse.data.uptime !== undefined ? `, up ${Math.round(pulse.data.uptime / 60)}min` : ""
    console.log(`ok    pulse — status ${pulse.data.status}, pid ${pulse.data.pid}${up}`)
  } else {
    console.log(`--    pulse health endpoint not present (standalone relay) — ${pulse.err}`)
  }

  const engine = await tryJSON<{ ok?: boolean; protocol?: number }>(ENGINE, "/health")
  if (engine.ok && engine.data.ok) {
    console.log(`ok    eval-engine ${ENGINE} — protocol v${engine.data.protocol}`)
  } else {
    sick = true
    console.log(`FAIL  eval-engine ${ENGINE} — ${engine.ok ? "unhealthy response" : engine.err}`)
    console.log(`      fix: cd ~/code/parlay/packages/eval-engine && nohup ./parlay-eval-engine > engine.log 2>&1 &`)
  }

  if (sick) process.exitCode = 1
}

// ── parlay doctor — this agent's self-diagnosis ─────────────────────────────────

type Verdict = "PASS" | "WARN" | "FAIL"
function report(v: Verdict, what: string, fix?: string): Verdict {
  console.log(`${v.padEnd(5)} ${what}`)
  if (fix && v !== "PASS") console.log(`      fix: ${fix}`)
  return v
}

export async function cmdDoctor(args: string[]) {
  if (helpWanted("doctor", args)) return
  const verdicts: Verdict[] = []
  const agent = (process.env.PARLAY_AGENT_ID ?? "").trim()

  // 1. Identity env — everything else keys off it.
  verdicts.push(agent
    ? report("PASS", `PARLAY_AGENT_ID = ${agent}`)
    : report("FAIL", "PARLAY_AGENT_ID is not set",
        "run inside a parlay-spawn'd agent, or: export PARLAY_AGENT_ID=<id>"))

  // 2. Server reachable.
  const subs = await tryJSON<SubscribersInfo & { presence?: Array<{ channel: string; status: string; lastSeen: string | null }> }>(SERVER, "/api/chat/subscribers")
  verdicts.push(subs.ok
    ? report("PASS", `server reachable at ${SERVER}`)
    : report("FAIL", `server unreachable at ${SERVER} — ${subs.ok ? "" : subs.err}`,
        `check Pulse/relay is up; env PARLAY_SERVER controls the target`))

  // 3. Registration + 4. listening presence (need agent + server).
  if (agent && subs.ok) {
    const agents = await tryJSON<AgentInfo[]>(SERVER, "/api/chat/agents")
    const registered = agents.ok && agents.data.some(a => a.id === agent)
    verdicts.push(registered
      ? report("PASS", `registered as "${agent}" on the relay`)
      : report("WARN", `"${agent}" not in the agent registry`,
          `first poll auto-registers: parlay monitor --agent ${agent} (via Monitor{})`))

    const pres = subs.data.presence?.find(p => p.channel === agent)
    if (pres?.status === "listening") {
      verdicts.push(report("PASS", `monitor listening (last poll ${pres.lastSeen ?? "?"})`))
    } else {
      verdicts.push(report("WARN", `monitor not listening (presence: ${pres?.status ?? "unknown"}) — captain messages will queue, not stream`,
        `arm it: Monitor({ command: "parlay monitor --agent ${agent}", persistent: true })`))
    }
  }

  // 5. Memory surfaces on disk.
  if (agent) {
    const base = process.env.PARLAY_AGENT_HOME || join(homedir(), ".parlay", "agents")
    const dir = join(base, agent)
    for (const kind of ["identity", "scratchpad"] as const) {
      const file = join(dir, `${kind}.md`)
      if (!existsSync(file)) {
        verdicts.push(report("WARN", `${kind}.md missing (${file})`,
          kind === "identity" ? `seed it: identity --register --name <name> --color <hex>` : `first write creates it: scratchpad '<note>'`))
        continue
      }
      const txt = readFileSync(file, "utf8")
      if (kind === "identity") {
        const fm = txt.match(/^---\n([\s\S]*?)\n---/)
        const id = fm?.[1].match(/^id:\s*"?([^"\n]*)"?/m)?.[1]
        if (!fm) verdicts.push(report("WARN", "identity.md has no frontmatter launch spec",
          "re-seed: identity --register (parlay-spawn does this at spawn)"))
        else if (id && id !== agent) verdicts.push(report("FAIL", `identity.md frontmatter id "${id}" != PARLAY_AGENT_ID "${agent}"`,
          "identity --register overwrites the spec with the current id"))
        else verdicts.push(report("PASS", `identity.md ok (${txt.length} bytes, launch spec present)`))
        const handoff = txt.match(/📎 Handoff:\s*(\S+)/)?.[1]
        if (handoff) console.log(`      note: handoff pointer → ${handoff} (run: handoff show ${handoff})`)
      } else {
        verdicts.push(report("PASS", `scratchpad.md ok (${txt.length} bytes)`))
      }
    }
  }

  // 6. Eval-engine (informational — agents don't need it to talk).
  const engine = await tryJSON<{ ok?: boolean }>(ENGINE, "/health")
  verdicts.push(engine.ok && engine.data.ok
    ? report("PASS", `eval-engine healthy at ${ENGINE}`)
    : report("WARN", `eval-engine unreachable at ${ENGINE} — panel voice commands degraded`,
        "cd ~/code/parlay/packages/eval-engine && nohup ./parlay-eval-engine > engine.log 2>&1 &"))

  const fails = verdicts.filter(v => v === "FAIL").length
  const warns = verdicts.filter(v => v === "WARN").length
  console.log(fails ? `\n${fails} FAIL, ${warns} warn — fix the FAILs above` : `\nall clear (${warns} warn)`)
  if (fails) process.exitCode = 1
}
