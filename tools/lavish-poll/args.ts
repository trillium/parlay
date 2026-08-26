// Argument parsing for the lavish-poll bridge.
//
// Split out from the entry point so it can be unit-tested as a pure function.
// It reports failure by throwing UsageError rather than calling process.exit(),
// which is what makes that possible — index.ts turns the throw into an exit.

export const USAGE =
  "usage: <agentId> <parlayUrl> <file> [--agent-reply <text>] [--timeout-ms <n>]"

export class UsageError extends Error {}

export interface PollArgs {
  file: string
  agentReply?: string
  timeoutMs?: number
}

/**
 * Parses the `[poll-args...]` tail of `lavish poll`.
 *
 * Every branch either consumes a known flag or throws. The original parser had
 * no terminal `else`, so a typo'd flag was silently discarded and the run
 * reported success — the same shape as the `parlay lavish-import --dry-run`
 * bug, where a guessed safety flag performed a real import against the live
 * Parlay. AGENTS.md: a dropped flag is not a degraded flag, it is a hard exit.
 */
export function parsePollArgs(argv: readonly string[]): PollArgs {
  let file = ""
  let agentReply: string | undefined
  let timeoutMs: number | undefined

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i] ?? ""

    if (arg === "--agent-reply") {
      const v = argv[++i]
      // A flag that blindly swallows the next token cannot tell "value omitted"
      // from "value is the next flag": `--agent-reply --timeout-ms 5` used to
      // POST the literal text "--timeout-ms" to the captain as the agent's reply.
      if (v === undefined || v.startsWith("--")) throw new UsageError("--agent-reply needs a value")
      agentReply = v
      continue
    }

    if (arg === "--timeout-ms") {
      const v = argv[++i]
      if (v === undefined || v.startsWith("--")) throw new UsageError("--timeout-ms needs a value")
      const n = Number(v)
      // Number("abc") is NaN and Number("") is 0 — both FALSY, so an unvalidated
      // value fell straight through the `timeoutMs ? … : Infinity` deadline and
      // turned a malformed timeout into "never time out", the exact opposite of
      // what the caller asked for.
      if (!Number.isFinite(n) || n <= 0) {
        throw new UsageError(`--timeout-ms must be a positive number, got ${JSON.stringify(v)}`)
      }
      timeoutMs = n
      continue
    }

    if (arg.startsWith("--")) throw new UsageError(`unknown flag ${arg}`)

    if (arg) {
      if (file) {
        throw new UsageError(
          `only one file may be given, got ${JSON.stringify(file)} and ${JSON.stringify(arg)}`,
        )
      }
      file = arg
    }
  }

  // Without a file the native poll requests `?file=` and every next_step string
  // interpolates an empty path, producing instructions that name no file at all.
  if (!file) throw new UsageError("a file argument is required")

  return { file, agentReply, timeoutMs }
}
