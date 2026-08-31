// Who is allowed to talk to a guarded route, and what content type they must
// use. Pure predicates — no Response construction, no route knowledge. See
// ./index.ts for how they are applied and ./paths.ts for where.

// Loopback and private-LAN literals. The phone reaches the panel over the LAN
// (Origin http://192.168.x.x:4242) and legacy Pulse may reverse-proxy /api/chat/* to
// this server under a rewritten Host, so a strict same-host test alone would
// cut off legitimate local clients. None of these can be an attacker's origin
// without them already serving pages from inside the captain's network — and
// DNS rebinding does not help, since the Origin header keeps the attacker's
// own name.
const PRIVATE_V4 = /^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/

function isLocalHostname(hostname: string): boolean {
  const h = hostname.replace(/^\[|\]$/g, "").toLowerCase()
  if (h === "localhost" || h.endsWith(".localhost")) return true
  if (h.endsWith(".local")) return true
  if (h === "::1" || h === "0:0:0:0:0:0:0:1") return true
  return PRIVATE_V4.test(h)
}

// PARLAY_ALLOWED_ORIGINS: comma-separated exact origins (e.g. a tunnel
// hostname). "*" opts out of the origin check entirely — an escape hatch for
// a deployment that needs it, never the default.
export function allowedOriginList(): string[] {
  return (process.env.PARLAY_ALLOWED_ORIGINS ?? "").split(",").map(s => s.trim()).filter(Boolean)
}

export function originAllowed(req: Request): boolean {
  const origin = req.headers.get("origin")
  // No Origin at all → not a browser cross-site request. CLI, curl, hooks,
  // MuseFeeder, the relay. Allowing this is what keeps the live fleet working.
  if (!origin) return true

  const list = allowedOriginList()
  if (list.includes("*")) return true
  if (list.includes(origin)) return true

  // "null" is what a sandboxed iframe / file:// / redirected request sends.
  if (origin === "null") return false

  let u: URL
  try { u = new URL(origin) } catch { return false }
  if (u.protocol !== "http:" && u.protocol !== "https:") return false

  // Same-origin: the Origin's host:port matches the Host this request arrived
  // on. Covers localhost:4242, the LAN IP, and any tunnel that forwards Host.
  const host = req.headers.get("host") ?? safeUrlHost(req.url)
  if (host && u.host.toLowerCase() === host.toLowerCase()) return true

  return isLocalHostname(u.hostname)
}

function safeUrlHost(url: string): string {
  try { return new URL(url).host } catch { return "" }
}

export function isJsonContentType(value: string | null): boolean {
  if (!value) return false
  return value.split(";")[0]!.trim().toLowerCase() === "application/json"
}
