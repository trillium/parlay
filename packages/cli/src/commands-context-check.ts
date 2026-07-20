// parlay context-check — machine-readable rotation advisory.
//
// Half of the Mayor auto-rotation loop (task-gbs). When a persistent agent's context
// window approaches exhaustion (~85%), it should write a handoff and exit cleanly so the
// supervisor respawns a fresh one. Claude Code exposes no in-process context gauge to a
// CLI, so this verb is a pure DECISION function: the caller passes the context percentage
// it knows (from its harness / an env var), and this prints a one-line verdict plus an
// exit code a script can branch on. The supervisor-respawn loop itself is GasCity's side.
//
//   below threshold  → "OK <pct>% (threshold <t>%)"                      exit 0
//   at/above         → "ROTATE: create handoff now, then identity --submit …"  exit 3
//   unparseable pct  → usage error                                        exit 2 (EXIT_USAGE)
//
// Exit 3 is deliberately distinct from 0 (ok) / 1 (runtime) / 2 (usage) so a scripted
// caller can `case $? in 3) rotate;; esac` without parsing text.

import { EXIT_USAGE } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

export const EXIT_ROTATE = 3
export const DEFAULT_ROTATE_THRESHOLD = 85

// Parse a context-percentage token. Accepts "85", "85%", "85.4", "0.85" (fraction ≤ 1 is
// scaled to a percent). Returns undefined for anything non-numeric or out of 0–100.
export function parsePercent(raw: string | undefined): number | undefined {
  if (raw === undefined) return undefined
  const cleaned = raw.trim().replace(/%$/, "")
  if (cleaned === "") return undefined
  const n = Number(cleaned)
  if (!Number.isFinite(n) || n < 0) return undefined
  const pct = n > 0 && n <= 1 ? n * 100 : n // fraction form (0.85) → 85
  return pct <= 100 ? pct : undefined
}

// Pure verdict: does `pct` meet/exceed `threshold`? Returned as a struct so the
// decision is unit-testable without touching process exit / stdout.
export function rotateVerdict(
  pct: number,
  threshold: number = DEFAULT_ROTATE_THRESHOLD,
): { rotate: boolean; line: string; exitCode: number } {
  const p = Math.round(pct * 10) / 10
  if (p >= threshold) {
    return {
      rotate: true,
      line: `ROTATE: create handoff now, then identity --submit (context ${p}% ≥ ${threshold}%)`,
      exitCode: EXIT_ROTATE,
    }
  }
  return { rotate: false, line: `OK ${p}% (threshold ${threshold}%)`, exitCode: 0 }
}

export async function cmdContextCheck(args: string[]) {
  if (helpWanted("context-check", args)) return
  const { positionals, opts } = parseArgs("context-check", args, [], ["--threshold"])

  const pct = parsePercent(positionals[0])
  if (pct === undefined) {
    return die(
      "parlay context-check: need a context percentage (0–100), e.g. context-check 85 — " +
      "pass what your harness knows; accepts 85, 85%, or 0.85",
      EXIT_USAGE,
    )
  }

  const threshold = opts["--threshold"] !== undefined
    ? parsePercent(opts["--threshold"] as string)
    : DEFAULT_ROTATE_THRESHOLD
  if (threshold === undefined) {
    return die("parlay context-check: --threshold must be a percentage (0–100)", EXIT_USAGE)
  }

  const { line, exitCode } = rotateVerdict(pct, threshold)
  console.log(line)
  if (exitCode !== 0) process.exitCode = exitCode
}
