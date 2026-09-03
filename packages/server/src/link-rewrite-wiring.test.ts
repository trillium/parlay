import { describe, test, expect, beforeEach, afterEach } from "bun:test"
import { handleEventsRequest } from "./router-events"
import { handlePollRequest } from "./router-poll"
import { history, historyIndex, pushToHistory } from "./storage"
import { sseClients, broadcastToClients } from "./sse"
import { __resetLinkRewriteCacheForTest } from "./link-rewrite"
import type { ChatMessage } from "./types"

// Confirms the choke points this task wired (GET /api/chat/history, the SSE
// history/message events, and GET /api/chat/poll's immediate-delivery path)
// actually apply link-rewrite.ts's serve-time rewrite — the helper existed
// but had no caller until this task connected it.

function addMsg(text: string, extra: Partial<ChatMessage> = {}): ChatMessage {
  const msg: ChatMessage = { id: `m-${history.length}`, role: "user", ts: new Date().toISOString(), text, ...extra }
  pushToHistory(msg)
  return msg
}

beforeEach(() => {
  history.length = 0
  historyIndex.clear()
})

afterEach(() => {
  delete process.env.PARLAY_PUBLIC_HOST
  __resetLinkRewriteCacheForTest()
})

describe("GET /api/chat/history", () => {
  test("unconfigured: byte-identical, no rewrite", async () => {
    addMsg("see http://localhost:4242/panel")
    const res = handleEventsRequest(new Request("http://x/api/chat/history"), "/api/chat/history")!
    const body = await res.json() as ChatMessage[]
    expect(body[0].text).toBe("see http://localhost:4242/panel")
  })

  test("configured: localhost link host is rewritten, port/path preserved", async () => {
    process.env.PARLAY_PUBLIC_HOST = "macbook"
    addMsg("see http://localhost:4242/panel?x=1")
    const res = handleEventsRequest(new Request("http://x/api/chat/history"), "/api/chat/history")!
    const body = await res.json() as ChatMessage[]
    expect(body[0].text).toBe("see http://macbook:4242/panel?x=1")
    // the stored message itself is never mutated
    expect(history[0].text).toBe("see http://localhost:4242/panel?x=1")
  })

  test("configured: non-matching hosts are left untouched", async () => {
    process.env.PARLAY_PUBLIC_HOST = "macbook"
    addMsg("see https://example.com:4242/panel and http://192.168.1.5:9000/x")
    const res = handleEventsRequest(new Request("http://x/api/chat/history"), "/api/chat/history")!
    const body = await res.json() as ChatMessage[]
    expect(body[0].text).toBe("see https://example.com:4242/panel and http://192.168.1.5:9000/x")
  })
})

describe("GET /api/chat/poll immediate delivery", () => {
  test("configured: pending message text is rewritten on delivery", async () => {
    process.env.PARLAY_PUBLIC_HOST = "macbook"
    addMsg("http://127.0.0.1:9999/x", { role: "user", channel: "c-wiring" })
    const res = handlePollRequest(new Request("http://x/api/chat/poll?after=&channel=c-wiring"), "/api/chat/poll")!
    const body = await res.json() as ChatMessage
    expect(body.text).toBe("http://macbook:9999/x")
  })
})

describe("broadcastToClients(\"message\", ...) rewrite", () => {
  test("configured: SSE-delivered message text is rewritten", () => {
    process.env.PARLAY_PUBLIC_HOST = "macbook"
    let received = ""
    const controller = { enqueue: (chunk: Uint8Array) => { received = new TextDecoder().decode(chunk) } }
    sseClients.set("c1", { id: "c1", controller: controller as any, connectedAt: new Date().toISOString() })
    broadcastToClients("message", { id: "m1", role: "user", ts: new Date().toISOString(), text: "http://localhost:4242/y" })
    sseClients.delete("c1")
    expect(received).toContain("http://macbook:4242/y")
    expect(received).not.toContain("http://localhost:4242/y")
  })
})
