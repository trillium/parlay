#!/usr/bin/env bun
/**
 * parity-audit-page.ts — render the task-4bad parity matrix as a mobile-first
 * Pulse page from the SAME data the check derives. Keeps page ⇄ check in sync.
 *
 *   bun tools/parity-audit-page.ts > ~/pulse-pages/parity-audit/index.html
 */
import { execSync } from "node:child_process"

const raw = execSync(`bun ${import.meta.dir}/parity-audit-firstmate.ts --json`, { encoding: "utf8" })
const data = JSON.parse(raw) as {
  counts: Record<string, number>
  rows: Array<{ cap: string; fm: string; parlay: string; verdict: string; built?: boolean; fix?: string; note?: string }>
  problems: string[]
  notEvaluated?: Array<{ cap: string; fm: string; verdict: string; note?: string }>
  docDerived?: string[]
}

const VERDICT_META: Record<string, { label: string; cls: string; blurb: string }> = {
  MISSING: { label: "MISSING", cls: "v-missing", blurb: "Contraction — no home in the folded end-state. Fix task filed." },
  DEFERRED: { label: "DEFERRED", cls: "v-deferred", blurb: "Mechanism to migrate; explicitly built later with a scaffolded seam." },
  "COVERED-alternate": { label: "COVERED · alt", cls: "v-alt", blurb: "Ported in parlay's idiom (some designed, not yet built)." },
  "COVERED-same": { label: "COVERED · same", cls: "v-same", blurb: "Parlay already has an equal primitive." },
  EXPANDED: { label: "EXPANDED", cls: "v-expanded", blurb: "Parlay adds capability beyond firstmate." },
  "STAYS-FIRSTMATE": { label: "STAYS · firstmate", cls: "v-stays", blurb: "Policy retained firstmate-side — not a contraction." },
  "DROP-justified": { label: "DROP · justified", cls: "v-drop", blurb: "Out of scope; justification recorded and sound." },
}
const ORDER = ["MISSING", "DEFERRED", "COVERED-alternate", "COVERED-same", "EXPANDED", "STAYS-FIRSTMATE", "DROP-justified"]
const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")

const notEvaluated = data.notEvaluated ?? []
const evaluated = data.rows.length
const total = evaluated + notEvaluated.length
const missing = data.counts["MISSING"] ?? 0
const haveHome = evaluated - missing
const contractions = data.rows.filter(r => r.verdict === "MISSING")

const chips = ORDER.filter(v => data.counts[v])
  .map(v => `<span class="chip ${VERDICT_META[v].cls}">${VERDICT_META[v].label.split(" ")[0]} ${data.counts[v]}</span>`)
  .join("") + (notEvaluated.length ? `<span class="chip v-uneval">NOT EVALUATED ${notEvaluated.length}</span>` : "")

const groups = ORDER.filter(v => data.rows.some(r => r.verdict === v)).map(v => {
  const meta = VERDICT_META[v]
  const rows = data.rows.filter(r => r.verdict === v)
  const cards = rows.map(r => {
    const built = r.built === undefined ? "" : r.built ? `<span class="tag landed">landed</span>` : `<span class="tag designed">designed</span>`
    const fix = r.fix ? `<a class="tag fix" href="#c-${r.fix}">fix ${r.fix}</a>` : ""
    return `<div class="cap">
      <div class="cap-h"><span class="cap-name">${esc(r.cap)}</span>${built}${fix}</div>
      <div class="cap-map"><code>${esc(r.fm)}</code><span class="arr">→</span><code>${esc(r.parlay)}</code></div>
      ${r.note ? `<div class="cap-note">${esc(r.note)}</div>` : ""}
    </div>`
  }).join("")
  return `<section class="grp">
    <button class="grp-h ${meta.cls}" onclick="this.parentElement.classList.toggle('closed')">
      <span class="grp-title">${meta.label}</span><span class="grp-count">${rows.length}</span>
    </button>
    <div class="grp-body"><p class="grp-blurb">${meta.blurb}</p>${cards}</div>
  </section>`
}).join("")

const contractionList = contractions.map(r => {
  return `<li id="c-${r.fix}"><span class="c-tag">${r.fix}</span> <strong>${esc(r.cap)}</strong>
    <div class="c-note">${esc(r.note ?? "")}</div>
    <div class="c-map"><code>${esc(r.fm)}</code> → <code>${esc(r.parlay)}</code></div>
    <div class="c-fix">Filed: <code>parlay-fold ${r.fix}</code> in the <code>task</code> store</div></li>`
}).join("")

// Design blockers are notes on COVERED rows, not MISSING rows of their own, so the
// probe carries no row of its own for them. Whether one is still OPEN is derived from
// the same rows the matrix renders — a row's note declaring "<id> RESOLVED" retires it
// everywhere at once, so the verdict prose below cannot disagree with the matrix.
const BLOCKERS = [{
  id: "C1",
  title: "'parlay status' verb name collision",
  note: `The fold's proposed keyed <code>parlay status &lt;verb&gt;</code> (§3.6) collides with parlay's EXISTING <code>status</code> verb — a panel/fleet reader (<code>commands.ts:27 cmdStatus</code>). Unacknowledged in the fold. Design blocker before Slice 1.`,
}]
const resolvedInData = new Set(
  data.rows.flatMap(r => [...(r.note ?? "").matchAll(/\b(C\d+)\s+RESOLVED\b/g)].map(m => m[1]))
)
const openBlockers = BLOCKERS.filter(b => !resolvedInData.has(b.id))
const blockerList = openBlockers.map(b => `<li id="c-${b.id}"><span class="c-tag block">${b.id}</span> <strong>${esc(b.title)}</strong>
  <div class="c-note">${b.note}</div>
  <div class="c-fix">Filed: <code>parlay-fold ${b.id}</code> (P1) in the <code>task</code> store</div></li>`).join("")

// Both verdict claims below are derived, never asserted: "design-only" from how many
// probe-checked rows report landed, and each gap clause from an open blocker.
const landedRows = data.rows.filter(r => r.built === true).length
const probedRows = data.rows.filter(r => r.built !== undefined).length
const foldStatus = landedRows === 0
  ? "The fold is design-only (no code landed yet)."
  : `The fold is partly built: ${landedRows} of ${probedRows} probe-checked rows are landed, the rest design-only.`
const gaps = [
  ...(missing ? [`<b>${missing} fall through</b> with no home.`] : []),
  ...(openBlockers.some(b => b.id === "C1") ? [`The proposed <code>parlay status</code> verb <b>collides</b> with an existing one.`] : []),
]
const gapProse = gaps.length
  ? `${gaps.join(" ")} Each is filed as a fix task below — resolve before the fold lands to keep the "expansion-only" claim true.`
  : `Nothing falls through without a home, and no design blocker is still open.`
// Both headline branches state a measurement, and a measurement may not claim more
// rows than it covered: when any row went unevaluated, the headline carries its own
// scope, for the reader who stops there and never reaches the paragraph below.
const headlineScope = evaluated === total ? "" : ` among the ${evaluated} of ${total} rows evaluated`
const headlineGaps = missing + openBlockers.length
  ? ` — with ${missing} genuine contraction(s) + ${openBlockers.length} design blocker(s) to fix first${headlineScope}`
  : ` — with no open contraction or design blocker${headlineScope}`
const contractionSection = missing + openBlockers.length
  ? `<h2>Contractions — fix before the fold lands</h2>
  <ol class="contractions">${blockerList}${contractionList}</ol>`
  : ""

// A dated manual check the auditor ran by hand, so it is reported inside the NOT
// EVALUATED block and never promoted into the verdict, the headline or any count —
// it is not derived from this run. Which rows it can speak for comes from the
// audit's own `docDerived` flag (the same source that decides what goes
// unevaluated), not a second list of cap labels here: the note renders only when
// the unevaluated set is exactly that flagged set AND every row in it has a
// hand-checked verdict recorded below, so a new doc-derived row retires the note
// rather than silently extending it to a row nobody checked.
const MANUAL_VERDICTS: Record<string, string> = {
  "Away-mode unattended sub-supervision": "COVERED-alternate, landed",
  "Crew-dispatch profiles + quota-balanced": "STAYS-FIRSTMATE",
}
const docDerived = data.docDerived ?? []
const manualCovers =
  docDerived.length > 0 && notEvaluated.length === docDerived.length &&
  docDerived.every(c => notEvaluated.some(r => r.cap === c) && MANUAL_VERDICTS[c])
const manualNote = manualCovers
  ? `<p class="manual"><b>Auditor's manual check, 2026-08-14</b> — a one-time snapshot taken by hand on that date, which this tool does not re-verify on later runs; not derived from this run and not in the counts: with a copy of the fold doc supplied out-of-band, ${docDerived.length === 1 ? "the row above did" : "the rows above did"} evaluate and none came back MISSING (${docDerived.map(c => `<b>${esc(c)}</b> → ${esc(MANUAL_VERDICTS[c]!)}`).join("; ")}).</p>`
  : ""

const notEvalSection = notEvaluated.length ? `
  <h2>Not evaluated — ${notEvaluated.length} of ${total} capabilities</h2>
  <div class="notice">
    <p class="grp-blurb">These verdicts were derived from the fold design doc, which is not in this repo. The audit cannot check their claims, so they are reported here rather than shown as verified below. Point <code>PARLAY_FOLD_DOC</code> at a readable copy and re-run to evaluate them.</p>
    ${notEvaluated.map(r => `<div class="cap">
      <div class="cap-h"><span class="cap-name">${esc(r.cap)}</span><span class="tag uneval">not evaluated</span></div>
      <div class="cap-map"><code>${esc(r.fm)}</code></div>
      ${r.note ? `<div class="cap-note">${esc(r.note)}</div>` : ""}
    </div>`).join("")}
    ${manualNote}
  </div>` : ""

const html = `<title>parlay × firstmate — capability parity audit</title>
<style>
  :root{--bg:#fff;--fg:#18181b;--mut:#71717a;--line:#e4e4e7;--card:#fafafa;--acc:#eab308}
  @media(prefers-color-scheme:dark){:root{--bg:#0b0b0d;--fg:#e8e8ea;--mut:#8b8b93;--line:#26262b;--card:#141418}}
  :root[data-theme=dark]{--bg:#0b0b0d;--fg:#e8e8ea;--mut:#8b8b93;--line:#26262b;--card:#141418}
  :root[data-theme=light]{--bg:#fff;--fg:#18181b;--mut:#71717a;--line:#e4e4e7;--card:#fafafa}
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;-webkit-text-size-adjust:100%}
  .wrap{max-width:820px;margin:0 auto;padding:20px 16px 64px}
  h1{font-size:20px;margin:0 0 4px;letter-spacing:-.02em}
  .sub{color:var(--mut);font-size:13px;margin:0 0 16px}
  .verdict{border:1px solid var(--line);border-left:4px solid var(--acc);border-radius:10px;padding:14px 16px;background:var(--card);margin-bottom:18px}
  .verdict b{font-size:15px}
  .verdict p{margin:8px 0 0;font-size:14px;color:var(--fg)}
  .chips{display:flex;flex-wrap:wrap;gap:6px;margin:12px 0 20px}
  .chip{font-size:12px;font-weight:600;padding:3px 9px;border-radius:999px;border:1px solid var(--line);white-space:nowrap}
  .v-missing{background:#fee2e2;color:#991b1b;border-color:#fecaca}
  .v-deferred{background:#fef9c3;color:#854d0e;border-color:#fde68a}
  .v-alt{background:#dbeafe;color:#1e40af;border-color:#bfdbfe}
  .v-same{background:#dcfce7;color:#166534;border-color:#bbf7d0}
  .v-expanded{background:#f3e8ff;color:#6b21a8;border-color:#e9d5ff}
  .v-stays{background:#e4e4e7;color:#3f3f46;border-color:#d4d4d8}
  .v-drop{background:#f4f4f5;color:#71717a;border-color:#e4e4e7}
  .v-uneval{background:#fff;color:#3f3f46;border-color:#a1a1aa;border-style:dashed}
  .notice{border:1px solid var(--line);border-left:4px solid #a1a1aa;border-radius:10px;background:var(--card);padding:4px 14px 12px}
  .tag.uneval{background:#e4e4e7;color:#3f3f46}
  .manual{font-size:12.5px;color:var(--fg);opacity:.9;margin:12px 0 2px;padding:9px 11px;background:var(--bg);border:1px dashed var(--line);border-left:3px solid var(--acc);border-radius:8px}
  @media(prefers-color-scheme:dark){
   .v-missing{background:#3b1213;color:#fca5a5;border-color:#5b1a1c}
   .v-deferred{background:#3a2e08;color:#fcd34d;border-color:#5c4a0c}
   .v-alt{background:#0e2148;color:#93c5fd;border-color:#173263}
   .v-same{background:#0c2c18;color:#86efac;border-color:#14401f}
   .v-expanded{background:#2a1245;color:#d8b4fe;border-color:#3f1d63}
   .v-stays{background:#232327;color:#c4c4cc;border-color:#33333a}
   .v-drop{background:#1a1a1e;color:#8b8b93;border-color:#2a2a30}
   .v-uneval{background:#0b0b0d;color:#c4c4cc;border-color:#4a4a52}}
  h2{font-size:13px;text-transform:uppercase;letter-spacing:.06em;color:var(--mut);margin:26px 0 10px}
  .grp{border:1px solid var(--line);border-radius:10px;margin-bottom:10px;overflow:hidden;background:var(--card)}
  .grp-h{width:100%;display:flex;align-items:center;justify-content:space-between;gap:10px;padding:11px 14px;border:0;border-left:4px solid currentColor;background:transparent;color:inherit;font:inherit;font-weight:700;cursor:pointer;text-align:left}
  .grp-h.v-missing{color:#dc2626}.grp-h.v-deferred{color:#ca8a04}.grp-h.v-alt{color:#2563eb}.grp-h.v-same{color:#16a34a}.grp-h.v-expanded{color:#9333ea}.grp-h.v-stays{color:#71717a}.grp-h.v-drop{color:#a1a1aa}
  .grp-title{font-size:14px}
  .grp-count{font-size:12px;background:var(--bg);border:1px solid var(--line);border-radius:999px;padding:1px 9px;color:var(--fg)}
  .grp-body{padding:2px 14px 12px}
  .grp.closed .grp-body{display:none}
  .grp-blurb{font-size:12.5px;color:var(--mut);margin:6px 0 10px}
  .cap{padding:10px 0;border-top:1px solid var(--line)}
  .cap-h{display:flex;flex-wrap:wrap;align-items:center;gap:7px}
  .cap-name{font-weight:600;font-size:14px}
  .cap-map{margin-top:5px;font-size:12px;color:var(--mut);display:flex;flex-wrap:wrap;align-items:center;gap:6px}
  .cap-map code,.c-map code,.c-note code,.c-fix code{background:var(--bg);border:1px solid var(--line);border-radius:5px;padding:1px 5px;font-size:11.5px;word-break:break-word}
  .arr{color:var(--acc);font-weight:700}
  .cap-note{margin-top:5px;font-size:12.5px;color:var(--fg);opacity:.85;padding-left:8px;border-left:2px solid var(--line)}
  .tag{font-size:10.5px;font-weight:700;padding:1px 7px;border-radius:6px;text-decoration:none}
  .tag.landed{background:#dcfce7;color:#166534}.tag.designed{background:#fef9c3;color:#854d0e}
  .tag.fix{background:#fee2e2;color:#991b1b}
  @media(prefers-color-scheme:dark){.tag.landed{background:#0c2c18;color:#86efac}.tag.designed{background:#3a2e08;color:#fcd34d}.tag.fix{background:#3b1213;color:#fca5a5}.tag.uneval{background:#232327;color:#c4c4cc}}
  ol.contractions{list-style:none;padding:0;margin:0;counter-reset:c}
  ol.contractions li{border:1px solid var(--line);border-left:4px solid #dc2626;border-radius:9px;padding:12px 14px;margin-bottom:9px;background:var(--card)}
  .c-tag{display:inline-block;font-weight:800;font-size:12px;background:#fee2e2;color:#991b1b;border-radius:6px;padding:1px 8px;margin-right:6px}
  .c-tag.block{background:#fde68a;color:#854d0e}
  .c-note{font-size:13px;margin:7px 0;color:var(--fg);opacity:.9}
  .c-map{font-size:12px;color:var(--mut);margin:5px 0}
  .c-fix{font-size:12px;color:var(--mut)}
  .rerun{border:1px solid var(--line);border-radius:10px;background:var(--card);padding:12px 14px;margin-top:8px}
  .rerun code{display:block;background:var(--bg);border:1px solid var(--line);border-radius:6px;padding:8px 10px;font-size:12px;margin-top:6px;overflow-x:auto;white-space:pre}
  footer{margin-top:28px;font-size:11.5px;color:var(--mut);text-align:center}
</style>
<div class="wrap">
  <h1>parlay × firstmate — capability parity</h1>
  <p class="sub">Independent audit · task-4bad · ${total} firstmate capabilities mapped onto the fold (decision-3ae). Auditor: parity-audit (not parlay-dev).</p>

  <div class="verdict">
    <b>Verdict: EXPANSION-ONLY holds at the DESIGN level${headlineGaps}.</b>
    <p>${foldStatus} Of ${total} capabilities, ${evaluated} were evaluated here${notEvaluated.length ? ` and ${notEvaluated.length} could not be` : ""}. Of those ${evaluated}: <b>${haveHome} have a home</b> (parlay primitive, retained firstmate policy, or justified drop) and parlay genuinely <b>expands</b> (durable identity, phone reach, panel enrollment, richer state oracle). ${gapProse}</p>
  </div>

  <div class="chips">${chips}</div>

  ${contractionSection}
${notEvalSection}
  <h2>Full parity matrix — ${evaluated} of ${total} capabilities evaluated</h2>
  ${groups}

  <h2>Re-runnable check</h2>
  <div class="rerun">
    Re-derives this matrix by probing both repos live. Exits non-zero if any contraction loses its fix task, or a "landed" claim fails its probe.
    <code>cd ~/code/parlay && bun tools/parity-audit-firstmate.ts        # human table
bun tools/parity-audit-firstmate.ts --json   # machine
bun tools/parity-audit-page.ts &gt; ~/pulse-pages/parity-audit/index.html  # this page</code>
  </div>

  <footer>Generated from tools/parity-audit-firstmate.ts · verdicts are the auditor's, adversarially derived · task-4bad</footer>
</div>`

console.log(html)
