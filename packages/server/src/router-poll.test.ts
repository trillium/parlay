import { describe, test, expect, beforeEach } from "bun:test"
import { handlePollRequest } from "./router-poll"
import { tombstone, clearTombstone, unregisterAgent } from "./prune"
import { agents, lastPollByChannel } from "./sse"

// robots-ycfa. The registry auto-registers any channel that polls, which is the
// right default for a first-time agent and was catastrophic for a leaked one: a
// pruned test fixture whose listener process was still running re-created its
// own registry row on its next poll, seconds later. The sweep removed 82
// channels an hour, every hour, and 82 channels came straight back.
//
// These tests pin the contract that closes it: a deliberately removed channel is
// refused, told 410 Gone so its poller can stop, and NOT re-registered.

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

  test("an untombstoned channel still auto-registers and records presence", () => {
    const res = poll("live-agent")
    expect(res).not.toBeNull()
    expect(res!.status).not.toBe(410)
    expect(agents.has("live-agent")).toBe(true)
    expect(lastPollByChannel.has("live-agent")).toBe(true)
  })

  test("clearing the tombstone restores normal polling", () => {
    tombstone("live-agent")
    expect(poll("live-agent")!.status).toBe(410)
    clearTombstone("live-agent")
    expect(poll("live-agent")!.status).not.toBe(410)
    expect(agents.has("live-agent")).toBe(true)
  })
})
