#!/usr/bin/env bun
/**
 * parity-audit-firstmate.ts — re-runnable capability-parity check (task-4bad).
 *
 * Checks the parlay×firstmate fold's EXPANSION-ONLY claim over the capabilities it can evaluate:
 * whether each has a home in the folded end-state (parlay primitive OR retained firstmate policy
 * OR justified drop), flagging those that DON'T; a fold-doc-dependent row is NOT EVALUATED, not passed.
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

// Default to the checkout this tool lives in (repo root = tools/..) so the audit probes
// its own source of truth — canonical run and worktree/CI alike. Override with PL_REPO.
const PL = process.env.PL_REPO ?? `${import.meta.dir}/..`

const read = (p: string) => (existsSync(p) ? readFileSync(p, "utf8") : "")
const has = (p: string) => existsSync(p)

// ---- live probes of the parlay CLI surface -------------------------------
const spawnSrc = read(`${PL}/bin/parlay-spawn`)
const cliIndex = read(`${PL}/packages/cli/src/index.ts`)
const cliStatusSrc = read(`${PL}/packages/cli/src/commands-status.ts`)
const cliSuperviseSrc = read(`${PL}/packages/cli/src/commands-supervise.ts`)
// The fold design doc is captain-private and no longer lives in this repo. Point
// PARLAY_FOLD_DOC at a copy to re-enable the two doc-derived probes below
// (`crewDispatchRetentionStated`, `afkHomeInFold`); without it BOTH their rows are
// reported once as NOT EVALUATED — never a MISSING contraction, never verified. Unset
// is supported; set-but-unreadable is a mistake, so say so loudly.
const foldDocPath = process.env.PARLAY_FOLD_DOC
const foldDoc = foldDocPath ? read(foldDocPath) : ""
const foldDocAvailable = foldDoc.length > 0
if (foldDocPath && !foldDocAvailable) console.error(`⚠ PARLAY_FOLD_DOC="${foldDocPath}" is unreadable or empty — its probes are NOT EVALUATED.`)

const probe = {
  spawnFlag: (f: string) => new RegExp(`--${f}\\b`).test(spawnSrc),
  verb: (v: string) => new RegExp(`case "${v}"`).test(cliIndex),
  // Keyed status sink: check commands-status.ts (not commands.ts — the old panel-status
  // reader). Landed = $PARLAY_STATUS_FILE sink there AND the verb wired in index.ts.
  keyedStatusBuilt:
    /PARLAY_STATUS_FILE/.test(cliStatusSrc) && /case "status"/.test(cliIndex),
  // Teardown: landed as a CLI verb, not a standalone bin (fold §3.7 chose CLI).
  teardownBuilt:
    /case "teardown"/.test(cliIndex) &&
    has(`${PL}/packages/cli/src/commands-teardown.ts`),
  worktreeSpawn: /--worktree\b/.test(spawnSrc),
  effortSpawn: /--effort\b/.test(spawnSrc),
  modeSpawn: /--mode\b/.test(spawnSrc),
  projectSpawn: /--project\b/.test(spawnSrc),
  // C3: batch id=repo dispatch is landed when the thin-loop block and its per-pair
  // failure report are both present in parlay-spawn.
  batchSpawn: /Batch dispatch \(thin loop\)/.test(spawnSrc) && /batch: FAILED to spawn/.test(spawnSrc),
  // C4: the runtime worktree-tangle guard ported from fm-guard. Landed = the
  // `parlay guard` verb dispatches AND the variant lifecycle calls guardRepo().
  tangleGuardBuilt:
    /case "guard"/.test(cliIndex) &&
    /export function guardRepo/.test(read(`${PL}/packages/cli/src/commands-guard.ts`)) &&
    /guardRepo\(/.test(read(`${PL}/packages/cli/src/commands-variant.ts`)),
  // C5: crew-dispatch is only legitimately STAYS-FIRSTMATE if the fold doc
  // actually states the retention + re-activation sequencing (task-8io0).
  crewDispatchRetentionStated: /crew-dispatch/i.test(foldDoc) && /STAYS[- ]FIRSTMATE/i.test(foldDoc) && /re-activ/i.test(foldDoc),
}

// C2 (away-mode): does the fold doc give unattended sub-supervision a home (§3.6.2)?
const afkHomeInFold = /3\.6\.2 Unattended/.test(foldDoc)
// Slice 3 unattended/headless supervise mode: check commands-supervise.ts (not
// commands.ts, which doesn't import it). Landed = `supervise` verb wired + the
// PARLAY_UNATTENDED_FLAG presence gate exists in the supervise module.
const afkUnattendedBuilt =
  probe.verb("supervise") && /PARLAY_UNATTENDED_FLAG|isUnattended/.test(cliSuperviseSrc)

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
  docDerived?: "crew-dispatch" | "away-mode" // stable key; verdict sourced from the fold doc: unreadable ⇒ NOT EVALUATED, never MISSING
}

const M: Row[] = [
  // --- spawn / launch ---
  { cap: "Spawn a direct report", fm: "fm-spawn.sh", parlay: "bin/parlay-spawn", verdict: "COVERED-same", built: has(`${PL}/bin/parlay-spawn`) },
  { cap: "Model pin", fm: "--model", parlay: "parlay-spawn --model", verdict: "COVERED-same", built: probe.spawnFlag("model") },
  { cap: "Effort level", fm: "--effort", parlay: "parlay-spawn --effort (§3.4)", verdict: "COVERED-alternate", built: probe.effortSpawn },
  { cap: "Per-task worktree isolation (mandatory)", fm: "treehouse worktree", parlay: "parlay-spawn --worktree opt-in (§3.3)", verdict: "COVERED-alternate", built: probe.worktreeSpawn, note: "mandatory→opt-in; parlay agents often not in a repo" },
  { cap: "Batch dispatch (id=repo pairs)", fm: "fm-spawn.sh id=repo …", parlay: "parlay-spawn id=repo … --prompt (thin loop, §3.8)", verdict: "COVERED-alternate", built: probe.batchSpawn, note: "C3 RESOLVED (task-ovkq): thin loop re-execs single mode per pair; shared --prompt/--model/--color, name+color derived per id; one failed pair does not stop the rest, batch exits non-zero" },
  { cap: "Multi-harness (codex/opencode/pi/grok)", fm: "fm-harness.sh + adapters", parlay: "deferred primitive, seam scaffolded (§3.4)", verdict: "DEFERRED", note: "built LAST; claude-only until then" },
  { cap: "Runtime backend (tmux/zellij/orca/cmux)", fm: "fm-backend.sh", parlay: "firstmate-retained (herdr-only by design)", verdict: "DROP-justified", note: "decision-3ae does not ask parlay to own backends" },
  { cap: "Crew-dispatch profiles + quota-balanced", docDerived: "crew-dispatch", fm: "crew-dispatch.json + fm-dispatch-select.sh", parlay: foldDocAvailable ? "firstmate POLICY, retention stated (§3.4a)" : "— (retention claim not checkable here)", verdict: "STAYS-FIRSTMATE", note: foldDocAvailable ? "what-to-spawn choice stays fm (decision-3ae); re-activates against the §3.4 harness primitive — inert while parlay is claude-only" : "the retention + re-activation statement was sourced from the fold design doc, which is no longer in this repo, so this audit cannot check the claim" },

  // --- brief / meta / identity ---
  { cap: "Structured brief contract", fm: "fm-brief.sh", parlay: "enrollment + appended task contract (§3.1)", verdict: "COVERED-alternate", built: /## Definition of done/.test(spawnSrc) },
  // Recorded meta: parlay-spawn passes --mode/--yolo to identity --register (YAML frontmatter,
  // not firstmate's key=value .meta format). Probe: --mode/--yolo present in spawn.
  // BLIND SPOT: --mode/--yolo are all this checks. The rest of the meta superset the row
  // claims (--project_bead, --worktree recording) is designed-not-built and unprobed here.
  { cap: "Recorded meta (runtime facts)", fm: "state/<id>.meta", parlay: "identity.md frontmatter superset (§3.2)", verdict: "COVERED-alternate", built: /--mode\b/.test(spawnSrc) && /--yolo\b/.test(spawnSrc) },
  { cap: "Identity / recovery / self-restart", fm: "(none — disposable)", parlay: "durable identity+handoff+context-reset", verdict: "EXPANDED" },
  { cap: "Panel enrollment / phone reachability", fm: "(none)", parlay: "parlay monitor + Pulse panel", verdict: "EXPANDED" },

  // --- status / supervision ---
  { cap: "Keyed status stream (agent→supervisor)", fm: "state/<id>.status + fm-classify-lib", parlay: "parlay status verb (§3.6) — C1 collision resolved (task-ve2v)", verdict: "COVERED-alternate", built: probe.keyedStatusBuilt, note: "C1 RESOLVED: 'parlay status' now the fold §3.6 verb; old alias retired" },
  { cap: "Event-driven watcher (absorb-when-working)", fm: "fm-watch.sh", parlay: "bridge reuses fm-watch → Slice 3 primitive", verdict: "DEFERRED" },
  { cap: "Authoritative current-state read", fm: "fm-crew-state.sh", parlay: "Slice 3 crew-state (richer oracle)", verdict: "DEFERRED", note: "parlay adds tab-liveness + subscriber presence" },
  { cap: "Durable wake queue", fm: "fm-wake-drain + .wake-queue", parlay: "(rolls into Slice 3 supervise primitive)", verdict: "DEFERRED" },
  { cap: "Worktree-tangle runtime guard", fm: "fm-guard.sh", parlay: "parlay guard — tangle+liveness backstop, wired into variant lifecycle (C4)", verdict: "COVERED-alternate", built: probe.tangleGuardBuilt, note: "runtime banner + non-destructive restore + --beat liveness beacon; anchored on the variant worktree primitive" },
  { cap: "Turn-end guard hooks", fm: "fm-turnend-guard.sh", parlay: "deferred w/ harness primitive (§3.4)", verdict: "DEFERRED" },
  // C2 (task-eg75): away-mode home is DERIVED from the fold doc, so a doc supplied via
  // PARLAY_FOLD_DOC that has lost §3.6.2 reverts this row to MISSING and re-fails
  // integrity — genuine re-verification. No doc at all is NOT EVALUATED, not MISSING.
  { cap: "Away-mode unattended sub-supervision", docDerived: "away-mode", fm: "fm-afk-* + fm-supervise-daemon.sh", parlay: afkHomeInFold ? "Slice 3 supervise: unattended mode (§3.6.2) + fm-afk policy" : "— (unaddressed)", verdict: afkHomeInFold ? "COVERED-alternate" : "MISSING", fix: afkHomeInFold ? undefined : "C2", built: afkUnattendedBuilt, note: afkHomeInFold ? "mechanism→Slice 3 headless mode (presence flag + batched escalation + max-defer + in-band captain-return marker); policy→firstmate (/afk gesture, max-defer value, approval-authority preservation)" : foldDocAvailable ? "no home: not in Slice 3 scope, not clearly fm-retained" : "the §3.6.2 home for this capability was stated only in the fold design doc, which is no longer in this repo, so this audit cannot check the claim" },
  { cap: "Steer agent (captain→crewmate)", fm: "fm-send.sh", parlay: "parlay send/say --agent + monitor", verdict: "COVERED-same", built: probe.verb("send") && probe.verb("say") },
  { cap: "Peek pane for diagnosis", fm: "fm-peek.sh", parlay: "parlay history + herdr agent get", verdict: "COVERED-alternate", built: probe.verb("history") },

  // --- teardown ---
  { cap: "Safe teardown (never discard unlanded)", fm: "fm-teardown.sh", parlay: "parlay teardown CLI verb (§3.7) — unlanded-work gate, worktree+store cleanup", verdict: "COVERED-alternate", built: probe.teardownBuilt },

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
const docDerivedCaps = M.filter(r => r.docDerived).map(r => r.cap)
const notEvaluated = foldDocAvailable ? [] : docDerivedCaps
for (const r of M) {
  if (notEvaluated.includes(r.cap)) continue
  if (r.verdict === "MISSING") {
    if (!r.fix) problems.push(`MISSING w/o fix tag: ${r.cap}`)
    else if (!hasFixTask(r.fix)) problems.push(`MISSING "${r.cap}" → no open fix task tagged parlay-fold ${r.fix}`)
  }
}
// C5 (task-8io0): the crew-dispatch row is only a legitimate STAYS-FIRSTMATE if the
// fold doc actually states retention + re-activation — assertable only when the doc
// is readable; otherwise it is NOT EVALUATED (above), not silently verified. The row
// is found by its stable docDerived key, and losing it is itself a problem here.
const crewRow = M.find(r => r.docDerived === "crew-dispatch")
if (!crewRow) problems.push(`C5 guard could not locate the row it asserts on — no row carries docDerived "crew-dispatch"`)
else if (foldDocAvailable && crewRow.verdict === "STAYS-FIRSTMATE" && !probe.crewDispatchRetentionStated)
  problems.push(`Crew-dispatch marked STAYS-FIRSTMATE but fold doc does not state retention + re-activation (see §3.4a)`)

// ---- render --------------------------------------------------------------
// Unevaluated rows are reported once under NOT EVALUATED — in BOTH output modes —
// never counted or listed under a verdict they were never assessed against.
const rendered = M.filter(r => !notEvaluated.includes(r.cap))
const unevaluated = M.filter(r => notEvaluated.includes(r.cap)).map(r => ({ cap: r.cap, fm: r.fm, verdict: "NOT-EVALUATED", note: r.note }))
const counts: Record<string, number> = {}
for (const r of rendered) counts[r.verdict] = (counts[r.verdict] ?? 0) + 1

const asJson = process.argv.includes("--json")
if (asJson) {
  console.log(JSON.stringify({ generated: "probe-live", counts, rows: rendered, problems, notEvaluated: unevaluated, docDerived: docDerivedCaps }, null, 2))
} else {
  console.log(`\nparlay×firstmate capability parity — ${M.length} capabilities (${rendered.length} evaluated)\n`)
  if (unevaluated.length) {
    console.log(`NOT EVALUATED (fold doc unavailable — set PARLAY_FOLD_DOC to a copy):`)
    for (const r of unevaluated) console.log(`  ${r.cap}${r.note ? `\n      ↳ ${r.note}` : ""}`)
    console.log()
  }
  const order: Verdict[] = ["MISSING", "DEFERRED", "COVERED-alternate", "COVERED-same", "EXPANDED", "STAYS-FIRSTMATE", "DROP-justified"]
  for (const v of order) {
    const rows = rendered.filter(r => r.verdict === v)
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
const missing = rendered.filter(r => r.verdict === "MISSING").length
const held = unevaluated.length ? `; ${unevaluated.length} NOT EVALUATED (set PARLAY_FOLD_DOC)` : ""
console.error(`\n✓ parity integrity OK — ${missing} contraction(s), each with a tracked fix task; ${rendered.length - missing} of ${rendered.length} evaluated capabilities have a home${held}.`)
