// capture-to-golden.ts — companion to capture-sse-golden.sh, which is the only
// thing that invokes it. Parses two raw SSE captures, normalizes volatile
// values, slices frames into scenario steps by the cumulative frame-count
// boundaries the shell harness recorded, and prints the capture as JSON.
//
// The normalization here must stay rule-for-rule identical to normalizeValue
// in internal/handlers/sse_golden_test.go — the Go test applies the same
// rules to its own capture before comparing. That pairing is the whole reason
// this file still exists: the normalization is the portable half of a capture,
// and it is the part a reader can still check by eye after the reference
// server (packages/server) was deleted in the Bun→Go cutover.
//
// Usage: bun capture-to-golden.ts <legacy.raw> <l1,l2,...> <caps.raw> <c1,c2,...>
//   SSE_GOLDEN_CAPTURED_FROM  labels the output's `capturedFrom` field

const STEPS = ["connect-burst", "register-agent", "poll-park", "send", "reload", "unregister"]

// The one stable identifier in the scenario; every other id is volatile.
const STABLE_ID = "golden"
const NORM_KEYS = new Set(["ts", "clientId", "connectedAt", "lastSeen"])

interface Frame { event: string; data: unknown }

function normalize(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(normalize)
  if (v && typeof v === "object") {
    const out: Record<string, unknown> = {}
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      if (NORM_KEYS.has(k) && typeof val === "string") out[k] = "<norm>"
      else if (k === "id" && val !== STABLE_ID) out[k] = "<norm>"
      else out[k] = normalize(val)
    }
    return out
  }
  return v
}

function parseSSE(raw: string, file: string): Frame[] {
  const frames: Frame[] = []
  let event = ""
  let data = ""
  for (const line of raw.split("\n")) {
    if (line === "") {
      if (event !== "" || data !== "") {
        if (event === "" || data === "") throw new Error(`${file}: frame missing event or data: event=${event} data=${data}`)
        frames.push({ event, data: normalize(JSON.parse(data)) })
        event = ""; data = ""
      }
      continue
    }
    if (line.startsWith(":")) continue // keepalive comment (TS `: ka`, Go `: keep-alive`)
    if (line.startsWith("event: ")) event = line.slice("event: ".length)
    else if (line.startsWith("data: ")) data = line.slice("data: ".length)
    else throw new Error(`${file}: unrecognized SSE line: ${JSON.stringify(line)}`)
  }
  // presence_map never enters the golden: the TypeScript server rebroadcast
  // it from a 10s sweep timer (packages/server/src/sse.ts, deleted in the
  // Bun→Go cutover — the rule is preserved here) whose arrivals are
  // wall-clock-nondeterministic, and its vocabulary diverges anyway
  // (api-contract.md ledger row 3). The shell harness excludes it from frame
  // counting for the same reason, so the boundaries line up with this filter.
  //
  // commands is the mirror image: a Go-only burst snapshot (ledger row 29) that
  // the TypeScript server had no equivalent of, so it is absent from the
  // committed golden and dropped by transformGoStep. Filtering it here keeps
  // this pipeline, the harness's boundary accounting and the test's two
  // transforms modelling the same set of frames — otherwise a capture of the Go
  // server is one frame longer than the boundaries that describe it, and
  // slice() rejects it as "a frame arrived outside the scenario".
  return frames.filter(f => f.event !== "presence_map" && f.event !== "commands")
}

function slice(frames: Frame[], bounds: number[], file: string): Frame[][] {
  if (bounds.length !== STEPS.length) throw new Error(`${file}: ${bounds.length} boundaries for ${STEPS.length} steps`)
  const last = bounds[bounds.length - 1]
  if (frames.length !== last) throw new Error(`${file}: ${frames.length} frames captured but final boundary is ${last} — a frame arrived outside the scenario`)
  const steps: Frame[][] = []
  let prev = 0
  for (const b of bounds) {
    if (b < prev) throw new Error(`${file}: boundaries not monotonic`)
    steps.push(frames.slice(prev, b))
    prev = b
  }
  return steps
}

const [legacyFile, legacyBounds, capsFile, capsBounds] = process.argv.slice(2)
if (!legacyFile || !legacyBounds || !capsFile || !capsBounds) {
  console.error("usage: bun capture-to-golden.ts <legacy.raw> <l1,l2,...> <caps.raw> <c1,c2,...>")
  process.exit(2)
}
const parseBounds = (s: string) => s.split(",").map(n => parseInt(n, 10))

const golden = {
  // Provenance of whatever was captured, not a promise that it can be
  // re-captured: see capture-sse-golden.sh's header for why the committed
  // golden is frozen.
  capturedFrom: process.env.SSE_GOLDEN_CAPTURED_FROM || "packages/go-server (Go)",
  normalizedBy: "packages/go-server/parity/capture-to-golden.ts",
  steps: STEPS,
  legacy: slice(parseSSE(await Bun.file(legacyFile).text(), legacyFile), parseBounds(legacyBounds), legacyFile),
  caps: slice(parseSSE(await Bun.file(capsFile).text(), capsFile), parseBounds(capsBounds), capsFile),
}
console.log(JSON.stringify(golden, null, 2))
