import { expect, test } from "bun:test"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

// Run the audit with PARLAY_FOLD_DOC pointed at a directory (read throws EISDIR).
test("unreadable PARLAY_FOLD_DOC warns loudly instead of throwing", () => {
  const dir = mkdtempSync(join(tmpdir(), "folddoc-"))
  const p = Bun.spawnSync(["bun", join(import.meta.dir, "parity-audit-firstmate.ts")], {
    env: { ...process.env, PARLAY_FOLD_DOC: dir },
  })
  const err = p.stderr.toString()
  expect(err).toContain("is unreadable or empty")
  expect(err).not.toContain("EISDIR")
})
