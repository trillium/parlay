import { describe, test, expect, beforeEach } from "bun:test"
import { handlePollRequest } from "./router-poll"
import { handleMessagesRequest } from "./router-messages"
import { tombstone, clearTombstone, unregisterAgent } from "./prune"
import { agents, lastPollByChannel } from "./sse"
import { history } from "./storage"

// robots-ycfa / task-1t0m. The registry used to auto-register any channel that
// polled — convenient for a first-time agent, but catastrophic for a leaked
// one: a pruned test fixture whose listener process was still running
// re-created its own registry row on its next poll, seconds later. The sweep
// removed 82 channels an hour, every hour, and 82 channels came straight back.
//
// task-1t0m removed poll's implicit registration entirely (GET /api/chat/poll
// must be genuinely read-only, not read-only by accident of the origin guard):
// registration now only happens via the explicit, already-guarded POST
// /api/chat/register-agent. These tests pin that a poll — tombstoned or not —
// never creates or resurrects a registry row, while still recording presence
// (lastPollByChannel) for whatever id it was asked to poll.

const poll = (channel: string) =>
  handlePollRequest(new Request(`http://x/api/chat/poll?after=&channel=${channel}`), "/api/chat/poll")

beforeEach(() => {
  agents.clear()
  lastPollByChannel.clear()
  for (const id of ["ghost-z1", "live-agent"]) clearTombstone(id)
})

describe("handlePollRequest — tombstoned channels", () => {
  test("a tombstoned channel gets 410 Gone", async () => {
    tombstone("ghost-z1")
    const res = poll("ghost-z1")!
    expect(res.status).toBe(410)
    const body = await res.json() as { gone?: boolean; error?: string }
    expect(body.gone).toBe(true)
    expect(body.error).toContain("stop polling")
  })

  test("a tombstoned channel is NOT re-added to the registry by its own poll", () => {
    tombstone("ghost-z1")
    poll("ghost-z1")
    expect(agents.has("ghost-z1")).toBe(false)
  })

  test("a refused poll does not count as presence", () => {
    tombstone("ghost-z1")
    poll("ghost-z1")
    expect(lastPollByChannel.has("ghost-z1")).toBe(false)
  })

  test("unregister → poll is terminal end to end: removed, refused, still removed", async () => {
    agents.set("ghost-z1", { id: "ghost-z1", name: "ghost", color: "#000" })
    expect(unregisterAgent("ghost-z1").ok).toBe(true)
    const res = poll("ghost-z1")!
    expect(res.status).toBe(410)
    expect(agents.has("ghost-z1")).toBe(false)
  })

  test("an untombstoned channel records presence but is NOT registered by polling alone", () => {
    const res = poll("live-agent")
    expect(res).not.toBeNull()
    expect(res!.status).not.toBe(410)
    expect(agents.has("live-agent")).toBe(false)
    expect(lastPollByChannel.has("live-agent")).toBe(true)
  })

  test("clearing the tombstone restores normal polling, still without auto-registering", () => {
    tombstone("live-agent")
    expect(poll("live-agent")!.status).toBe(410)
    clearTombstone("live-agent")
    expect(poll("live-agent")!.status).not.toBe(410)
    expect(agents.has("live-agent")).toBe(false)
  })

  test("polling the same channel repeatedly never writes the agent registry", () => {
    poll("live-agent")
    poll("live-agent")
    poll("live-agent")
    expect(agents.has("live-agent")).toBe(false)
  })

  // The moved mutation: registration now happens ONLY through the explicit,
  // already-guarded POST /api/chat/register-agent (every real poll consumer —
  // parlay listen, parlay monitor, the relay — calls this before polling).
  test("the explicit register-agent path still registers, independent of poll", async () => {
    const req = new Request("http://x/api/chat/register-agent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: "live-agent", name: "Live Agent", color: "#123456" }),
    })
    const res = await handleMessagesRequest(req, "/api/chat/register-agent")!
    const body = await res.json() as { ok?: boolean }
    expect(body.ok).toBe(true)
    expect(agents.has("live-agent")).toBe(true)

    // and polling that now-registered channel doesn't disturb it
    poll("live-agent")
    expect(agents.has("live-agent")).toBe(true)
  })
})

// task-35ww. `parlay shutdown`'s server half: unregisterAgent() now reports
// how many user messages addressed to the channel were never polled/received
// (reported, not flushed — see prune/sweep.ts's UnregisterResult doc comment),
// and immediately resolves any long-poll already parked on that channel
// instead of leaving it to find out on its own next timeout.
describe("unregisterAgent — graceful shutdown (task-35ww)", () => {
  beforeEach(() => {
    history.length = 0
  })

  test("undelivered counts only unreceived user messages on that channel", () => {
    agents.set("ghost-z1", { id: "ghost-z1", name: "ghost", color: "#000" })
    history.push(
      { id: "1", role: "user", ts: "t", text: "hi", channel: "ghost-z1" }, // undelivered
      { id: "2", role: "user", ts: "t", text: "hi", channel: "ghost-z1", received: true }, // delivered
      { id: "3", role: "agent", ts: "t", text: "reply", channel: "ghost-z1" }, // not a user msg
      { id: "4", role: "user", ts: "t", text: "hi", channel: "other-agent" }, // different channel
    )
    const res = unregisterAgent("ghost-z1")
    expect(res.ok).toBe(true)
    expect(res.undelivered).toBe(1)
  })

  test("undelivered is 0 when every queued message was already received", () => {
    agents.set("ghost-z1", { id: "ghost-z1", name: "ghost", color: "#000" })
    history.push({ id: "1", role: "user", ts: "t", text: "hi", channel: "ghost-z1", received: true })
    const res = unregisterAgent("ghost-z1")
    expect(res.ok).toBe(true)
    expect(res.undelivered).toBe(0)
  })

  test("a long-poll parked on the channel resolves immediately with gone:true on unregister", async () => {
    agents.set("ghost-z1", { id: "ghost-z1", name: "ghost", color: "#000" })
    // handlePollRequest is synchronous and parks its waiter (pollWaiters.push)
    // during the ReadableStream's start() callback, which runs synchronously
    // on construction — the waiter is already registered by the time poll()
    // returns, with nothing to deliver yet on this fresh channel.
    const pending = poll("ghost-z1")!

    const res = unregisterAgent("ghost-z1")
    expect(res.ok).toBe(true)

    const body = await pending.json() as { gone?: boolean }
    expect(body.gone).toBe(true)
  })
})
