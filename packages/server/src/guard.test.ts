import { describe, expect, test, afterEach } from "bun:test"
import {
  CORS,
  guardChatRequest,
  guardedCorsHeaders,
  isGuardedChatPath,
  isJsonContentType,
  originAllowed,
  preflightResponse,
  withGuardedCors,
} from "./guard"

// guard.ts is deliberately dependency-free (no storage/sse imports), so these
// run with zero side effects: no ~/exchange, no port, no timers.

const HOST = "localhost:31337"
const SAME_ORIGIN = "http://localhost:31337"
const EVIL = "https://evil.example.com"

function req(
  method: string,
  pathname: string,
  opts: { origin?: string; contentType?: string | null; host?: string } = {},
): Request {
  const headers = new Headers({ host: opts.host ?? HOST })
  if (opts.origin) headers.set("origin", opts.origin)
  if (opts.contentType !== null) headers.set("content-type", opts.contentType ?? "application/json")
  return new Request(`http://${opts.host ?? HOST}${pathname}`, { method, headers })
}

afterEach(() => { delete process.env.PARLAY_ALLOWED_ORIGINS })

describe("guarded route set", () => {
  test("mutating chat routes are guarded", () => {
    for (const p of [
      "/api/chat/send", "/api/chat/reply", "/api/chat/alert", "/api/chat/system",
      "/api/chat/register-agent", "/api/chat/unregister", "/api/chat/declare-channel",
      "/api/chat/clear", "/api/chat/navigate", "/api/chat/reload", "/api/chat/device-cmd",
    ]) expect(isGuardedChatPath(p)).toBe(true)
  })

  test("DELETE /api/chat/agents/:id is guarded but GET /api/chat/agents is not", () => {
    expect(isGuardedChatPath("/api/chat/agents/some-agent")).toBe(true)
    expect(isGuardedChatPath("/api/chat/agents")).toBe(false)
  })

  test("read, SSE and upload routes stay unguarded", () => {
    for (const p of [
      "/api/chat/history", "/api/chat/events", "/api/chat/poll", "/api/chat/subscribers",
      "/api/chat/draft", "/api/chat/upload", "/api/chat/uploads/x.png",
    ]) expect(isGuardedChatPath(p)).toBe(false)
  })
})

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

  test("a sandboxed iframe / file:// origin ('null') is rejected", () => {
    expect(originAllowed(req("POST", "/api/chat/send", { origin: "null" }))).toBe(false)
  })

  test("a cross-origin DELETE of an agent is rejected", () => {
    const p = "/api/chat/agents/parlay-cors-p1"
    expect(guardChatRequest(req("DELETE", p, { origin: EVIL, contentType: null }), p)?.status).toBe(403)
  })

  test("an attacker origin that merely embeds the host name is still rejected", () => {
    const o = "https://localhost:31337.evil.example.com"
    expect(originAllowed(req("POST", "/api/chat/send", { origin: o }))).toBe(false)
  })
})

describe("non-JSON Content-Type is rejected with 415", () => {
  // These are exactly the content types a cross-origin POST can send WITHOUT a
  // preflight. Rejecting them is what closes the simple-request bypass.
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

  test("charset and casing on a real JSON type are accepted", () => {
    expect(isJsonContentType("application/json")).toBe(true)
    expect(isJsonContentType("Application/JSON; charset=utf-8")).toBe(true)
    expect(isJsonContentType("application/jsonp")).toBe(false)
    expect(isJsonContentType(null)).toBe(false)
  })
})

describe("legitimate callers still get through", () => {
  test("same-origin JSON POST from the panel is accepted", () => {
    expect(guardChatRequest(req("POST", "/api/chat/send", { origin: SAME_ORIGIN }), "/api/chat/send")).toBeNull()
  })

  test("CLI JSON POST (no Origin header at all) is accepted", () => {
    const r = new Request(`http://${HOST}/api/chat/send`, {
      method: "POST",
      headers: { host: HOST, "content-type": "application/json" },
    })
    expect(guardChatRequest(r, "/api/chat/send")).toBeNull()
    expect(originAllowed(r)).toBe(true)
  })

  test("the phone on the LAN is accepted (same host, and as a private origin)", () => {
    const lan = "http://192.168.1.42:31337"
    expect(originAllowed(req("POST", "/api/chat/send", { origin: lan, host: "192.168.1.42:31337" }))).toBe(true)
    // Pulse may reverse-proxy to this server under a rewritten Host.
    expect(originAllowed(req("POST", "/api/chat/send", { origin: lan, host: "localhost:4242" }))).toBe(true)
  })

  test("a loopback origin on another port (Pulse → standalone proxy) is accepted", () => {
    const r = req("POST", "/api/chat/send", { origin: "http://127.0.0.1:31337", host: "localhost:4242" })
    expect(originAllowed(r)).toBe(true)
  })

  test("PARLAY_ALLOWED_ORIGINS opts a specific public origin in", () => {
    const r = req("POST", "/api/chat/send", { origin: "https://tunnel.example.com" })
    expect(originAllowed(r)).toBe(false)
    process.env.PARLAY_ALLOWED_ORIGINS = "https://other.example.com, https://tunnel.example.com"
    expect(originAllowed(r)).toBe(true)
  })

  test("PARLAY_ALLOWED_ORIGINS='*' is the explicit opt-out escape hatch", () => {
    process.env.PARLAY_ALLOWED_ORIGINS = "*"
    expect(originAllowed(req("POST", "/api/chat/send", { origin: EVIL }))).toBe(true)
  })

  test("read routes are not touched by the guard", () => {
    expect(guardChatRequest(req("GET", "/api/chat/agents", { origin: EVIL, contentType: null }), "/api/chat/agents")).toBeNull()
    expect(guardChatRequest(req("GET", "/api/chat/events", { origin: EVIL, contentType: null }), "/api/chat/events")).toBeNull()
  })
})

describe("no wildcard on guarded responses", () => {
  test("withGuardedCors replaces '*' with the single reflected origin", () => {
    const handler = new Response("{}", { headers: { "Content-Type": "application/json", ...CORS } })
    expect(handler.headers.get("access-control-allow-origin")).toBe("*")
    const out = withGuardedCors(req("POST", "/api/chat/send", { origin: SAME_ORIGIN }), handler)
    expect(out.headers.get("access-control-allow-origin")).toBe(SAME_ORIGIN)
    expect(out.headers.get("vary")).toBe("Origin")
    expect(out.headers.get("content-type")).toBe("application/json")
  })

  test("a no-Origin (CLI) response carries no ACAO at all", () => {
    const handler = new Response("{}", { headers: { ...CORS } })
    const r = new Request(`http://${HOST}/api/chat/send`, { method: "POST", headers: { host: HOST } })
    expect(withGuardedCors(r, handler).headers.get("access-control-allow-origin")).toBeNull()
  })

  test("guardedCorsHeaders never emits a wildcard", () => {
    expect(guardedCorsHeaders(req("POST", "/api/chat/send", { origin: EVIL }))["Access-Control-Allow-Origin"]).toBeUndefined()
    expect(guardedCorsHeaders(req("POST", "/api/chat/send", { origin: SAME_ORIGIN }))["Access-Control-Allow-Origin"]).toBe(SAME_ORIGIN)
  })
})

describe("preflight", () => {
  test("cross-origin preflight on a guarded route is rejected, not 204'd", () => {
    const resp = preflightResponse(req("OPTIONS", "/api/chat/send", { origin: EVIL, contentType: null }), "/api/chat/send")
    expect(resp.status).toBe(403)
    expect(resp.headers.get("access-control-allow-origin")).toBeNull()
  })

  test("same-origin preflight still succeeds with a reflected origin", () => {
    const resp = preflightResponse(req("OPTIONS", "/api/chat/send", { origin: SAME_ORIGIN, contentType: null }), "/api/chat/send")
    expect(resp.status).toBe(204)
    expect(resp.headers.get("access-control-allow-origin")).toBe(SAME_ORIGIN)
  })

  test("unguarded routes keep the old blanket 204 + wildcard", () => {
    const resp = preflightResponse(req("OPTIONS", "/api/chat/history", { origin: EVIL, contentType: null }), "/api/chat/history")
    expect(resp.status).toBe(204)
    expect(resp.headers.get("access-control-allow-origin")).toBe("*")
  })
})
