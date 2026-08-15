import { describe, expect, test } from "bun:test"
import { isGuardedChatPath } from "./paths"

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

  // ACCEPTED RESIDUE, TRACKED SEPARATELY. The DELETE alias is guarded; the GET
  // is not, and it does disclose every registered agent id under the wildcard
  // CORS — the same class of disclosure /subscribers was guarded for. Left
  // outside deliberately, filed as `identifier-disclosure-remains-on-sse`; the
  // assertion pins the boundary as it actually stands, not as safe.
  test("DELETE /api/chat/agents/:id is guarded but GET /api/chat/agents is not", () => {
    expect(isGuardedChatPath("/api/chat/agents/some-agent")).toBe(true)
    expect(isGuardedChatPath("/api/chat/agents")).toBe(false)
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

  test("a plugin route nobody has written yet is guarded by the prefix", () => {
    // The point of the prefix: a future plugin ships closed, not open.
    expect(isGuardedChatPath("/api/chat/plugin/some-future-plugin/rpc")).toBe(true)
  })

  // A GET is not evidence of a read: handlePollRequest registers an unknown
  // channel in the agent registry, broadcasts agent_register and persists to
  // disk. This assertion FAILS against the pre-fix route set.
  test("GET /api/chat/poll is guarded — it writes the registry", () => {
    expect(isGuardedChatPath("/api/chat/poll")).toBe(true)
  })

  // /events and /agents are ACCEPTED RESIDUE here too, not proven-inert reads
  // — see the guarded-route-set comment in ./paths.ts.
  test("read and SSE routes stay unguarded", () => {
    for (const p of [
      "/api/chat/history", "/api/chat/events", "/api/chat/agents",
      "/api/chat/version", "/api/chat/pages", "/api/chat/plugins",
    ]) expect(isGuardedChatPath(p)).toBe(false)
  })

  test("GET /api/chat/uploads/<name> stays unguarded — an <img> must load it", () => {
    // Note how close this is to the guarded "/api/chat/upload": the guarded
    // set is exact-match, so the trailing "s" keeps the serve route out.
    expect(isGuardedChatPath("/api/chat/uploads/abc123def456.png")).toBe(false)
    expect(isGuardedChatPath("/api/chat/upload")).toBe(true)
  })
})
