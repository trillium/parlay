import { describe, expect, test } from "bun:test"
import { CORS, guardChatRequest, guardedCorsHeaders, preflightResponse, withGuardedCors } from "./index"
import { EVIL, OTHER_HOST, SAME_ORIGIN, TUNNEL_ORIGIN, req } from "./test-helpers"

// What the guard REFUSES. ./allow.test.ts is the other half — every refusal
// below has a counterpart there proving the legitimate caller still works.

describe("cross-origin POST is rejected", () => {
  test("403 with no Access-Control-Allow-Origin, even with a JSON body", async () => {
    const resp = guardChatRequest(req("POST", "/api/chat/send", { origin: EVIL }), "/api/chat/send")
    expect(resp).not.toBeNull()
    expect(resp!.status).toBe(403)
    expect(resp!.headers.get("access-control-allow-origin")).toBeNull()
    expect(await resp!.json()).toEqual({ error: "cross-origin request rejected" })
  })

  test("the fleet-wide broadcast route is rejected the same way", () => {
    const resp = guardChatRequest(req("POST", "/api/chat/alert", { origin: EVIL }), "/api/chat/alert")
    expect(resp?.status).toBe(403)
  })

  test("a cross-origin DELETE of an agent is rejected", () => {
    const p = "/api/chat/agents/parlay-cors-p1"
    expect(guardChatRequest(req("DELETE", p, { origin: EVIL, contentType: null }), p)?.status).toBe(403)
  })

  test("a non-local origin whose Host does NOT match is refused, with no ACAO", () => {
    // The control for ./allow.test.ts's tunnel case: the same origin that the
    // same-host comparison accepts on its own Host is refused on any other, so
    // that comparison cannot be mistaken for a blanket allowance.
    const r = req("PUT", "/api/chat/draft", { origin: TUNNEL_ORIGIN, host: OTHER_HOST })
    expect(guardChatRequest(r, "/api/chat/draft")?.status).toBe(403)
    expect(guardedCorsHeaders(r)["Access-Control-Allow-Origin"]).toBeUndefined()
  })
})

// ── task-6ai1 / D9 ──────────────────────────────────────────────────────────
// The verification report drove a real attack chain through these routes with
// a cross-origin CORS *simple request* (Content-Type: text/plain, no
// preflight). Every test in this block asserts the exact shape that returned
// 200 before the fix.
describe("D9: the proven cross-origin chain is refused", () => {
  const simple = (method: string, p: string) =>
    guardChatRequest(req(method, p, { origin: EVIL, contentType: "text/plain" }), p)

  test("POST /api/chat/eval — drove an input_action into a connected panel", () => {
    expect(simple("POST", "/api/chat/eval")?.status).toBe(403)
  })

  test("PUT /api/chat/draft — set the captain's outgoing text", () => {
    expect(simple("PUT", "/api/chat/draft")?.status).toBe(403)
  })

  test("GET /api/chat/draft — reads back what the captain is typing", () => {
    const r = req("GET", "/api/chat/draft", { origin: EVIL, contentType: null })
    expect(guardChatRequest(r, "/api/chat/draft")?.status).toBe(403)
  })

  test("POST /api/chat/upload", () => {
    expect(simple("POST", "/api/chat/upload")?.status).toBe(403)
  })

  test("GET /api/chat/subscribers — the device-id and agent-id disclosure", async () => {
    const r = req("GET", "/api/chat/subscribers", { origin: EVIL, contentType: null })
    const resp = guardChatRequest(r, "/api/chat/subscribers")
    expect(resp?.status).toBe(403)
    expect(await resp!.json()).toEqual({ error: "cross-origin request rejected" })
  })

  test("/subscribers no longer answers a foreign origin with a wildcard ACAO", () => {
    // Before: the handler spread the wildcard CORS and the router let it
    // through untouched, so any page could read the body.
    const evil = req("GET", "/api/chat/subscribers", { origin: EVIL, contentType: null })
    expect(guardedCorsHeaders(evil)["Access-Control-Allow-Origin"]).toBeUndefined()
    const handler = new Response("{}", { headers: { ...CORS } })
    expect(withGuardedCors(evil, handler).headers.get("access-control-allow-origin")).toBeNull()
  })

  test("cross-origin preflight on the D9 routes is refused, not 204'd", () => {
    for (const p of ["/api/chat/eval", "/api/chat/draft", "/api/chat/upload", "/api/chat/subscribers"]) {
      const resp = preflightResponse(req("OPTIONS", p, { origin: EVIL, contentType: null }), p)
      expect(resp.status).toBe(403)
      expect(resp.headers.get("access-control-allow-origin")).toBeNull()
    }
  })
})

describe("D9 neighbours: mutating routes found while auditing the route table", () => {
  for (const [method, p] of [
    ["PUT",  "/api/chat/parlay/settings"],
    ["POST", "/api/chat/tts"],
    ["POST", "/api/chat/tts-correction"],
    ["POST", "/api/chat/tts-report"],
    ["POST", "/api/chat/tts-event"],
    ["POST", "/api/chat/tts/validate-splits"],
    ["POST", "/api/chat/plugin/cursorless/rpc"],
    ["POST", "/api/chat/plugin/cursorless/response"],
    ["POST", "/api/debug/input-timing"],
    ["GET",  "/api/debug/input-timing"],
  ] as const) {
    test(`${method} ${p} refuses a cross-origin simple request`, () => {
      const r = req(method, p, { origin: EVIL, contentType: method === "GET" ? null : "text/plain" })
      expect(guardChatRequest(r, p)?.status).toBe(403)
    })
  }
})

describe("non-JSON Content-Type is rejected with 415", () => {
  // A same-origin simple request is still a request that skipped preflight,
  // so the gate applies to it too.
  for (const ct of ["text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data"]) {
    test(`same-origin POST with ${ct} → 415`, async () => {
      const r = req("POST", "/api/chat/send", { origin: SAME_ORIGIN, contentType: ct })
      const resp = guardChatRequest(r, "/api/chat/send")
      expect(resp?.status).toBe(415)
      expect(await resp!.json()).toEqual({ error: "Content-Type: application/json required" })
    })
  }

  test("a missing Content-Type (empty-type Blob body) → 415", () => {
    const r = req("POST", "/api/chat/send", { origin: SAME_ORIGIN, contentType: null })
    expect(guardChatRequest(r, "/api/chat/send")?.status).toBe(415)
  })

  test("but a CROSS-origin multipart upload is refused on the origin check, not the gate", () => {
    const ct = "multipart/form-data; boundary=----WebKitFormBoundaryABC"
    expect(guardChatRequest(req("POST", "/api/chat/upload", { origin: EVIL, contentType: ct }), "/api/chat/upload")?.status).toBe(403)
  })
})
