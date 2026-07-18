// two-stack — true code-level split test. Boot TWO fully isolated sandboxes, one
// per checkout (baseline vs feature branch), and run the SAME probe suite against
// each. Because each sandbox builds its own relay/engine from its own source and
// runs the server from its own tree, differences in behavior are attributable to
// the code, not the environment. Zero prod contact throughout.

import { sandboxUp, sandboxDown } from "./sandbox"
import { registerAgent, sendViaAObserveViaB, subscribers } from "./probe"
import { renderTable, overallVerdict, type Row } from "./table"

export interface TwoStackOptions {
  aDir: string
  bDir: string
  withEngine: boolean
}

export interface TwoStackResult {
  rows: Row[]
  verdict: "PASS" | "FAIL"
}

function fmtMs(ms: number | undefined): string {
  return ms === undefined ? "—" : `${ms.toFixed(0)}ms`
}

/**
 * Run the single-stack probe suite against ONE sandbox door. Same store,
 * same door for send+observe (a stack talks to itself). Returns per-check
 * outcomes so the two stacks can be compared side by side.
 */
async function probeStack(door: string): Promise<{ reachable: boolean; ms?: number; registered: boolean; roundTrip: boolean; roundTripMs?: number; error?: string }> {
  const sub = await subscribers(door)
  if (!sub.ok) return { reachable: false, registered: false, roundTrip: false, error: sub.error }

  const probeAgent = `split-probe-${Date.now()}`
  const reg = await registerAgent(door, { id: probeAgent, name: "Split Probe", color: "#8b5cf6" })

  const rt = await sendViaAObserveViaB({
    sendDoor: door,
    observeDoor: door,
    channel: probeAgent,
    from: "split-stack",
    text: `two-stack probe ${Date.now()}`,
    timeoutMs: 12_000,
  })

  return {
    reachable: true,
    ms: sub.ms,
    registered: reg.ok,
    roundTrip: rt.ok,
    roundTripMs: rt.observedMs,
    error: rt.ok ? undefined : rt.error,
  }
}

export async function runTwoStack(opts: TwoStackOptions): Promise<TwoStackResult> {
  const rows: Row[] = []
  const nameA = `stack-a-${process.pid}`
  const nameB = `stack-b-${process.pid}`

  // Boot both sandboxes. If EITHER boot fails, tear down whatever came up and
  // surface the failure — never leave a half-booted split test running.
  let mA: Awaited<ReturnType<typeof sandboxUp>> | null = null
  let mB: Awaited<ReturnType<typeof sandboxUp>> | null = null
  try {
    mA = await sandboxUp({ name: nameA, branchDir: opts.aDir, withEngine: opts.withEngine })
    mB = await sandboxUp({ name: nameB, branchDir: opts.bDir, withEngine: opts.withEngine })

    const doorA = mA.env.PARLAY_SERVER
    const doorB = mB.env.PARLAY_SERVER

    rows.push({ check: "sandbox booted (isolated)", a: `${doorA}`, b: `${doorB}`, verdict: "PASS" })

    const pa = await probeStack(doorA)
    const pb = await probeStack(doorB)

    rows.push({
      check: "server reachable",
      a: pa.reachable ? `ok ${fmtMs(pa.ms)}` : `FAIL ${pa.error}`,
      b: pb.reachable ? `ok ${fmtMs(pb.ms)}` : `FAIL ${pb.error}`,
      verdict: pa.reachable && pb.reachable ? "PASS" : "FAIL",
    })
    rows.push({
      check: "agent register",
      a: pa.registered ? "ok" : "FAIL",
      b: pb.registered ? "ok" : "FAIL",
      verdict: pa.registered && pb.registered ? "PASS" : "FAIL",
    })
    rows.push({
      check: "send→observe round-trip",
      a: pa.roundTrip ? `ok ${fmtMs(pa.roundTripMs)}` : `FAIL ${pa.error}`,
      b: pb.roundTrip ? `ok ${fmtMs(pb.roundTripMs)}` : `FAIL ${pb.error}`,
      verdict: pa.roundTrip && pb.roundTrip ? "PASS" : "FAIL",
    })
  } finally {
    // Always tear down both sandboxes, even on failure.
    if (mA) sandboxDown(nameA)
    if (mB) sandboxDown(nameB)
  }

  return { rows, verdict: overallVerdict(rows) }
}

export function printTwoStackReport(res: TwoStackResult, aDir: string, bDir: string): void {
  console.log(`\nTwo-stack split test\n  A = ${aDir}\n  B = ${bDir}\n`)
  console.log(renderTable(res.rows, "A (baseline)", "B (feature)"))
  console.log(`\nOVERALL: ${res.verdict === "PASS" ? "✅ PASS" : "❌ FAIL"}\n`)
}
