// two-door — SAME backing store, two front doors. The pulse-next case: door A is
// the direct server (http://localhost:31337) and door B is the proxy
// (http://localhost:31339) that forwards to the SAME store. If the two doors are
// truly the same store, a message sent through one is observable through the
// other, presence agrees, and latencies are comparable.
//
// Scripted suite (all against a throwaway split-probe-<ts> channel):
//   1. reachability of both doors (subscribers vitals)
//   2. register the probe agent via A, confirm visible in B's registry
//   3. send via A → observe via B  (cross-door delivery, A→B)
//   4. send via B → observe via A  (cross-door delivery, B→A)
//   5. poll-latency comparison (timeout round-trip through each door)
//   6. doctor-equivalent: subscribers snapshot parity (same registered count)
//   7. soak (optional): hold N long-polls through each door for --soak seconds
//      while injecting M messages per door; verify delivery counts + timing match.

import { registerAgent, subscribers, agentsList, sendViaAObserveViaB } from "./probe"
import { renderTable, overallVerdict, type Row } from "./table"

export interface TwoDoorOptions {
  doorA: string
  doorB: string
  soakSeconds: number
  labelA?: string
  labelB?: string
}

export interface TwoDoorResult {
  rows: Row[]
  verdict: "PASS" | "FAIL"
  probeAgent: string
}

function fmtMs(ms: number | undefined): string {
  return ms === undefined ? "—" : `${ms.toFixed(0)}ms`
}

export async function runTwoDoor(opts: TwoDoorOptions): Promise<TwoDoorResult> {
  const A = opts.doorA.replace(/\/+$/, "")
  const B = opts.doorB.replace(/\/+$/, "")
  const probeAgent = `split-probe-${Date.now()}`
  const rows: Row[] = []

  // ── 1. reachability ──────────────────────────────────────────────────────────
  const subA = await subscribers(A)
  const subB = await subscribers(B)
  rows.push({
    check: "door reachable (/subscribers)",
    a: subA.ok ? `ok ${fmtMs(subA.ms)}` : `FAIL ${subA.error}`,
    b: subB.ok ? `ok ${fmtMs(subB.ms)}` : `FAIL ${subB.error}`,
    verdict: subA.ok && subB.ok ? "PASS" : "FAIL",
  })
  if (!subA.ok || !subB.ok) {
    // Both doors must be reachable for the rest to mean anything. Return early
    // with the rest marked SKIP so the table is honest about what ran.
    for (const check of ["register via A → visible in B", "send A → observe B", "send B → observe A", "poll latency", "subscribers parity", "soak delivery"]) {
      rows.push({ check, a: "—", b: "—", verdict: "SKIP" })
    }
    return { rows, verdict: overallVerdict(rows), probeAgent }
  }

  // ── 2. register via A, confirm in B ──────────────────────────────────────────
  const reg = await registerAgent(A, { id: probeAgent, name: "Split Probe", color: "#8b5cf6" })
  const listB = await agentsList(B)
  const visibleInB = listB.ok && (listB.data ?? []).some((x) => x.id === probeAgent)
  rows.push({
    check: "register via A → visible in B",
    a: reg.ok ? `registered ${fmtMs(reg.ms)}` : `FAIL ${reg.error}`,
    b: visibleInB ? "present in registry" : "NOT present",
    verdict: reg.ok && visibleInB ? "PASS" : "FAIL",
  })

  // ── 3. send via A → observe via B ────────────────────────────────────────────
  const ab = await sendViaAObserveViaB({
    sendDoor: A,
    observeDoor: B,
    channel: probeAgent,
    from: "split-a",
    text: `two-door A→B ${Date.now()}`,
    timeoutMs: 12_000,
  })
  rows.push({
    check: "send A → observe B",
    a: ab.sentId ? `sent ${fmtMs(ab.sendMs)}` : `FAIL ${ab.error}`,
    b: ab.ok ? `observed ${fmtMs(ab.observedMs)}` : `FAIL ${ab.error}`,
    verdict: ab.ok ? "PASS" : "FAIL",
  })

  // ── 4. send via B → observe via A ────────────────────────────────────────────
  const ba = await sendViaAObserveViaB({
    sendDoor: B,
    observeDoor: A,
    channel: probeAgent,
    from: "split-b",
    text: `two-door B→A ${Date.now()}`,
    timeoutMs: 12_000,
  })
  rows.push({
    check: "send B → observe A",
    a: ba.ok ? `observed ${fmtMs(ba.observedMs)}` : `FAIL ${ba.error}`,
    b: ba.sentId ? `sent ${fmtMs(ba.sendMs)}` : `FAIL ${ba.error}`,
    verdict: ba.ok ? "PASS" : "FAIL",
  })

  // ── 5. poll latency comparison (empty-channel timeout round-trip is slow by
  //     design — instead measure a fast pending-delivery round-trip per door) ──
  const latA = await measurePollLatency(A, `${probeAgent}`)
  const latB = await measurePollLatency(B, `${probeAgent}`)
  const bothLat = latA.ok && latB.ok
  const skew = bothLat ? Math.abs((latA.ms ?? 0) - (latB.ms ?? 0)) : Infinity
  rows.push({
    check: "poll latency (deliver round-trip)",
    a: latA.ok ? fmtMs(latA.ms) : `FAIL ${latA.error}`,
    b: latB.ok ? fmtMs(latB.ms) : `FAIL ${latB.error}`,
    // Doors share a store; a large skew (>2s) suggests one door adds real overhead.
    verdict: bothLat ? (skew < 2000 ? "PASS" : "WARN") : "FAIL",
  })

  // ── 6. subscribers parity (doctor-equivalent) ────────────────────────────────
  const sA = await subscribers(A)
  const sB = await subscribers(B)
  const cntA = sA.data?.registered?.count ?? -1
  const cntB = sB.data?.registered?.count ?? -1
  rows.push({
    check: "subscribers parity (registered count)",
    a: `${cntA} agent(s)`,
    b: `${cntB} agent(s)`,
    verdict: cntA >= 0 && cntA === cntB ? "PASS" : "WARN",
  })

  // ── 7. soak (optional) ───────────────────────────────────────────────────────
  if (opts.soakSeconds > 0) {
    const soak = await runSoak(A, B, probeAgent, opts.soakSeconds)
    rows.push({
      check: `soak delivery (${opts.soakSeconds}s, ${soak.injected} msgs)`,
      a: `A→B recv ${soak.deliveredAB}/${soak.injectedAB} (${fmtMs(soak.avgMsAB)})`,
      b: `B→A recv ${soak.deliveredBA}/${soak.injectedBA} (${fmtMs(soak.avgMsBA)})`,
      verdict: soak.deliveredAB === soak.injectedAB && soak.deliveredBA === soak.injectedBA ? "PASS" : "FAIL",
    })
  } else {
    rows.push({ check: "soak delivery", a: "—", b: "—", verdict: "SKIP" })
  }

  return { rows, verdict: overallVerdict(rows), probeAgent }
}

/**
 * Measure a delivery round-trip through one door: send a message on `channel`,
 * then poll the SAME door until it comes back, timing the poll. This is a fast,
 * store-hitting latency probe (vs. the 30s empty-channel timeout).
 */
async function measurePollLatency(door: string, channel: string): Promise<{ ok: boolean; ms?: number; error?: string }> {
  const r = await sendViaAObserveViaB({
    sendDoor: door,
    observeDoor: door,
    channel,
    from: "split-lat",
    text: `latency ${Date.now()}`,
    timeoutMs: 10_000,
  })
  return r.ok ? { ok: true, ms: r.observedMs } : { ok: false, error: r.error }
}

interface SoakResult {
  injected: number
  injectedAB: number
  injectedBA: number
  deliveredAB: number
  deliveredBA: number
  avgMsAB?: number
  avgMsBA?: number
}

/**
 * Hold long-poll connections open through each door for `seconds`, injecting a
 * message every ~2s in each direction (send via A observe via B, and send via B
 * observe via A), and verify every injected message is delivered cross-door with
 * comparable timing. Each injection is an independent send→observe cycle, so a
 * dropped message shows up as delivered < injected.
 */
async function runSoak(A: string, B: string, channel: string, seconds: number): Promise<SoakResult> {
  const deadline = Date.now() + seconds * 1000
  const intervalMs = 2000

  const abLatencies: number[] = []
  const baLatencies: number[] = []
  let injectedAB = 0
  let injectedBA = 0

  // Run A→B and B→A injection loops concurrently for the full soak window.
  const loop = async (dir: "AB" | "BA") => {
    const send = dir === "AB" ? A : B
    const observe = dir === "AB" ? B : A
    while (Date.now() < deadline) {
      const r = await sendViaAObserveViaB({
        sendDoor: send,
        observeDoor: observe,
        channel,
        from: `soak-${dir.toLowerCase()}`,
        text: `soak ${dir} ${Date.now()}`,
        timeoutMs: 15_000,
      })
      if (dir === "AB") {
        injectedAB++
        if (r.ok && r.observedMs !== undefined) abLatencies.push(r.observedMs)
      } else {
        injectedBA++
        if (r.ok && r.observedMs !== undefined) baLatencies.push(r.observedMs)
      }
      // Pace the next injection, but never sleep past the soak deadline.
      if (Date.now() + intervalMs < deadline) await new Promise((res) => setTimeout(res, intervalMs))
    }
  }

  await Promise.all([loop("AB"), loop("BA")])

  const avg = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : undefined)
  return {
    injected: injectedAB + injectedBA,
    injectedAB,
    injectedBA,
    deliveredAB: abLatencies.length,
    deliveredBA: baLatencies.length,
    avgMsAB: avg(abLatencies),
    avgMsBA: avg(baLatencies),
  }
}

/** Format a full two-door report to stdout. */
export function printTwoDoorReport(res: TwoDoorResult, labelA: string, labelB: string): void {
  console.log(`\nTwo-door probe — agent ${res.probeAgent}\n`)
  console.log(renderTable(res.rows, labelA, labelB))
  console.log(`\nOVERALL: ${res.verdict === "PASS" ? "✅ PASS" : "❌ FAIL"}\n`)
}
