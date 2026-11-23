// Only http(s) (or a relative path, which resolves to the page's own scheme) may
// be navigated to or rendered as a link. javascript:, data: and the like are
// executable or content-injecting, and these URLs arrive over the unauthenticated
// chat API, so they are never trusted to be benign.
export function isSafeUrl(u: unknown): u is string {
  if (typeof u !== 'string' || !u) return false
  try {
    const p = new URL(u, 'http://relative.invalid/').protocol
    return p === 'http:' || p === 'https:'
  } catch { return false }
}
