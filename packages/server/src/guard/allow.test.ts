import { describe, expect, test } from "bun:test"
import { CORS, guardChatRequest, guardedCorsHeaders, preflightResponse, withGuardedCors } from "./index"
import { EVIL, HOST, SAME_ORIGIN, TUNNEL_HOST, TUNNEL_ORIGIN, noOrigin, req } from "./test-helpers"

// What the guard must NOT break. A guard that refuses the CLI, curl or the
// panel's own same-origin requests is a failed fix, not a strict one — so
// every refusal in ./reject.test.ts has its counterpart here.

describe("D9: the panel and the CLI still work on every newly guarded route", () => {
  // Every same-origin caller below is the real one, with the real
  // Content-Type it sends — see packages/client/src/{input,settings-modal/io,
  // input-timing}.ts, src-plugins/speak/*, and the eval dispatcher.
  for (const [method, p, ct] of [
    ["POST", "/api/chat/eval",                     "application/json"],
    ["POST", "/api/chat/eval-push",                "application/json"],
    ["PUT",  "/api/chat/draft",                    "application/json"],
    ["GET",  "/api/chat/draft",                    null],
    ["GET",  "/api/chat/subscribers",              null],
    ["PUT",  "/api/chat/parlay/settings",          "application/json"],
    ["POST", "/api/chat/tts",                      "application/json"],
    ["POST", "/api/chat/tts-event",                "application/json"],
    ["POST", "/api/chat/plugin/cursorless/rpc",    "application/json"],
    ["POST", "/api/debug/input-timing",            "application/json"],
  ] as const) {
    test(`same-origin ${method} ${p} is accepted`, () => {
      expect(guardChatRequest(req(method, p, { origin: SAME_ORIGIN, contentType: ct }), p)).toBeNull()
    })

    test(`no-Origin (CLI / curl / Talon / engine) ${method} ${p} is accepted`, () => {
      expect(guardChatRequest(noOrigin(method, p, ct), p)).toBeNull()
    })
  }

  test("the panel's multipart upload is NOT 415'd — /upload is JSON-exempt", () => {
    // A FormData POST sends multipart/form-data with a boundary. The origin
    // check is what defends this route; holding it to JSON would break every
    // real upload.
    const ct = "multipart/form-data; boundary=----WebKitFormBoundaryABC"
    expect(guardChatRequest(req("POST", "/api/chat/upload", { origin: SAME_ORIGIN, contentType: ct }), "/api/chat/upload")).toBeNull()
    // …and the no-Origin (curl -F) caller too.
    expect(guardChatRequest(noOrigin("POST", "/api/chat/upload", ct), "/api/chat/upload")).toBeNull()
  })

  test("a panel behind a Host-forwarding tunnel can still write, on the same-host branch alone", () => {
    // TUNNEL_ORIGIN's hostname is not loopback, private-LAN, .local or
    // allow-listed, so ./origin.ts's same-host comparison is the only thing
    // that accepts it — delete that comparison and this goes red while every
    // local-hostname case above stays green.
    const r = req("PUT", "/api/chat/draft", { origin: TUNNEL_ORIGIN, host: TUNNEL_HOST })
    expect(guardChatRequest(r, "/api/chat/draft")).toBeNull()
    expect(guardedCorsHeaders(r)["Access-Control-Allow-Origin"]).toBe(TUNNEL_ORIGIN)
  })

  test("the phone on the LAN can still upload and set a draft", () => {
    const lan = "http://192.168.1.42:31337"
    expect(guardChatRequest(req("PUT", "/api/chat/draft", { origin: lan, host: "192.168.1.42:31337" }), "/api/chat/draft")).toBeNull()
    const upload = req("POST", "/api/chat/upload", { origin: lan, host: "localhost:4242", contentType: "multipart/form-data; boundary=x" })
    expect(guardChatRequest(upload, "/api/chat/upload")).toBeNull()
  })
})

describe("legitimate callers still get through", () => {
  test("same-origin JSON POST from the panel is accepted", () => {
    expect(guardChatRequest(req("POST", "/api/chat/send", { origin: SAME_ORIGIN }), "/api/chat/send")).toBeNull()
  })

  test("CLI JSON POST (no Origin header at all) is accepted", () => {
    expect(guardChatRequest(noOrigin("POST", "/api/chat/send"), "/api/chat/send")).toBeNull()
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
