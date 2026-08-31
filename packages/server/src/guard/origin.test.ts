import { describe, expect, test, afterEach } from "bun:test"
import { isJsonContentType, originAllowed } from "./origin"
import { EVIL, HOST, OTHER_HOST, SAME_ORIGIN, TUNNEL_HOST, TUNNEL_ORIGIN, noOrigin, req } from "./test-helpers"

afterEach(() => { delete process.env.PARLAY_ALLOWED_ORIGINS })

describe("origin policy: who is refused", () => {
  test("a hostile page", () => {
    expect(originAllowed(req("POST", "/api/chat/send", { origin: EVIL }))).toBe(false)
  })

  test("a sandboxed iframe / file:// origin ('null')", () => {
    expect(originAllowed(req("POST", "/api/chat/send", { origin: "null" }))).toBe(false)
  })

  test("an attacker origin that merely embeds the host name", () => {
    const o = "https://localhost:4242.evil.example.com"
    expect(originAllowed(req("POST", "/api/chat/send", { origin: o }))).toBe(false)
  })

  test("a non-http scheme", () => {
    expect(originAllowed(req("POST", "/api/chat/send", { origin: "chrome-extension://abcdef" }))).toBe(false)
  })

  test("a non-local tunnel origin arriving on a DIFFERENT Host", () => {
    // The control for the accept case below: same non-local origin, wrong
    // Host, so the same-host comparison cannot save it and nothing else can
    // either. Without this, that accept case could be passing for the wrong
    // reason.
    expect(originAllowed(req("POST", "/api/chat/send", { origin: TUNNEL_ORIGIN, host: OTHER_HOST }))).toBe(false)
  })
})

describe("origin policy: who still gets through", () => {
  test("the CLI / curl / hooks — no Origin header at all", () => {
    // The single most load-bearing rule here: a browser cannot forge the
    // absence of Origin on a cross-site request, so allowing it costs nothing
    // and is what keeps the live fleet working.
    expect(originAllowed(noOrigin("POST", "/api/chat/send"))).toBe(true)
  })

  test("the panel on its own origin", () => {
    expect(originAllowed(req("POST", "/api/chat/send", { origin: SAME_ORIGIN }))).toBe(true)
  })

  test("the phone on the LAN — same host, and as a private origin", () => {
    const lan = "http://192.168.1.42:4242"
    expect(originAllowed(req("POST", "/api/chat/send", { origin: lan, host: "192.168.1.42:4242" }))).toBe(true)
    // Pulse may reverse-proxy to this server under a rewritten Host.
    expect(originAllowed(req("POST", "/api/chat/send", { origin: lan, host: "localhost:4242" }))).toBe(true)
  })

  test("a loopback origin on another port (Pulse → standalone proxy)", () => {
    const r = req("POST", "/api/chat/send", { origin: "http://127.0.0.1:4242", host: "localhost:4242" })
    expect(originAllowed(r)).toBe(true)
  })

  test("a bonjour .local name", () => {
    const r = req("POST", "/api/chat/send", { origin: "http://captain.local:4242", host: HOST })
    expect(originAllowed(r)).toBe(true)
  })

  test("the panel behind a Host-forwarding tunnel — ONLY the same-host comparison can accept it", () => {
    // Isolates the branch at ./origin.ts's `u.host === host` comparison. Every
    // other accepted fixture here is a local hostname, so deleting that
    // comparison leaves them all green; this one goes red, which is the point.
    // The shape it protects is real: a panel served through a reverse proxy or
    // tunnel under a non-local name. If the branch silently regresses, that
    // panel gets 403 on every mutating route while the suite stays green.
    expect(originAllowed(req("POST", "/api/chat/send", { origin: TUNNEL_ORIGIN, host: TUNNEL_HOST }))).toBe(true)
  })

  test("PARLAY_ALLOWED_ORIGINS opts a specific public origin in", () => {
    const r = req("POST", "/api/chat/send", { origin: "https://tunnel.example.com" })
    expect(originAllowed(r)).toBe(false)
    process.env.PARLAY_ALLOWED_ORIGINS = "https://other.example.com, https://tunnel.example.com"
    expect(originAllowed(r)).toBe(true)
    // …and only that one.
    expect(originAllowed(req("POST", "/api/chat/send", { origin: EVIL }))).toBe(false)
  })

  test("PARLAY_ALLOWED_ORIGINS='*' is the explicit opt-out escape hatch", () => {
    process.env.PARLAY_ALLOWED_ORIGINS = "*"
    expect(originAllowed(req("POST", "/api/chat/send", { origin: EVIL }))).toBe(true)
  })
})

describe("content type", () => {
  test("charset and casing on a real JSON type are accepted", () => {
    expect(isJsonContentType("application/json")).toBe(true)
    expect(isJsonContentType("Application/JSON; charset=utf-8")).toBe(true)
  })

  test("the shapes a CORS simple request may use are not JSON", () => {
    // Exactly the content types a cross-origin POST can send WITHOUT a
    // preflight — rejecting them is what closes the simple-request bypass.
    for (const ct of ["text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data"]) {
      expect(isJsonContentType(ct)).toBe(false)
    }
    expect(isJsonContentType("application/jsonp")).toBe(false)
    expect(isJsonContentType(null)).toBe(false)
  })
})
