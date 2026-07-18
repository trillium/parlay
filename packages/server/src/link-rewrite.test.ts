import { describe, test, expect, beforeEach, afterEach } from "bun:test"
import {
  rewriteLocalhostLinks,
  rewriteMessageForServe,
  rewriteMessagesForServe,
  __resetLinkRewriteCacheForTest,
} from "./link-rewrite"

// Each test controls PARLAY_PUBLIC_HOST directly and resets the module cache so
// the value is re-resolved. Restore the original env after each test so the
// suite leaves no global state behind.
const ORIGINAL = process.env.PARLAY_PUBLIC_HOST

function setHost(value: string | undefined): void {
  if (value === undefined) delete process.env.PARLAY_PUBLIC_HOST
  else process.env.PARLAY_PUBLIC_HOST = value
  __resetLinkRewriteCacheForTest()
}

beforeEach(() => __resetLinkRewriteCacheForTest())

afterEach(() => {
  if (ORIGINAL === undefined) delete process.env.PARLAY_PUBLIC_HOST
  else process.env.PARLAY_PUBLIC_HOST = ORIGINAL
  __resetLinkRewriteCacheForTest()
})

describe("rewriteLocalhostLinks — with PARLAY_PUBLIC_HOST set", () => {
  test("rewrites localhost host, preserving port and path", () => {
    setHost("100.74.138.74")
    expect(rewriteLocalhostLinks("http://localhost:31337/notes/")).toBe(
      "http://100.74.138.74:31337/notes/",
    )
  })

  test("rewrites 127.0.0.1 identically to localhost", () => {
    setHost("100.74.138.74")
    expect(rewriteLocalhostLinks("http://127.0.0.1:31337/notes/")).toBe(
      "http://100.74.138.74:31337/notes/",
    )
  })

  test("preserves the full path and query string exactly", () => {
    setHost("100.74.138.74")
    const input = "http://localhost:31337/status/?tab=plans&open=1#frag"
    expect(rewriteLocalhostLinks(input)).toBe(
      "http://100.74.138.74:31337/status/?tab=plans&open=1#frag",
    )
  })

  test("preserves a different port verbatim", () => {
    setHost("mac.tail-scale.ts.net")
    expect(rewriteLocalhostLinks("http://localhost:8080/x")).toBe(
      "http://mac.tail-scale.ts.net:8080/x",
    )
  })

  test("rewrites every localhost link in a multi-link message", () => {
    setHost("100.74.138.74")
    const input =
      "see http://localhost:31337/a and http://127.0.0.1:31337/b plus http://localhost:9000/c"
    expect(rewriteLocalhostLinks(input)).toBe(
      "see http://100.74.138.74:31337/a and http://100.74.138.74:31337/b plus http://100.74.138.74:9000/c",
    )
  })

  test("leaves a non-localhost URL untouched", () => {
    setHost("100.74.138.74")
    const input = "check https://github.com/trillium/parlay/pull/1"
    expect(rewriteLocalhostLinks(input)).toBe(input)
  })

  test("does not match a hostname that merely starts with localhost", () => {
    setHost("100.74.138.74")
    // No port immediately after the host token → not a Parlay localhost link.
    const input = "http://localhost.evil.example/phish"
    expect(rewriteLocalhostLinks(input)).toBe(input)
  })

  test("does not touch an https localhost link (only http is rewritten)", () => {
    setHost("100.74.138.74")
    const input = "https://localhost:31337/secure"
    expect(rewriteLocalhostLinks(input)).toBe(input)
  })

  test("rewrites a bare localhost link with no trailing path", () => {
    setHost("100.74.138.74")
    expect(rewriteLocalhostLinks("http://localhost:31337")).toBe(
      "http://100.74.138.74:31337",
    )
  })

  test("only swaps the host inside surrounding prose", () => {
    setHost("box.local")
    const input = "Open http://localhost:31337/clipboard/ to copy it."
    expect(rewriteLocalhostLinks(input)).toBe(
      "Open http://box.local:31337/clipboard/ to copy it.",
    )
  })
})

describe("rewriteLocalhostLinks — opt-in gating", () => {
  test("unset PARLAY_PUBLIC_HOST leaves text identical", () => {
    setHost(undefined)
    const input = "http://localhost:31337/notes/"
    expect(rewriteLocalhostLinks(input)).toBe(input)
  })

  test("empty PARLAY_PUBLIC_HOST leaves text identical", () => {
    setHost("")
    const input = "http://localhost:31337/notes/"
    expect(rewriteLocalhostLinks(input)).toBe(input)
  })

  test("whitespace-only PARLAY_PUBLIC_HOST is treated as unset", () => {
    setHost("   ")
    const input = "http://127.0.0.1:31337/notes/"
    expect(rewriteLocalhostLinks(input)).toBe(input)
  })
})

describe("rewriteLocalhostLinks — hostname config values", () => {
  test("accepts a short tailnet hostname (the captain's preference)", () => {
    setHost("macbook")
    expect(rewriteLocalhostLinks("http://localhost:31337/notes/")).toBe(
      "http://macbook:31337/notes/",
    )
  })

  test("accepts a full tailnet FQDN", () => {
    setHost("macbook.hippo-tilapia.ts.net")
    expect(rewriteLocalhostLinks("http://127.0.0.1:31337/x")).toBe(
      "http://macbook.hippo-tilapia.ts.net:31337/x",
    )
  })
})

describe("rewriteMessageForServe", () => {
  test("returns a clone with rewritten text, leaving the original untouched", () => {
    setHost("macbook")
    const stored = { id: "m1", role: "agent", ts: "t", text: "http://localhost:31337/a" }
    const served = rewriteMessageForServe(stored)
    expect(served.text).toBe("http://macbook:31337/a")
    // The stored object is presentation-immutable — never mutated in place.
    expect(stored.text).toBe("http://localhost:31337/a")
    expect(served).not.toBe(stored)
  })

  test("preserves all other message fields on the clone", () => {
    setHost("macbook")
    const stored = {
      id: "m2",
      role: "agent" as const,
      ts: "2026-07-17T00:00:00Z",
      text: "open http://localhost:31337/status/",
      channel: "main-agent",
      images: ["http://exchange/u/x.png"],
    }
    const served = rewriteMessageForServe(stored)
    expect(served.id).toBe("m2")
    expect(served.channel).toBe("main-agent")
    expect(served.images).toEqual(["http://exchange/u/x.png"])
    expect(served.text).toBe("open http://macbook:31337/status/")
  })

  test("returns the SAME reference when nothing changes (no-op, no allocation)", () => {
    setHost("macbook")
    const stored = { id: "m3", text: "no links here" }
    expect(rewriteMessageForServe(stored)).toBe(stored)
  })

  test("returns the SAME reference when host is unset", () => {
    setHost(undefined)
    const stored = { id: "m4", text: "http://localhost:31337/a" }
    expect(rewriteMessageForServe(stored)).toBe(stored)
  })

  test("tolerates a message with no text field", () => {
    setHost("macbook")
    const stored = { id: "m5" }
    expect(rewriteMessageForServe(stored)).toBe(stored)
  })
})

describe("rewriteMessagesForServe", () => {
  test("rewrites each message in a history array", () => {
    setHost("macbook")
    const history = [
      { id: "a", text: "http://localhost:31337/1" },
      { id: "b", text: "no link" },
      { id: "c", text: "http://127.0.0.1:31337/2" },
    ]
    const served = rewriteMessagesForServe(history)
    expect(served.map(m => m.text)).toEqual([
      "http://macbook:31337/1",
      "no link",
      "http://macbook:31337/2",
    ])
    // Original array + its untouched elements are unchanged.
    expect(history[0].text).toBe("http://localhost:31337/1")
  })

  test("returns the SAME array reference when nothing changes", () => {
    setHost("macbook")
    const history = [{ id: "a", text: "plain" }, { id: "b", text: "also plain" }]
    expect(rewriteMessagesForServe(history)).toBe(history)
  })

  test("returns the SAME array reference when host is unset", () => {
    setHost(undefined)
    const history = [{ id: "a", text: "http://localhost:31337/1" }]
    expect(rewriteMessagesForServe(history)).toBe(history)
  })

  test("handles an empty array", () => {
    setHost("macbook")
    const history: { text: string }[] = []
    expect(rewriteMessagesForServe(history)).toBe(history)
  })
})

describe("rewriteLocalhostLinks — resilience", () => {
  test("empty string in returns empty string out", () => {
    setHost("100.74.138.74")
    expect(rewriteLocalhostLinks("")).toBe("")
  })

  test("text with no http:// scheme is returned unchanged", () => {
    setHost("100.74.138.74")
    const input = "just some plain text, no links here"
    expect(rewriteLocalhostLinks(input)).toBe(input)
  })

  test("caches the resolved host across calls within a process", () => {
    setHost("100.74.138.74")
    expect(rewriteLocalhostLinks("http://localhost:31337/a")).toBe(
      "http://100.74.138.74:31337/a",
    )
    // Change the env WITHOUT resetting the cache: the first value must stick.
    process.env.PARLAY_PUBLIC_HOST = "10.0.0.9"
    expect(rewriteLocalhostLinks("http://localhost:31337/b")).toBe(
      "http://100.74.138.74:31337/b",
    )
  })
})
