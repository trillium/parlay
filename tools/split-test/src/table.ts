// Minimal PASS/FAIL comparison table renderer. No deps — pads columns to align.

export interface Row {
  check: string
  a: string
  b: string
  verdict: "PASS" | "FAIL" | "WARN" | "SKIP"
}

const GLYPH: Record<Row["verdict"], string> = {
  PASS: "✅",
  FAIL: "❌",
  WARN: "⚠️ ",
  SKIP: "—",
}

/** Render rows as an aligned monospace table. `aLabel`/`bLabel` head the columns. */
export function renderTable(rows: Row[], aLabel: string, bLabel: string): string {
  const headers = { check: "CHECK", a: aLabel, b: bLabel, verdict: "RESULT" }
  const all = [headers, ...rows.map((r) => ({ check: r.check, a: r.a, b: r.b, verdict: `${GLYPH[r.verdict]} ${r.verdict}` }))]
  const w = {
    check: Math.max(...all.map((r) => r.check.length)),
    a: Math.max(...all.map((r) => r.a.length)),
    b: Math.max(...all.map((r) => r.b.length)),
    verdict: Math.max(...all.map((r) => r.verdict.length)),
  }
  const pad = (s: string, n: number) => s + " ".repeat(Math.max(0, n - s.length))
  const line = (r: { check: string; a: string; b: string; verdict: string }) =>
    `| ${pad(r.check, w.check)} | ${pad(r.a, w.a)} | ${pad(r.b, w.b)} | ${pad(r.verdict, w.verdict)} |`
  const sep = `|-${"-".repeat(w.check)}-|-${"-".repeat(w.a)}-|-${"-".repeat(w.b)}-|-${"-".repeat(w.verdict)}-|`
  return [line(headers), sep, ...rows.map((r) => line({ check: r.check, a: r.a, b: r.b, verdict: `${GLYPH[r.verdict]} ${r.verdict}` }))].join("\n")
}

/** Overall verdict: FAIL if any row FAILed, else PASS (WARN/SKIP don't fail the run). */
export function overallVerdict(rows: Row[]): "PASS" | "FAIL" {
  return rows.some((r) => r.verdict === "FAIL") ? "FAIL" : "PASS"
}
