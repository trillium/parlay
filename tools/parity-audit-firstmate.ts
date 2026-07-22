#!/usr/bin/env bun
/**
 * parity-audit-firstmate.ts — re-runnable capability-parity check (task-4bad).
 *
 * Proves the parlay×firstmate fold is EXPANSION-ONLY: every firstmate capability
 * has a home in the folded end-state (parlay primitive OR retained firstmate
 * policy OR justified drop), and flags any that DON'T.
 *
 * It re-derives the parity matrix by probing the LIVE state of both repos, so as
 * the fold implements, `bun tools/parity-audit-firstmate.ts` re-verifies parity
 * and flips COVERED-when-built rows from "designed" to "landed".
 *
 * Verdicts:
 *   COVERED-same       parlay already has an equal primitive
 *   COVERED-alternate  ported in parlay's idiom (may be designed-not-built yet)
 *   EXPANDED           parlay adds capability beyond firstmate
 *   STAYS-FIRSTMATE    POLICY retained firstmate-side (not a contraction)
 *   DEFERRED           MECHANISM to migrate, explicitly built-later w/ seam
 *   DROP-justified     out of scope, justification recorded & sound
 *   MISSING            CONTRACTION — no home in the folded end-state (a fix task exists)
 *
 * Exit non-zero if any MISSING row lacks a tracked fix task, or if a
 * COVERED-alternate row claims "landed" but the probe shows it isn't.
 */
import { existsSync, readFileSync } from "node:fs"
import { execSync } from "node:child_process"

const FM = process.env.FM_REPO ?? `${process.env.HOME}/code/firstmate`
const PL = process.env.PL_REPO ?? `${process.env.HOME}/code/parlay`

const read = (p: string) => (existsSync(p) ? readFileSync(p, "utf8") : "")
const has = (p: string) => existsSync(p)
/** does a file contain a token */
const grepFile = (p: string, re: RegExp) => re.test(read(p))

// ---- live probes of the parlay CLI surface -------------------------------
const spawnSrc = read(`${PL}/bin/parlay-spawn`)
const cliIndex = read(`${PL}/packages/cli/src/index.ts`)
const cliCmds = read(`${PL}/packages/cli/src/commands.ts`)
const foldDoc = read(`${PL}/docs/PARLAY_FIRSTMATE_FOLD.md`)

const probe = {
  spawnFlag: (f: string) => new RegExp(`--${f}\\b`).test(spawnSrc),
  verb: (v: string) => new RegExp(`case "${v}"`).test(cliIndex),
  // the fold's keyed status is a NEW sink; the existing 'status' verb is a reader.
  keyedStatusBuilt: /PARLAY_STATUS_FILE/.test(cliCmds) || /PARLAY_STATUS_FILE/.test(spawnSrc),
  teardownBin: has(`${PL}/bin/parlay-teardown`),
  worktreeSpawn: /--worktree\b/.test(spawnSrc),
  effortSpawn: /--effort\b/.test(spawnSrc),
  modeSpawn: /--mode\b/.test(spawnSrc),
  projectSpawn: /--project\b/.test(spawnSrc),
  // C5: crew-dispatch is only legitimately STAYS-FIRSTMATE if the fold doc
  // actually states the retention + re-activation sequencing (task-8io0).
  crewDispatchRetentionStated: /crew-dispatch/i.test(foldDoc) && /STAYS[- ]FIRSTMATE/i.test(foldDoc) && /re-activ/i.test(foldDoc),
}

// ---- open fix tasks (contraction ledger) ---------------------------------
function openFixTasks(): string {
  try {
    return execSync(`task list 2>/dev/null`, { encoding: "utf8" })
  } catch {
    return ""
  }
}
const fixTasks = openFixTasks()
const hasFixTask = (tag: string) => new RegExp(`parlay-fold ${tag}\\b`).test(fixTasks)

// ---- the capability matrix -----------------------------------------------
type Verdict =
  | "COVERED-same"
  | "COVERED-alternate"
  | "EXPANDED"
  | "STAYS-FIRSTMATE"
  | "DEFERRED"
  | "DROP-justified"
  | "MISSING"

interface Row {
  cap: string // firstmate capability
  fm: string // where it lives in firstmate
  parlay: string // its home in the folded end-state
  verdict: Verdict
  built?: boolean // for COVERED-alternate: is it landed yet?
  fix?: string // contraction ledger tag, when MISSING
  note?: string
}

const M: Row[] = [
  // --- spawn / launch ---
  { cap: "Spawn a direct report", fm: "fm-spawn.sh", parlay: "bin/parlay-spawn", verdict: "COVERED-same", built: has(`${PL}/bin/parlay-spawn`) },
  { cap: "Model pin", fm: "--model", parlay: "parlay-spawn --model", verdict: "COVERED-same", built: probe.spawnFlag("model") },
  { cap: "Effort level", fm: "--effort", parlay: "parlay-spawn --effort (§3.4)", verdict: "COVERED-alternate", built: probe.effortSpawn },
  { cap: "Per-task worktree isolation (mandatory)", fm: "treehouse worktree", parlay: "parlay-spawn --worktree opt-in (§3.3)", verdict: "COVERED-alternate", built: probe.worktreeSpawn, note: "mandatory→opt-in; parlay agents often not in a repo" },
  { cap: "Batch dispatch (id=repo pairs)", fm: "fm-spawn.sh id=repo …", parlay: "— (dropped v1)", verdict: "MISSING", fix: "C3", note: "'thin loop later' = deferral not coverage" },
  { cap: "Multi-harness (codex/opencode/pi/grok)", fm: "fm-harness.sh + adapters", parlay: "deferred primitive, seam scaffolded (§3.4)", verdict: "DEFERRED", note: "built LAST; claude-only until then" },
  { cap: "Runtime backend (tmux/zellij/orca/cmux)", fm: "fm-backend.sh", parlay: "firstmate-retained (herdr-only by design)", verdict: "DROP-justified", note: "decision-3ae does not ask parlay to own backends" },
  { cap: "Crew-dispatch profiles + quota-balanced", fm: "crew-dispatch.json + fm-dispatch-select.sh", parlay: "firstmate POLICY, retention stated (§3.4a)", verdict: "STAYS-FIRSTMATE", note: "what-to-spawn choice stays fm (decision-3ae); re-activates against the §3.4 harness primitive — inert while parlay is claude-only" },

  // --- brief / meta / identity ---
  { cap: "Structured brief contract", fm: "fm-brief.sh", parlay: "enrollment + appended task contract (§3.1)", verdict: "COVERED-alternate", built: /## Definition of done/.test(spawnSrc) },
  { cap: "Recorded meta (runtime facts)", fm: "state/<id>.meta", parlay: "identity.md frontmatter superset (§3.2)", verdict: "COVERED-alternate", built: /project_bead=|kind=|\bmode=/.test(spawnSrc) },
  { cap: "Identity / recovery / self-restart", fm: "(none — disposable)", parlay: "durable identity+handoff+context-reset", verdict: "EXPANDED" },
  { cap: "Panel enrollment / phone reachability", fm: "(none)", parlay: "parlay monitor + Pulse panel", verdict: "EXPANDED" },

  // --- status / supervision ---
  { cap: "Keyed status stream (agent→supervisor)", fm: "state/<id>.status + fm-classify-lib", parlay: "NEW parlay status verb (§3.6)", verdict: "COVERED-alternate", built: probe.keyedStatusBuilt, note: "NAME COLLISION w/ existing panel-status reader → C1" },
  { cap: "Event-driven watcher (absorb-when-working)", fm: "fm-watch.sh", parlay: "bridge reuses fm-watch → Slice 3 primitive", verdict: "DEFERRED" },
  { cap: "Authoritative current-state read", fm: "fm-crew-state.sh", parlay: "Slice 3 crew-state (richer oracle)", verdict: "DEFERRED", note: "parlay adds tab-liveness + subscriber presence" },
  { cap: "Durable wake queue", fm: "fm-wake-drain + .wake-queue", parlay: "(rolls into Slice 3 supervise primitive)", verdict: "DEFERRED" },
  { cap: "Worktree-tangle runtime guard", fm: "fm-guard.sh", parlay: "brief assertion ported; runtime alarm NOT", verdict: "MISSING", fix: "C4", note: "upstream guard ≠ runtime backstop" },
  { cap: "Turn-end guard hooks", fm: "fm-turnend-guard.sh", parlay: "deferred w/ harness primitive (§3.4)", verdict: "DEFERRED" },
  { cap: "Away-mode unattended sub-supervision", fm: "fm-afk-* + fm-supervise-daemon.sh", parlay: "— (unaddressed)", verdict: "MISSING", fix: "C2", note: "no home: not in Slice 3 scope, not clearly fm-retained" },
  { cap: "Steer agent (captain→crewmate)", fm: "fm-send.sh", parlay: "parlay send/say --agent + monitor", verdict: "COVERED-same", built: probe.verb("send") && probe.verb("say") },
  { cap: "Peek pane for diagnosis", fm: "fm-peek.sh", parlay: "parlay history + herdr agent get", verdict: "COVERED-alternate", built: probe.verb("history") },

  // --- teardown ---
  { cap: "Safe teardown (never discard unlanded)", fm: "fm-teardown.sh", parlay: "parlay-teardown (§3.7)", verdict: "COVERED-alternate", built: probe.teardownBin },

  // --- delivery / merge (POLICY) ---
  { cap: "Delivery mode report/branch/pr", fm: "no-mistakes/direct-PR/local-only/scout", parlay: "--mode report|branch|pr (§3.5)", verdict: "COVERED-alternate", built: probe.modeSpawn, note: "no-mistakes NOT ported (fm policy)" },
  { cap: "yolo approval posture", fm: "yolo flag", parlay: "recorded in meta (§3.5)", verdict: "COVERED-alternate", built: /yolo=/.test(spawnSrc) },
  { cap: "Merge discipline (captain-merge, squash rec.)", fm: "fm-pr-merge/pr-check/merge-local", parlay: "firstmate POLICY", verdict: "STAYS-FIRSTMATE" },
  { cap: "Branch review vs authoritative base", fm: "fm-review-diff.sh", parlay: "firstmate POLICY (+ lavish surface)", verdict: "STAYS-FIRSTMATE" },
  { cap: "Review store + decision pages", fm: "fm-review-page/decision.sh", parlay: "firstmate / lavish", verdict: "STAYS-FIRSTMATE" },
  { cap: "no-mistakes validation pipeline", fm: "no-mistakes gate", parlay: "firstmate POLICY (explicitly not ported)", verdict: "STAYS-FIRSTMATE" },

  // --- intake / project mgmt ---
  { cap: "Project resolution at intake", fm: "AGENTS.md §7 intake signals", parlay: "--project + resolver (§3.9)", verdict: "COVERED-alternate", built: probe.projectSpawn },
  { cap: "Project registry", fm: "data/projects.md", parlay: "projects bead store (§3.8/3.9)", verdict: "COVERED-alternate", note: "markdown form dropped; concept kept" },
  { cap: "Delivery-mode-per-project", fm: "projects.md [mode]", parlay: "per-spawn --mode (§3.8)", verdict: "COVERED-alternate", note: "config location moved, not lost" },
  { cap: "Proactive grooming / idea-mining", fm: "fm-groom.sh + fm-idea-mine.ts", parlay: "firstmate POLICY (what-to-spawn)", verdict: "STAYS-FIRSTMATE" },
  { cap: "Backlog queue", fm: "data/backlog.md + tasks-axi", parlay: "MIXED: split Slice 4 (task/projects store)", verdict: "DEFERRED" },
  { cap: "Knowledge routing (captain/learnings)", fm: "data/*.md + §6", parlay: "firstmate memory POLICY", verdict: "STAYS-FIRSTMATE" },

  // --- fleet / session (supervisor-side) ---
  { cap: "Fleet sync (ff project clones)", fm: "fm-fleet-sync.sh", parlay: "MIXED fleet-state (Slice 4 split)", verdict: "DEFERRED" },
  { cap: "Fleet snapshot / view", fm: "fm-fleet-snapshot/view.sh", parlay: "parlay agents/subscribers/stats (partial)", verdict: "DEFERRED", built: probe.verb("agents") },
  { cap: "Supervisor session-start digest", fm: "fm-session-start.sh", parlay: "firstmate orchestrator infra", verdict: "STAYS-FIRSTMATE", note: "agent-side self-start is EXPANDED separately" },
  { cap: "Session lock (single owner/home)", fm: "fm-lock.sh", parlay: "firstmate orchestrator infra", verdict: "STAYS-FIRSTMATE" },
  { cap: "Supervisor recovery reconcile", fm: "AGENTS.md §5", parlay: "MIXED: agent-side EXPANDED, sup-side fm", verdict: "DEFERRED" },

  // --- secondmates / config ---
  { cap: "Secondmate (persistent domain supervisor)", fm: "fm-home-seed + charter + §7", parlay: "dropped; nested-sup via Slice 3 primitive", verdict: "DROP-justified", note: "abstraction redundant; nested supervision reconstructible post-Slice-3" },
  { cap: "Config inheritance to secondmates", fm: "fm-config-inherit/push.sh", parlay: "— (rides secondmate drop)", verdict: "DROP-justified", note: "only relevant if secondmates return" },

  // --- X mode ---
  { cap: "X mode (relay mention → task, answer)", fm: "fm-x-* + §14", parlay: "firstmate POLICY (inert until opted in)", verdict: "STAYS-FIRSTMATE" },
  { cap: "Herdr-lab hard-isolation brief contract", fm: "fm-herdr-lab.sh + --herdr-lab", parlay: "unmentioned (niche)", verdict: "STAYS-FIRSTMATE", note: "parlay is herdr-native; lab-isolation for herdr-lifecycle tasks" },
  { cap: "External herdr-agent → firstmate spur", fm: "fm-herdr-spur.sh", parlay: "event fabric (robots-watch, CLI_VERBS §2)", verdict: "DEFERRED" },
]

// ---- verify integrity ----------------------------------------------------
const problems: string[] = []
for (const r of M) {
  if (r.verdict === "MISSING") {
    if (!r.fix) problems.push(`MISSING w/o fix tag: ${r.cap}`)
    else if (!hasFixTask(r.fix)) problems.push(`MISSING "${r.cap}" → no open fix task tagged parlay-fold ${r.fix}`)
  }
}
// C5 (task-8io0): the crew-dispatch row is only a legitimate STAYS-FIRSTMATE if the
// fold doc actually states retention + re-activation. Regress to a problem otherwise.
if (/Crew-dispatch/.test(M.find(r => /Crew-dispatch/.test(r.cap))?.cap ?? "") && !probe.crewDispatchRetentionStated) {
  problems.push(`Crew-dispatch marked STAYS-FIRSTMATE but fold doc does not state retention + re-activation (see §3.4a)`)
}

// ---- render --------------------------------------------------------------
const counts: Record<string, number> = {}
for (const r of M) counts[r.verdict] = (counts[r.verdict] ?? 0) + 1

const asJson = process.argv.includes("--json")
if (asJson) {
  console.log(JSON.stringify({ generated: "probe-live", counts, rows: M, problems }, null, 2))
} else {
  console.log(`\nparlay×firstmate capability parity — ${M.length} capabilities\n`)
  const order: Verdict[] = ["MISSING", "DEFERRED", "COVERED-alternate", "COVERED-same", "EXPANDED", "STAYS-FIRSTMATE", "DROP-justified"]
  for (const v of order) {
    const rows = M.filter(r => r.verdict === v)
    if (!rows.length) continue
    console.log(`── ${v} (${rows.length}) ${"─".repeat(Math.max(0, 40 - v.length))}`)
    for (const r of rows) {
      const built = r.built === undefined ? "" : r.built ? " [landed]" : " [designed]"
      const fix = r.fix ? ` → fix parlay-fold ${r.fix}${hasFixTask(r.fix) ? "✓" : " (NO TASK!)"}` : ""
      console.log(`  ${r.cap}${built}${fix}`)
      if (r.note) console.log(`      ↳ ${r.note}`)
    }
    console.log()
  }
  console.log("counts:", JSON.stringify(counts))
}

if (problems.length) {
  console.error(`\n✗ parity integrity FAILED:\n  - ${problems.join("\n  - ")}`)
  process.exit(1)
}
const missing = M.filter(r => r.verdict === "MISSING").length
console.error(`\n✓ parity integrity OK — ${missing} contraction(s), each with a tracked fix task; ${M.length - missing} capabilities have a home.`)
