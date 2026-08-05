// Served-bundle version check — reads PA_VERSION from the compiled client
// bundle, mtime-cached. Clients compare on every SSE reconnect and self-reload
// when stale (PWA pages can live for days; this is the root fix).

let _bundleVer: { mtime: number; version: string } | null = null

export function bundleVersion(): string {
  try {
    const { statSync, readFileSync } = require("fs") as typeof import("fs")
    const path = `${process.env.HOME}/pulse-pages/annotate/pulse-agent.js`
    const mtime = statSync(path).mtimeMs
    if (_bundleVer && _bundleVer.mtime === mtime) return _bundleVer.version
    const m = readFileSync(path, "utf8").match(/PA_VERSION = ["']([^"']+)["']/)
    _bundleVer = { mtime, version: m ? m[1] : "unknown" }
    return _bundleVer.version
  } catch { return "unknown" }
}
