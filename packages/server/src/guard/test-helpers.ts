// Shared fixtures for this folder's unit tests. Not a .test.ts, so it is
// never collected as a suite. The guard modules are deliberately
// dependency-free (no storage/sse imports), so every test built on this runs
// with zero side effects: no ~/exchange, no port, no timers.

export const HOST = "localhost:31337"
export const SAME_ORIGIN = "http://localhost:31337"
export const EVIL = "https://evil.example.com"

// A panel reached through a Host-forwarding tunnel or reverse proxy under a
// NON-local name — the deployment shape ./origin.ts's same-host comparison
// exists for. `panel.tunnel.test` is not loopback, not private-LAN, not
// .local and not in PARLAY_ALLOWED_ORIGINS, so that comparison is the ONLY
// thing that can accept TUNNEL_ORIGIN, and only when the request arrives on
// TUNNEL_HOST. Every other fixture in this folder (localhost:31337,
// 192.168.1.42, 127.0.0.1) is also a local hostname, so it would still be
// accepted with the comparison deleted; these two are what pin it. Send
// TUNNEL_ORIGIN on OTHER_HOST instead and it must be refused — without that
// control the accept case could pass for the wrong reason.
export const TUNNEL_HOST = "panel.tunnel.test:8443"
export const TUNNEL_ORIGIN = "http://panel.tunnel.test:8443"
export const OTHER_HOST = "other.tunnel.test:8443"

/** A request as a browser would present it: Host is where it arrived, Origin is who sent it. */
export function req(
  method: string,
  pathname: string,
  opts: { origin?: string; contentType?: string | null; host?: string } = {},
): Request {
  const headers = new Headers({ host: opts.host ?? HOST })
  if (opts.origin) headers.set("origin", opts.origin)
  if (opts.contentType !== null) headers.set("content-type", opts.contentType ?? "application/json")
  return new Request(`http://${opts.host ?? HOST}${pathname}`, { method, headers })
}

/** The CLI / curl / hook shape: no Origin header at all. */
export function noOrigin(method: string, pathname: string, contentType: string | null = "application/json"): Request {
  const headers = new Headers({ host: HOST })
  if (contentType !== null) headers.set("content-type", contentType)
  return new Request(`http://${HOST}${pathname}`, { method, headers })
}
