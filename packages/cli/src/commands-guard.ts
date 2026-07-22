// parlay CLI — `parlay guard`: runtime worktree-tangle + watcher-liveness alarm.
//
// Ported from firstmate's fm-guard.sh + fm-tangle-lib.sh (AGENTS.md §8) into
// parlay's idiom. Parlay's worktree-isolation primitive is `parlay variant`:
// a variant runs in a linked git worktree at ~/.parlay/worktrees/<id>, branched
// from the PRIMARY checkout the variant's cwd resolves to. The "worktree tangle"
// failure mode is a parlay agent branching/committing in that PRIMARY checkout
// instead of its own disposable worktree, stranding the primary on a feature
// branch. The brief/enrollment prose ports the UPSTREAM assertion ("work only in
// your worktree"); THIS is the RUNTIME backstop that surfaces a tangle loudly on
// the very next fleet action, plus a watcher-liveness beacon check while variants
// are in flight.
//
// Faithful to fm-guard's contract: the guard WARNS, it never BLOCKS — it always
// exits 0. Only usage errors exit 2. Banners go to stderr — the one channel every
// harness surfaces in tool output — so an agent cannot skim past them.

import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, utimesSync, writeFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { EXIT_USAGE } from "./config"
import { helpWanted } from "./help"
import { parseArgs } from "./args"

const WKTREES_DIR = join(homedir(), ".parlay", "worktrees")

function stateHome(): string {
  return process.env.PARLAY_STATE_HOME || join(homedir(), ".parlay")
}
function beaconPath(): string {
  return join(stateHome(), "guard", ".last-watcher-beat")
}

function sh(cmd: string, args: string[]): { ok: boolean; out: string } {
  const r = Bun.spawnSync([cmd, ...args], { stdout: "pipe", stderr: "pipe" })
  return { ok: r.exitCode === 0, out: new TextDecoder().decode(r.stdout).trim() }
}

// Resolve the default branch of the repo at <dir>: prefer origin/HEAD, then a
// local main/master. Returns the name, or "" if none. (fm_default_branch)
export function defaultBranch(dir: string): string {
  const head = sh("git", ["-C", dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"])
  if (head.ok && head.out) return head.out.replace(/^origin\//, "")
  for (const b of ["main", "master"]) {
    if (sh("git", ["-C", dir, "show-ref", "--verify", "--quiet", `refs/heads/${b}`]).ok) return b
  }
  return ""
}

// If the checkout at <root> is tangled — on a NAMED branch that is not its
// default — return that branch name; otherwise "". For every healthy state (not
// a work tree, detached HEAD, or already on the default branch) returns "".
// Detached HEAD is how linked worktrees legitimately sit, so they never trip.
// (fm_primary_tangle_branch)
export function primaryTangleBranch(root: string): string {
  if (!sh("git", ["-C", root, "rev-parse", "--is-inside-work-tree"]).ok) return ""
  const cur = sh("git", ["-C", root, "symbolic-ref", "--quiet", "--short", "HEAD"])
  if (!cur.ok || !cur.out) return "" // detached HEAD — legitimate for linked worktrees
  const def = defaultBranch(root)
  if (!def) return ""
  return cur.out === def ? "" : cur.out
}

const RULE = "━".repeat(67)

// Emit the bordered WORKTREE-TANGLE banner to stderr. readOnly softens the
// restore guidance (a read-only session leaves the fix to the lock holder).
export function emitTangleBanner(root: string, branch: string, readOnly: boolean): void {
  const def = defaultBranch(root) || "main"
  const lines = [
    `●${RULE}`,
    `●  WORKTREE TANGLE — PRIMARY CHECKOUT IS ON A FEATURE BRANCH`,
    `●  ${root} is on '${branch}', not its default branch '${def}'.`,
    `●  A parlay agent likely branched/committed in the primary instead of its own variant worktree.`,
    `●  The work is SAFE on the '${branch}' ref.`,
  ]
  if (readOnly) {
    lines.push(`●  This read-only session must leave restore to the session holding the fleet lock.`)
  } else {
    lines.push(`●  Restore the primary to '${def}' (non-destructive — the branch ref is kept):`)
    lines.push(`●      git -C ${root} checkout ${def}`)
    lines.push(`●  then re-run '${branch}' in a proper variant worktree: parlay variant launch <agent>`)
  }
  lines.push(`●${RULE}`)
  process.stderr.write(lines.join("\n") + "\n")
}

// Resolve the MAIN (primary) worktree for the repo containing <dir>. `git
// worktree list --porcelain` always lists the main worktree first, so its first
// "worktree <path>" line is the primary — the checkout the tangle guard must
// watch, even when called from inside a linked variant worktree. Returns "" if
// <dir> is not in a git repo.
export function mainWorktreePath(dir: string): string {
  const r = sh("git", ["-C", dir, "worktree", "list", "--porcelain"])
  if (!r.ok) return ""
  const m = r.out.match(/^worktree (.+)$/m)
  return m ? m[1] : ""
}

// Run the tangle check against <root> and emit the banner if tangled. Returns the
// offending branch (or ""). Importable so variant launch/teardown/monitor can run
// the runtime backstop inline. Silent (no banner) for every healthy state.
export function guardRepo(root: string, opts: { readOnly?: boolean } = {}): string {
  const branch = primaryTangleBranch(root)
  if (branch) emitTangleBanner(root, branch, opts.readOnly ?? false)
  return branch
}

// Count variants "in flight": linked worktrees parlay owns under WKTREES_DIR.
// A live variant means a task is riding on supervision, the same predicate that
// makes an absent watcher dangerous in firstmate.
function inFlightVariants(): number {
  if (!existsSync(WKTREES_DIR)) return 0
  try {
    return readdirSync(WKTREES_DIR, { withFileTypes: true }).filter(d => d.isDirectory()).length
  } catch {
    return 0
  }
}

// Beacon freshness: true if the beacon exists and was touched within `grace`
// seconds. A live watcher/monitor touches it every cycle via `parlay guard --beat`.
function beaconFresh(grace: number): { fresh: boolean; desc: string } {
  const p = beaconPath()
  if (!existsSync(p)) return { fresh: false, desc: "never" }
  const ageMs = Date.now() - statSync(p).mtimeMs
  const ageS = Math.round(ageMs / 1000)
  return { fresh: ageS <= grace, desc: `${ageS}s ago` }
}

// Touch the liveness beacon (idempotent; creates the dir). Called by a live
// watcher every poll cycle so the guard can tell "watcher alive" from "watcher
// down while variants are in flight".
export function beat(): void {
  const p = beaconPath()
  mkdirSync(join(stateHome(), "guard"), { recursive: true })
  if (!existsSync(p)) writeFileSync(p, "")
  const now = new Date()
  utimesSync(p, now, now)
}

function emitWatcherBanner(inFlight: number, desc: string, grace: number, readOnly: boolean): void {
  const lines = [
    `●${RULE}`,
    `●  WATCHER DOWN — SUPERVISION IS OFF`,
    `●  ${inFlight} variant(s) in flight, but no watcher has a fresh beacon (last beat: ${desc}, grace ${grace}s).`,
  ]
  if (readOnly) {
    lines.push(`●  This read-only session should report the lapse, not repair it.`)
  } else {
    lines.push(`●  Re-arm a supervisor monitor so the liveness beacon beats again; do not use shell & for watcher repair.`)
    lines.push(`●      parlay monitor --agent <supervisor-id>`)
  }
  lines.push(`●  This is a supervision warning only; the guarded operation WILL still run.`)
  lines.push(`●${RULE}`)
  process.stderr.write(lines.join("\n") + "\n")
}

export async function cmdGuard(args: string[]): Promise<void> {
  if (helpWanted("guard", args)) return
  const { opts } = parseArgs("guard", args, ["--beat", "--json", "--read-only"], ["--repo", "--grace"])

  // --beat: touch the beacon and exit (the watcher's per-cycle heartbeat).
  if (opts["--beat"]) {
    beat()
    if (opts["--json"]) console.log(JSON.stringify({ beat: beaconPath() }))
    else process.stderr.write(`parlay guard: beacon beat (${beaconPath()})\n`)
    return
  }

  const readOnly = opts["--read-only"] === true
  const graceRaw = opts["--grace"] as string | undefined
  const grace = graceRaw ? Number(graceRaw) : 300
  if (graceRaw && (!Number.isFinite(grace) || grace < 0)) {
    return void process.exit(EXIT_USAGE) // parseArgs already fails loud; this guards a bad --grace value
  }

  // Resolve the primary checkout: --repo, else the cwd's git toplevel.
  let root = (opts["--repo"] as string | undefined)?.trim()
  if (!root) {
    const top = sh("git", ["-C", process.cwd(), "rev-parse", "--show-toplevel"])
    root = top.ok ? top.out : ""
  }

  // 1) Tangle check FIRST, independent of in-flight tasks.
  const tangleBranch = root ? primaryTangleBranch(root) : ""
  if (tangleBranch) emitTangleBanner(root, tangleBranch, readOnly)

  // 2) Liveness check: only act with variants in flight (fm-guard exits 0 with none).
  const inFlight = inFlightVariants()
  const { fresh, desc } = beaconFresh(grace)
  const watcherDown = inFlight > 0 && !fresh
  if (watcherDown) emitWatcherBanner(inFlight, desc, grace, readOnly)

  if (opts["--json"]) {
    console.log(JSON.stringify({ root, tangleBranch, inFlight, watcherFresh: fresh, beaconAge: desc, grace, watcherDown }))
  } else if (!tangleBranch && !watcherDown) {
    process.stderr.write(`parlay guard: OK — ${root ? `primary '${root}' on its default branch` : "cwd not a git repo"}; ${inFlight} variant(s) in flight${inFlight ? `, watcher fresh (${desc})` : ""}.\n`)
  }

  // Always exit 0: the guard warns, it never blocks. (matches fm-guard)
}
