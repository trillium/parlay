// Shared fixtures for this folder's unit tests. Not a .test.ts, so it is
// never collected as a suite. The guard modules are deliberately
// dependency-free (no storage/sse imports), so every test built on this runs
// with zero side effects: no ~/exchange, no port, no timers.

export const HOST = "localhost:31337"
export const SAME_ORIGIN = "http://localhost:31337"
export const EVIL = "https://evil.example.com"

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
