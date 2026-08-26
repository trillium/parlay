// Every case here used to parse "successfully" and let the run report success.
// parsePollArgs is a pure function, so these are direct calls rather than the
// subprocess spawns in poll.test.ts.

import { test, expect, describe } from "bun:test"
import { parsePollArgs, UsageError } from "./args"

function reject(argv: string[]): string {
  try {
    parsePollArgs(argv)
  } catch (e) {
    if (e instanceof UsageError) return e.message
    throw e
  }
  throw new Error(`parsePollArgs(${JSON.stringify(argv)}) returned normally, want a UsageError`)
}

describe("--timeout-ms", () => {
  test("a non-numeric value is rejected, not silently turned into 'wait forever'", () => {
    // The old parser did `timeoutMs = Number(pollArgs[++i])` and the deadline
    // was `timeoutMs ? Date.now() + timeoutMs : Infinity`. NaN is falsy, so
    // this exact invocation polled forever — the opposite of a timeout.
    expect(reject(["doc.md", "--timeout-ms", "abc"])).toContain("must be a positive number")
  })

  test("0 is rejected rather than meaning 'no timeout'", () => {
    expect(reject(["doc.md", "--timeout-ms", "0"])).toContain("must be a positive number")
  })

  test("a negative value is rejected", () => {
    expect(reject(["doc.md", "--timeout-ms", "-5"])).toContain("must be a positive number")
  })

  test("an empty value is rejected — Number('') is 0, which is falsy", () => {
    expect(reject(["doc.md", "--timeout-ms", ""])).toContain("must be a positive number")
  })

  test("Infinity is rejected", () => {
    expect(reject(["doc.md", "--timeout-ms", "Infinity"])).toContain("must be a positive number")
  })

  test("a missing value is rejected instead of consuming nothing", () => {
    expect(reject(["doc.md", "--timeout-ms"])).toContain("needs a value")
  })

  test("a following flag is not swallowed as the value", () => {
    // Number("--agent-reply") is NaN, which the old code accepted silently —
    // and it ate the --agent-reply flag itself on the way past.
    expect(reject(["doc.md", "--timeout-ms", "--agent-reply", "hi"])).toContain("needs a value")
  })

  test("a valid value is accepted", () => {
    expect(parsePollArgs(["doc.md", "--timeout-ms", "1500"])).toEqual({
      file: "doc.md",
      agentReply: undefined,
      timeoutMs: 1500,
    })
  })
})

describe("--agent-reply", () => {
  test("a following flag is not swallowed as the reply text", () => {
    // Otherwise the literal string "--timeout-ms" gets POSTed to the captain as
    // the agent's reply.
    expect(reject(["doc.md", "--agent-reply", "--timeout-ms", "500"])).toContain("needs a value")
  })

  test("a missing value is rejected", () => {
    expect(reject(["doc.md", "--agent-reply"])).toContain("needs a value")
  })

  test("a normal reply is accepted", () => {
    expect(parsePollArgs(["doc.md", "--agent-reply", "done"]).agentReply).toBe("done")
  })

  test("a reply that merely looks like prose starting with a dash is accepted", () => {
    expect(parsePollArgs(["doc.md", "--agent-reply", "-1 is fine"]).agentReply).toBe("-1 is fine")
  })
})

describe("positional and unknown arguments", () => {
  test("an unknown flag is an error, not a discarded token", () => {
    // A dropped flag is not a degraded flag. `--dry-run` must never be mistaken
    // for a safety flag that was honoured.
    expect(reject(["doc.md", "--dry-run"])).toContain("unknown flag --dry-run")
  })

  test("a missing file is rejected", () => {
    expect(reject(["--timeout-ms", "500"])).toContain("a file argument is required")
  })

  test("no arguments at all is rejected", () => {
    expect(reject([])).toContain("a file argument is required")
  })

  test("two files are rejected rather than last-one-wins", () => {
    expect(reject(["a.md", "b.md"])).toContain("only one file may be given")
  })

  test("flags may precede the file", () => {
    expect(parsePollArgs(["--timeout-ms", "10", "doc.md"]).file).toBe("doc.md")
  })
})
