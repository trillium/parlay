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
import { buildMatrix, type Row } from "./parity-audit-matrix"

// Default to the checkout this tool lives in (repo root = tools/..) so the audit probes
// its own source of truth — canonical run and worktree/CI alike. Override with PL_REPO.
const PL = process.env.PL_REPO ?? `${import.meta.dir}/..`

const read = (p: string) => (existsSync(p) ? readFileSync(p, "utf8") : "")
const has = (p: string) => existsSync(p)

// ---- live probes of the parlay CLI surface -------------------------------
//
// These read the GO CLI (tools/cli). They used to read packages/cli, which was
// retired in ticket T-08 — and because `read()` returns "" for a missing file
// rather than failing, every probe silently went false and six landed
// capabilities were re-reported as merely `designed`. An audit whose job is to
// catch contractions must not quietly invent them, so `mustRead` refuses to
// probe a file that is not there.
const mustRead = (p: string) => {
  const src = read(p)
  if (!src) {
    console.error(`✗ parity probe source missing or empty: ${p}`)
    console.error(`  This audit reads the live CLI to decide landed-vs-designed. A missing`)
    console.error(`  probe source silently downgrades real capabilities, so refusing to run.`)
    process.exit(2)
  }
  return src
}

const spawnSrc = mustRead(`${PL}/tools/cli/internal/spawn/spawn.go`) // task-42qot: bin/parlay-spawn is deleted; the flag surface lives here
const cliIndex = mustRead(`${PL}/tools/cli/main.go`)
const cliStatusSrc = mustRead(`${PL}/tools/cli/internal/commands/status_verb.go`)
const cliSuperviseSrc = mustRead(`${PL}/tools/cli/internal/commands/supervise.go`)
// The fold design doc is captain-private and no longer lives in this repo. Point
// PARLAY_FOLD_DOC at a copy to re-enable the two doc-derived probes below
// (`crewDispatchRetentionStated`, `afkHomeInFold`); without it BOTH their rows are
// reported once as NOT EVALUATED — never a MISSING contraction, never verified. Unset
// is supported; set-but-unreadable is a mistake, so say so loudly.
const foldDocPath = process.env.PARLAY_FOLD_DOC
const foldDoc = foldDocPath ? (() => { try { return read(foldDocPath) } catch { return "" } })() : "" // read() only guards a missing path; EISDIR/EACCES must reach the warning below
const foldDocAvailable = foldDoc.length > 0
if (foldDocPath && !foldDocAvailable) console.error(`⚠ PARLAY_FOLD_DOC="${foldDocPath}" is unreadable or empty — its probes are NOT EVALUATED.`)

const probe = {
  spawnFlag: (f: string) => new RegExp(`--${f}\\b`).test(spawnSrc),
  verb: (v: string) => new RegExp(`case "${v}"`).test(cliIndex),
  // Keyed status sink: check the status verb's own file (not the panel-status
  // reader). Landed = $PARLAY_STATUS_FILE sink there AND the verb wired in the
  // dispatch switch.
  keyedStatusBuilt:
    /PARLAY_STATUS_FILE/.test(cliStatusSrc) && /case "status"/.test(cliIndex),
  // Teardown: landed as a CLI verb, not a standalone bin (fold §3.7 chose CLI).
  teardownBuilt:
    /case "teardown"/.test(cliIndex) &&
    has(`${PL}/tools/cli/internal/commands/teardown.go`),
  worktreeSpawn: /--worktree\b/.test(spawnSrc),
  effortSpawn: /--effort\b/.test(spawnSrc),
  modeSpawn: /--mode\b/.test(spawnSrc),
  projectSpawn: /--project\b/.test(spawnSrc),
  // C3: batch id=repo dispatch is landed when the batch entry point and its
  // per-pair failure report are both present in the spawn package.
  batchSpawn: /func runBatchSpawn\(/.test(spawnSrc) && /batch: FAILED to spawn/.test(spawnSrc),
  // C4: the runtime worktree-tangle guard ported from fm-guard. Landed = the
  // `parlay guard` verb dispatches AND the variant lifecycle calls guardRepo().
  tangleGuardBuilt:
    /case "guard"/.test(cliIndex) &&
    /func guardRepo\(/.test(mustRead(`${PL}/tools/cli/internal/commands/guard.go`)) &&
    /guardRepo\(/.test(mustRead(`${PL}/tools/cli/internal/commands/variant.go`)),
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

const M: Row[] = buildMatrix({ probe, foldDocAvailable, afkHomeInFold, afkUnattendedBuilt, has, PL, spawnSrc })

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
