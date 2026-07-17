// Auto-resolve the agent's current open handoff bead id.
//
// The create→submit death window: a persistent agent's clean shutdown is two acts —
// (1) `handoff create` mints a handoff bead, (2) `identity --submit <id>` pins the
// pointer AND reincarnates. If anything is interposed between them (a courtesy
// `parlay say`, a context-limit kill), step 2 never runs and the shutdown is stranded
// with a live bead but no pin/restart. Requiring the id to be threaded by hand through
// step 2 is exactly what leaves room for the interposition.
//
// Fix: make the id OPTIONAL. When `--submit`/`--handoff` is given no id, ask the
// handoff store for the currently-active bead (`handoff show --current`, which the
// store defines as the in-progress / hooked / last-touched issue). So the skill can
// run `handoff create … && identity --submit` as ONE atomic act with nothing between,
// and a create that DID get stranded is recovered by a bare `identity --submit`.

import { spawnSync } from "child_process"

// The store CLI name is the bead-id prefix ("handoff-1bk" → `handoff`). Kept a
// parameter (not hardcoded) so a differently-named handoff store still resolves.
export const DEFAULT_HANDOFF_STORE = "handoff"

// Query the store for the current open handoff bead's id. Returns undefined when the
// store is unavailable or reports no active handoff — callers then demand an explicit
// id rather than guessing. Never throws.
export function resolveCurrentHandoff(store: string = DEFAULT_HANDOFF_STORE): string | undefined {
  // Pass env explicitly so command resolution honors the live process.env.PATH
  // (bun's spawnSync otherwise resolves against a cached PATH).
  const r = spawnSync(store, ["show", "--current", "--json"], { encoding: "utf8", env: process.env })
  if (r.error || r.status !== 0 || !r.stdout) return undefined
  try {
    const parsed = JSON.parse(r.stdout)
    const arr = Array.isArray(parsed) ? parsed : [parsed]
    const id = (arr[0]?.id ?? "").toString().trim()
    return id || undefined
  } catch {
    return undefined
  }
}
