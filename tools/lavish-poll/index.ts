#!/usr/bin/env bun
// Bridges `lavish poll <file>` to Parlay chat at 4242.
// True concurrent racing: Parlay (chat) vs 4387 (layout_warnings, session-end, dom_snapshot).
// Whichever has data first wins. When 4387 wins, Parlay is aborted immediately; when Parlay wins,
// the 4387 request is held open through a short grace window so it can still contribute a
// dom_snapshot. Emits lavish-axi-compatible JSON, then exits.

import { parsePollArgs, UsageError, USAGE } from "./args"
import { readCursor, writeCursor } from "./cursor"
import { drop, nextStep, type NativeResult, type ParlayMsg } from "./protocol"
import { readBudget } from "./guards"
import { createPacer, installOrphanWatchdog } from "./spin"

const LAVISH_NATIVE = process.env.LAVISH_URL ?? "http://127.0.0.1:4387"
const [agentId, parlay, ...pollArgs] = process.argv.slice(2)

function die(msg: string): never {
  process.stderr.write(`lavish-poll: ${msg}\n${USAGE}\n`)
  process.exit(1)
}

if (!agentId || !parlay) die("agentId and parlayUrl are required")

let parsed
try {
  parsed = parsePollArgs(pollArgs)
} catch (e) {
  if (e instanceof UsageError) die(e.message)
  throw e
}
const { file, agentReply, timeoutMs } = parsed

function emit(result: object): never {
  process.stdout.write(JSON.stringify(result) + "\n")
  process.exit(0)
}

if (agentReply) {
  try {
    const res = await fetch(`${parlay}/api/chat/reply`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: agentReply, agent: agentId, name: agentId, color: "#f4c95d" }),
    })
    // fetch only rejects on a transport failure; a 4xx/5xx RESOLVES. Without
    // this check the bridge cannot tell "the captain received the reply" from
    // "the server refused it", and reports both as success — the same shape as
    // the other bugs this file fixes, one layer up. The reply channel is the
    // only way an agent's answer reaches a human, so a silently dropped one is
    // indistinguishable from an agent that never answered.
    if (!res.ok) {
      process.stderr.write(`lavish-poll: reply post rejected: HTTP ${res.status} ${res.statusText}\n`)
    }
  } catch (e) {
    process.stderr.write(`lavish-poll: reply post failed: ${e}\n`)
  }
}

// timeoutMs is either undefined or a validated positive finite number by now,
// so this no longer depends on the truthiness of a possibly-NaN value.
const deadline = timeoutMs === undefined ? Infinity : Date.now() + timeoutMs
let lastParlayId = readCursor(agentId, file)

// Hot-spin and orphan guards (robots-zahn) — guards.ts and spin.ts carry the
// full why. In short: an unreachable upstream rejects the fetch in microseconds
// instead of long-polling, so an unbounded retry is a busy loop, not a poll.
const budget = readBudget(process.env, m => process.stderr.write(`lavish-poll: ${m}\n`))
installOrphanWatchdog(budget)
const pacer = createPacer(budget, deadline, parlay)

// How long a winning Parlay message waits for the in-flight 4387 request to
// contribute a dom_snapshot before giving up on it.
const NATIVE_GRACE_MS = 200

while (Date.now() < deadline) {
  const iterStarted = Date.now()

  // One controller per request. These used to share a single `ac`, and the
  // `ac.abort()` that fired the moment the race resolved killed BOTH. So on the
  // chat path the native promise had already settled to null through its own
  // .catch() before the grace window below ever read it: dom_snapshot was
  // unconditionally "", layout_warnings unconditionally [], and the grace window
  // was dead code. The comment there even said "give aborted nativeP time to
  // deliver dom_snapshot" — which an aborted fetch cannot do.
  const parlayAC = new AbortController()
  const nativeAC = new AbortController()

  // Parlay: long-polls ~30s, returns {timeout:true} on expiry. A REJECTION is a
  // different animal — a dead or misbehaving upstream, settling in microseconds
  // rather than after 30s — and mapping it onto a bare {timeout:true} made the
  // two indistinguishable downstream, which is how a connection-refused loop
  // passed for polling. `failed` is that distinction.
  const parlayP = fetch(
    `${parlay}/api/chat/poll?after=${encodeURIComponent(lastParlayId)}&channel=${encodeURIComponent(agentId)}`,
    { signal: parlayAC.signal },
    // A non-OK response is a failure even when it carries a JSON body. fetch
    // only rejects on a transport error, so a 502 from a dying or proxying
    // server parsed cleanly and reached the loop as a message-shaped object
    // with no id — which fell through to the "progress" path and RESET the
    // failure streak. That is exactly the spin this guard exists to end, so
    // status has to be classified before the body is decoded. The thrown-error
    // path below stays separate: both are failures, by different routes.
  ).then(r => (r.ok
    ? (r.json() as Promise<ParlayMsg>)
    : ({ timeout: true, failed: true } as ParlayMsg))
  ).catch(() => ({ timeout: true, failed: true } as ParlayMsg))

  // 4387: streaming heartbeat mode (no timeoutMs) — holds connection open until data arrives
  const nativeP = fetch(
    `${LAVISH_NATIVE}/api/poll?file=${encodeURIComponent(file)}`,
    { signal: nativeAC.signal },
  ).then(r => r.json() as Promise<NativeResult>).catch(() => null)

  // The deadline has to be part of the race, not just the loop condition. Both
  // legs below can outlive it: Parlay long-polls for ~30s, and an unreachable
  // 4387 resolves to a promise that never settles at all (see drop()). Checking
  // `Date.now() < deadline` only between iterations therefore overshoots
  // --timeout-ms by up to a full long-poll, and blocks forever against a server
  // that never answers — a validated timeout that still bounds nothing.
  const remaining = deadline - Date.now()
  const winner = await Promise.race([
    parlayP.then(v => ({ src: "parlay" as const, v })),
    // Drop from race if 4387 is unreachable (null) — Parlay continues uninterrupted
    nativeP.then(v => v !== null ? { src: "native" as const, v } : drop<{ src: "native"; v: NativeResult }>()),
    ...(Number.isFinite(remaining) ? [Bun.sleep(remaining).then(() => ({ src: "deadline" as const }))] : []),
  ])

  if (winner.src === "deadline") {
    parlayAC.abort()
    nativeAC.abort()
    break
  }

  if (winner.src === "native") {
    parlayAC.abort()
    const n = winner.v

    if (n.status === "ended" || n.session_ended) {
      emit({
        session: { file, status: "ended", ...(n.ended_by ? { ended_by: n.ended_by } : {}) },
        dom_snapshot: n.dom_snapshot || "",
        prompts: [],
        next_step: nextStep(file, true),
      })
    }

    const warnings = n.layout_warnings ?? []
    const prompts = n.prompts ?? []
    if (warnings.length > 0 || prompts.length > 0) {
      emit({
        session: { file, status: "feedback" },
        dom_snapshot: n.dom_snapshot || "",
        prompts,
        ...(warnings.length > 0 ? { layout_warnings: warnings } : {}),
        next_step: nextStep(file, false, prompts),
      })
    }
    // Native returned "waiting" (shouldn't happen in streaming mode). Paced
    // anyway: a 4387 that answers instantly instead of holding the connection
    // open is the same unbounded restart loop by a different route.
    await pacer.settle(iterStarted)
    continue
  }

  // Parlay won. The native request is deliberately NOT aborted here — the grace
  // window below is the only thing that can populate dom_snapshot, so it has to
  // still be in flight when we get there.
  const msg = winner.v
  if (msg.timeout) {
    // 30s expired with no chat — restart both. Unless it did not take 30s:
    // `failed` says the request never reached a server, and an unbounded retry
    // of that is the 21h/98%-CPU process this guard exists for.
    nativeAC.abort()
    await pacer.settle(iterStarted, msg.failed ? "failed" : "progress")
    continue
  }

  // Advance the cursor as soon as a message has been CONSUMED, before filtering
  // on role or text. Filtering first meant an agent reply — which carries an id
  // but role "agent" — left the cursor unmoved, so the next iteration reissued
  // the identical after= request, got the same message back, and span at full
  // speed until the deadline. Same reason robots-nm8 persists it at all: agent
  // replies never clear the cursor, so nothing else advances past them.
  if (msg.id) {
    lastParlayId = msg.id
    writeCursor(agentId, file, lastParlayId) // persist BEFORE emit (which exits)
  }

  if (msg.id && msg.role === "user" && msg.text != null) {
    // Grace window: nativeAC is still live, so this can actually deliver.
    // Capped by whatever is left of --timeout-ms, because an unconditional
    // 200ms wait here overshoots the deadline the caller asked for — and this
    // whole PR exists because a validated input still has to be an obeyed one.
    // A caller passing --timeout-ms 100 must not get 300ms back.
    const graceMs = Math.max(0, Math.min(NATIVE_GRACE_MS, deadline - Date.now()))
    const n = await Promise.race([nativeP, Bun.sleep(graceMs).then(() => null)])
    nativeAC.abort()
    const warnings = n?.layout_warnings ?? []
    const chatPrompt = [{ tag: "chat", text: msg.text }]
    emit({
      session: { file, status: "feedback" },
      dom_snapshot: n?.dom_snapshot || "",
      prompts: chatPrompt,
      ...(warnings.length > 0 ? { layout_warnings: warnings } : {}),
      next_step: nextStep(file, false, chatPrompt),
    })
  }

  // Reached only when Parlay delivered something that is not a user message —
  // an agent reply, or a message with null text. emit() above never returns, so
  // this is the one path out of the Parlay branch that keeps looping, and it
  // must release the native request or each iteration would strand one.
  nativeAC.abort()
  await pacer.settle(iterStarted, "progress")
}

process.stdout.write(JSON.stringify({
  session: { file, status: "waiting" },
  next_step: `No user feedback arrived before the optional timeout. Run \`lavish poll ${file}\` without --timeout-ms to wait indefinitely — queued feedback is never lost.`,
}) + "\n")
