import { describe, expect, test } from "bun:test"
import { JSON_EXEMPT_PATHS, isGuardedChatPath } from "./paths"

// The route SET, on its own. This is the file that would have caught D9: the
// guard mechanism was fine, these classifications were not.

describe("guarded route set", () => {
  test("mutating chat routes are guarded", () => {
    for (const p of [
      "/api/chat/send", "/api/chat/reply", "/api/chat/alert", "/api/chat/system",
      "/api/chat/register-agent", "/api/chat/unregister", "/api/chat/declare-channel",
      "/api/chat/clear", "/api/chat/navigate", "/api/chat/reload", "/api/chat/device-cmd",
    ]) expect(isGuardedChatPath(p)).toBe(true)
  })

  // Formerly accepted residue (`identifier-disclosure-remains-on-sse`), now
  // closed: GET /api/chat/agents discloses every registered agent id — the
  // same class of disclosure /subscribers was guarded for — so it is guarded
  // exactly like /subscribers. The DELETE alias stays guarded via the prefix.
  test("both GET /api/chat/agents and DELETE /api/chat/agents/:id are guarded", () => {
    expect(isGuardedChatPath("/api/chat/agents/some-agent")).toBe(true)
    expect(isGuardedChatPath("/api/chat/agents")).toBe(true)
  })

  // POST /api/chat/debug-log appends request-shaped lines to a file on disk
  // ($PARLAY_STATE_HOME/debug.log) — a state write, guarded like /tts-report.
  test("debug-log is guarded", () => {
    expect(isGuardedChatPath("/api/chat/debug-log")).toBe(true)
  })

  // task-6ai1 / D9: these five are the routes the end-to-end verifier chained
  // into full control of the panel. Each of these assertions FAILS against the
  // pre-fix route set.
  test("the D9 routes are guarded", () => {
    for (const p of [
      "/api/chat/eval", "/api/chat/eval-push", "/api/chat/draft",
      "/api/chat/upload", "/api/chat/subscribers",
    ]) expect(isGuardedChatPath(p)).toBe(true)
  })

  test("the mutating routes found alongside D9 are guarded too", () => {
    for (const p of [
      "/api/chat/parlay/settings", "/api/chat/tts", "/api/chat/tts-correction",
      "/api/chat/tts-report", "/api/chat/tts-event", "/api/chat/tts/validate-splits",
      "/api/chat/plugin/cursorless/rpc", "/api/chat/plugin/cursorless/response",
      "/api/debug/input-timing",
    ]) expect(isGuardedChatPath(p)).toBe(true)
  })

  // The Cursorless bridge's two routes are classified together as guarded and
  // apart on the content-type gate. Both halves are pinned so a future edit
  // cannot silently drop /rpc from either set: dropping it from the guarded
  // set would open it cross-origin, dropping it from the exempt set would 415
  // the Talon caller.
  test("POST /api/chat/plugin/cursorless/rpc is guarded AND JSON-exempt", () => {
    expect(isGuardedChatPath("/api/chat/plugin/cursorless/rpc")).toBe(true)
    expect(JSON_EXEMPT_PATHS.has("/api/chat/plugin/cursorless/rpc")).toBe(true)
  })

  // Same classification, reached by the same three-part test: the handler is
  // `await req.json()` (../tts-validate.ts), the documented contract is a JSON
  // body under no stated content type, and no caller in this repo posts to it.
  // Both halves pinned for the same reason as /rpc above — dropping it from
  // the guarded set opens it cross-origin, dropping it from the exempt set
  // 415s a hand-run `curl -d`.
  test("POST /api/chat/tts/validate-splits is guarded AND JSON-exempt", () => {
    expect(isGuardedChatPath("/api/chat/tts/validate-splits")).toBe(true)
    expect(JSON_EXEMPT_PATHS.has("/api/chat/tts/validate-splits")).toBe(true)
  })

  // The exemption is one route deep, not the whole tts family: its siblings
  // all have in-repo panel callers that send Content-Type: application/json
  // (packages/client/src-plugins/speak/*), so they keep both layers.
  test("the tts siblings are NOT exempt", () => {
    for (const p of [
      "/api/chat/tts", "/api/chat/tts-correction",
      "/api/chat/tts-report", "/api/chat/tts-event",
    ]) expect(JSON_EXEMPT_PATHS.has(p)).toBe(false)
  })

  test("the panel's /response POST keeps both layers", () => {
    // It sends Content-Type: application/json explicitly, so there is nothing
    // to exempt — see packages/client/src-plugins/cursorless.ts.
    expect(isGuardedChatPath("/api/chat/plugin/cursorless/response")).toBe(true)
    expect(JSON_EXEMPT_PATHS.has("/api/chat/plugin/cursorless/response")).toBe(false)
  })

  test("a plugin route nobody has written yet is guarded by the prefix", () => {
    // The point of the prefix: a future plugin ships closed, not open.
    expect(isGuardedChatPath("/api/chat/plugin/some-future-plugin/rpc")).toBe(true)
  })

  // A GET is not automatically evidence of a read: handlePollRequest used to
  // register an unknown channel in the agent registry, broadcast
  // agent_register, and persist to disk (fixed under task-1t0m — poll is now
  // genuinely read-only). It stays in this guarded set anyway, defense in
  // depth: it still returns message content and drives presence bookkeeping,
  // so cross-origin should not reach it either way.
  test("GET /api/chat/poll is guarded", () => {
    expect(isGuardedChatPath("/api/chat/poll")).toBe(true)
  })

  // The inert read surface. /events and /agents are no longer in this list —
  // they disclose identifiers and are guarded as of the residue closure; see
  // the guarded-route-set comment in ./paths.ts. /history stays here by prior
  // decision (the documented world-readable surface).
  test("read routes stay unguarded", () => {
    for (const p of [
      "/api/chat/history", "/api/chat/version", "/api/chat/pages", "/api/chat/plugins",
    ]) expect(isGuardedChatPath(p)).toBe(false)
  })

  test("the SSE stream and the agents read route are guarded — they disclose identifiers", () => {
    expect(isGuardedChatPath("/api/chat/events")).toBe(true)
    expect(isGuardedChatPath("/api/chat/agents")).toBe(true)
  })

  test("GET /api/chat/uploads/<name> stays unguarded — an <img> must load it", () => {
    // Note how close this is to the guarded "/api/chat/upload": the guarded
    // set is exact-match, so the trailing "s" keeps the serve route out.
    expect(isGuardedChatPath("/api/chat/uploads/abc123def456.png")).toBe(false)
    expect(isGuardedChatPath("/api/chat/upload")).toBe(true)
  })
})
