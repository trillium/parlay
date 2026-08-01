// ── Localhost link rewriting (presentation-only) ─────────────────────────────
//
// Parlay surfaces `http://localhost:<port>/...` links in chat text. When the
// captain reads Parlay from off-home (e.g. his phone over Tailscale), those
// links are unreachable. This module rewrites the HOST of localhost links to a
// configured reachable host at SERVE time only — the stored message text is
// never mutated, so the behavior is reversible and toggleable via config.
//
// Config source: the `PARLAY_PUBLIC_HOST` env var.
//   - unset / empty  → NO rewrite (identical to legacy behavior; pure opt-in)
//   - "auto"         → resolve once from `tailscale status --json` (Self.DNSName,
//                      preferring the short node name e.g. "macbook"), cached;
//                      fail-open to NO rewrite if tailscale is unavailable
//   - "<host-or-ip>" → use that hostname/IP literally. The captain prefers the
//                      short tailnet name ("macbook", MagicDNS resolves it), but
//                      an FQDN ("macbook.hippo-tilapia.ts.net") or bare IP
//                      ("100.74.138.74") work identically — only the host swaps.
//
// Fail-open is absolute: any error in resolution or rewriting returns the
// original text unchanged. This transform must never break message serving.

import { spawnSync } from "child_process"

// Match `http://localhost:<port>` or `http://127.0.0.1:<port>` and capture the
// port. The host alternatives are anchored: the required `:` immediately after
// the host means `localhost.evil.com` (a `.` follows the host) never matches,
// and `127.0.0.1` is spelled with literal dots so no other host collides. Only
// the host is swapped; the `:<port>` and everything after (path, query, hash)
// are left byte-for-byte intact because the match ends at the port digits.
//
// `http://127.0.0.1:` — the `\b` after would be ambiguous around dots, so we do
// not use a trailing boundary; the leading `http://` plus the required literal
// host token and `:` is a precise enough anchor for the URLs Parlay emits.
const LOCALHOST_LINK = /http:\/\/(?:localhost|127\.0\.0\.1):(\d+)/g

// Resolved public host, cached across calls. `undefined` = not yet resolved
// this process; `null` = resolved to "no rewrite" (unset/empty or auto-failed).
let cachedHost: string | null | undefined = undefined

// Resolve the configured host to a concrete host string, or null for no-rewrite.
// Reads the env var fresh only on first call, then caches — a single process
// keeps one answer for its lifetime (env + tailscale IP are startup-stable).
function resolvePublicHost(): string | null {
  if (cachedHost !== undefined) return cachedHost

  const raw = (process.env.PARLAY_PUBLIC_HOST ?? "").trim()
  if (!raw) {
    cachedHost = null
    return cachedHost
  }

  if (raw.toLowerCase() === "auto") {
    cachedHost = resolveTailscaleHost()
    return cachedHost
  }

  cachedHost = raw
  return cachedHost
}

// Resolve this machine's Tailscale name for `auto` mode.
//
// The captain reads Parlay over the tailnet where MagicDNS is active, so the
// SHORT node name (e.g. `macbook`) resolves and is what he wants to see in
// links — not the raw IP and not the trailing-dotted FQDN. We read
// `Self.DNSName` from `tailscale status --json` (e.g. `macbook.hippo-tilapia.
// ts.net.`), strip the trailing dot, and prefer its first label (`macbook`).
// The full FQDN is the fallback if the name has no usable label.
//
// Returns null on any failure (binary missing, non-zero exit, unparseable
// output, empty name) so `auto` fails open to no-rewrite. Never throws.
function resolveTailscaleHost(): string | null {
  try {
    const res = spawnSync("tailscale", ["status", "--json"], {
      encoding: "utf8",
      timeout: 5_000,
    })
    if (res.error || res.status !== 0 || typeof res.stdout !== "string") return null

    let dnsName: unknown
    try {
      dnsName = JSON.parse(res.stdout)?.Self?.DNSName
    } catch {
      return null
    }
    if (typeof dnsName !== "string") return null

    // Strip the trailing FQDN dot: `macbook.hippo-tilapia.ts.net.` → `...ts.net`.
    const fqdn = dnsName.replace(/\.$/, "").trim()
    if (!fqdn) return null

    // Prefer the first label (the short node name the captain asked for). Fall
    // back to the full FQDN if, improbably, there is no leading label.
    const shortName = fqdn.split(".")[0]
    return shortName || fqdn
  } catch {
    return null
  }
}

// Rewrite localhost/127.0.0.1 hosts in `text` to the configured public host.
// Returns the original text unchanged when no host is configured, when the text
// has no localhost links, or when anything goes wrong (fail-open).
export function rewriteLocalhostLinks(text: string): string {
  try {
    if (typeof text !== "string" || text.length === 0) return text
    const host = resolvePublicHost()
    if (!host) return text
    // Cheap guard: skip the regex entirely when there is nothing to rewrite.
    if (!text.includes("http://")) return text
    return text.replace(LOCALHOST_LINK, `http://${host}:$1`)
  } catch {
    return text
  }
}

// Minimal shape this module needs from a chat message: an optional string
// `text` field. Kept structural (not an import of ChatMessage) so the helper
// stays dependency-free and trivially testable, while callers pass real
// ChatMessage objects — the generic `<T>` preserves their full type.
interface HasText {
  text?: unknown
}

// Return a SERVE-SAFE view of a message: identical to the input except its
// `text` has localhost links rewritten. The stored object is never mutated — a
// shallow clone is returned only when a rewrite actually changes the text, so
// the common (no-op) path returns the original reference with zero allocation.
// Any non-string/absent `text`, or a no-op rewrite, yields the original object.
export function rewriteMessageForServe<T extends HasText>(msg: T): T {
  try {
    if (!msg || typeof msg.text !== "string") return msg
    const rewritten = rewriteLocalhostLinks(msg.text)
    if (rewritten === msg.text) return msg
    return { ...msg, text: rewritten }
  } catch {
    return msg
  }
}

// Serve-safe view of a list of messages. Maps each element through
// rewriteMessageForServe; when nothing changes the original array is returned
// so history serving allocates nothing in the no-rewrite (unset host) case.
export function rewriteMessagesForServe<T extends HasText>(msgs: readonly T[]): T[] | readonly T[] {
  try {
    if (!Array.isArray(msgs) || msgs.length === 0) return msgs
    let changed = false
    const out = msgs.map(m => {
      const r = rewriteMessageForServe(m)
      if (r !== m) changed = true
      return r
    })
    return changed ? out : msgs
  } catch {
    return msgs
  }
}

// Test-only hook: clear the resolution cache so a test can flip the env var and
// re-resolve. Not part of the runtime serving path.
export function __resetLinkRewriteCacheForTest(): void {
  cachedHost = undefined
}
