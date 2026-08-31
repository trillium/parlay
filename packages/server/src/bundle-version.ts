// Served-bundle version check — reads PA_VERSION from the compiled client
// bundle, mtime-cached. Clients compare on every SSE reconnect and self-reload
// when stale (PWA pages can live for days; this is the root fix).
//
// Which bundle? The one THIS server serves: the assets dir (static.ts) first,
// then the Pulse-era ~/pulse-pages symlink for deployments still fronted by
// Pulse. Reporting a bundle nobody serves would reload clients into a version
// that never changes — the handshake must track the served file.

import { ASSETS_DIR } from "./static"

let _bundleVer: { path: string; mtime: number; version: string } | null = null

export function bundleVersion(): string {
  const { statSync, readFileSync } = require("fs") as typeof import("fs")
  const candidates = [
    `${ASSETS_DIR}/parlay-agent.js`,
    `${ASSETS_DIR}/pulse-agent.js`,
    `${process.env.HOME}/pulse-pages/annotate/pulse-agent.js`,
  ]
  for (const path of candidates) {
    try {
      const mtime = statSync(path).mtimeMs
      if (_bundleVer && _bundleVer.path === path && _bundleVer.mtime === mtime) {
        return _bundleVer.version
      }
      const m = readFileSync(path, "utf8").match(/PA_VERSION = ["']([^"']+)["']/)
      _bundleVer = { path, mtime, version: m ? m[1] : "unknown" }
      return _bundleVer.version
    } catch { /* unreadable — try the next candidate */ }
  }
  return "unknown"
}
