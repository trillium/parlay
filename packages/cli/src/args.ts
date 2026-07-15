// parlay CLI argument parser.

import { EXIT_USAGE } from "./config"
import { die } from "./http"

// Parse subcommand args. Boolean flags in `flags`, value-taking flags in `valueFlags`.
// Unknown -x/--x tokens fail loud with exit 2. `--` ends flag parsing.
export function parseArgs(
  cmd: string,
  args: string[],
  flags: string[] = [],
  valueFlags: string[] = [],
): { positionals: string[]; opts: Record<string, string | true> } {
  const positionals: string[] = []
  const opts: Record<string, string | true> = {}
  let noMoreFlags = false
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (noMoreFlags || !a.startsWith("-")) { positionals.push(a); continue }
    if (a === "--") { noMoreFlags = true; continue }
    if (flags.includes(a)) { opts[a] = true; continue }
    if (valueFlags.includes(a)) {
      const v = args[++i]
      if (v === undefined) die(`parlay ${cmd}: flag ${a} requires a value`, EXIT_USAGE)
      opts[a] = v
      continue
    }
    die(`parlay ${cmd}: unknown flag "${a}"`, EXIT_USAGE)
  }
  return { positionals, opts }
}
