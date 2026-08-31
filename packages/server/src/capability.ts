// Interface capability declaration — the OUTPUT-direction delivery gate
// (docs/interface-capabilities.md). A connecting surface may declare, via
// `?caps=<url-encoded JSON>` on GET /api/chat/events, which presentation
// commands it accepts; delivery of the gated event names to that connection
// is then narrowed to its declaration. An undeclared client is legacy:
// byte-identical delivery, gated by nothing.
//
// This is the TS mirror of the normative engine in
// tools/cli/internal/capability (a separate Go module this package cannot
// import). Keep the two in lockstep — the Go package's tests are the
// contract; a divergence here is a bug here.

export const SUPPORTED_SCHEMA_MAJOR = 1
export const MAX_DECLARATION_BYTES  = 8 * 1024
export const MAX_ACCEPTS            = 64
export const MAX_TOKENS             = 32

// The gated class: exactly the five Q2d panel-aiming names. Every other name
// (lifecycle, state reports, unknown/future events) is delivered to everyone —
// a new event is deliverable-to-all until deliberately admitted here.
export const PRESENTATION_COMMANDS = new Set([
  "navigate", "reload", "device_cmd", "input_action", "draft",
])

const NAME_RE     = /^[a-z][a-z0-9_]{0,63}$/
const INSTANCE_RE = /^[A-Za-z0-9._-]{1,128}$/
// Mirrors supersession.ParseVersion's strictness: three plain non-negative
// decimal fields, no signs, no prerelease tags, no leading "v".
const SCHEMA_RE   = /^[0-9]+\.[0-9]+\.[0-9]+$/

export type CapabilityDeclaration = {
  schema:       string
  surface:      { kind: string; instance?: string }
  accepts:      Record<string, unknown>   // name → open detail object, preserved as-is
  content:      string[]
  interactions: string[]
}

// parseDeclaration parses and validates one raw declaration. Strict and loud:
// an invalid declaration must refuse the connection rather than fall back to
// legacy full delivery — fail-open would widen what a narrowing surface
// receives. Unknown top-level fields are deliberately ignored (LSP's posture)
// so an additive field from a newer surface does not break this server.
export function parseDeclaration(raw: string): { decl: CapabilityDeclaration } | { error: string } {
  if (new TextEncoder().encode(raw).length > MAX_DECLARATION_BYTES)
    return { error: `capability declaration exceeds the ${MAX_DECLARATION_BYTES}-byte cap` }
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { return { error: "capability declaration is not valid JSON" } }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed))
    return { error: "capability declaration: want a JSON object" }
  const obj = parsed as Record<string, unknown>

  const schema = obj.schema
  if (typeof schema !== "string" || !SCHEMA_RE.test(schema))
    return { error: `schema ${JSON.stringify(schema)}: want MAJOR.MINOR.PATCH` }
  const major = parseInt(schema, 10)
  if (major !== SUPPORTED_SCHEMA_MAJOR)
    return { error: `schema major ${major} unsupported (this server implements ${SUPPORTED_SCHEMA_MAJOR})` }

  const surface = obj.surface
  if (typeof surface !== "object" || surface === null || Array.isArray(surface))
    return { error: "surface: want an object with a kind" }
  const kind = (surface as Record<string, unknown>).kind
  if (typeof kind !== "string" || !NAME_RE.test(kind))
    return { error: `surface.kind ${JSON.stringify(kind)}: want ${NAME_RE}` }
  const instance = (surface as Record<string, unknown>).instance
  if (instance !== undefined && instance !== "" && (typeof instance !== "string" || !INSTANCE_RE.test(instance)))
    return { error: `surface.instance ${JSON.stringify(instance)}: want ${INSTANCE_RE}` }

  const rawAccepts = obj.accepts ?? {}
  if (typeof rawAccepts !== "object" || rawAccepts === null || Array.isArray(rawAccepts))
    return { error: "accepts: want an object of name → detail" }
  const accepts = rawAccepts as Record<string, unknown>
  const names = Object.keys(accepts)
  if (names.length > MAX_ACCEPTS)
    return { error: `${names.length} accepts entries exceeds the ${MAX_ACCEPTS} cap` }
  for (const n of names) {
    if (!NAME_RE.test(n)) return { error: `accepts name ${JSON.stringify(n)}: want ${NAME_RE}` }
  }

  const tokens: Record<string, string[]> = {}
  for (const axis of ["content", "interactions"] as const) {
    const arr = obj[axis] ?? []
    if (!Array.isArray(arr)) return { error: `${axis}: want an array of tokens` }
    if (arr.length > MAX_TOKENS)
      return { error: `${arr.length} ${axis} entries exceeds the ${MAX_TOKENS} cap` }
    for (const tok of arr) {
      if (typeof tok !== "string" || !NAME_RE.test(tok))
        return { error: `${axis} token ${JSON.stringify(tok)}: want ${NAME_RE}` }
    }
    tokens[axis] = arr as string[]
  }

  return {
    decl: {
      schema,
      surface: { kind, ...(typeof instance === "string" && instance !== "" ? { instance } : {}) },
      accepts,
      content:      tokens.content,
      interactions: tokens.interactions,
    },
  }
}

// recognize splits a declaration's accepts names into those this server gates
// on and those it has never heard of — echoed back on `connected` so a surface
// can see which of its names are inert here. Unknown names are preserved,
// inert, and reported; never an error (forward compatibility across minors).
export function recognize(decl: CapabilityDeclaration): { recognized: string[]; unknown: string[] } {
  const recognized: string[] = []
  const unknown:    string[] = []
  for (const n of Object.keys(decl.accepts)) {
    (PRESENTATION_COMMANDS.has(n) ? recognized : unknown).push(n)
  }
  recognized.sort()
  unknown.sort()
  return { recognized, unknown }
}

// shouldDeliver is the delivery gate (docs/interface-capabilities.md, gate
// table). The subtraction invariant: a declaration can only remove deliveries
// relative to a legacy client, never add one.
export function shouldDeliver(decl: CapabilityDeclaration | undefined, event: string): boolean {
  if (!decl) return true                              // legacy: grandfathered byte-identical
  if (!PRESENTATION_COMMANDS.has(event)) return true  // lifecycle + state reports are ungated
  return Object.prototype.hasOwnProperty.call(decl.accepts, event)
}

// Suppression counters — a silent gate is indistinguishable from a gate that
// never runs (the presenceBroadcasts precedent). Exposed on
// /api/chat/subscribers as `capability_suppressed`.
const suppressed = new Map<string, number>()

export function countSuppressed(event: string): void {
  suppressed.set(event, (suppressed.get(event) ?? 0) + 1)
}

export function suppressedCounts(): Record<string, number> {
  return Object.fromEntries([...suppressed.entries()].sort(([a], [b]) => a.localeCompare(b)))
}
