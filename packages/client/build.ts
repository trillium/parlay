import { build } from "bun"

// parlay-agent.js — canonical output name for standalone users
await build({
  entrypoints: ["./src/init.ts"],
  outdir: "./dist",
  naming: "parlay-agent.js",
  format: "iife",
  minify: false,
  target: "browser",
})

// pulse-agent.js — compatibility alias served by Pulse via ~/pulse-pages/annotate/ symlink
await build({
  entrypoints: ["./src/init.ts"],
  outdir: ".",
  naming: "pulse-agent.js",
  format: "iife",
  minify: false,
  target: "browser",
})

// Plugins — one IIFE per src-plugins entry, served at /annotate/plugins/<id>.js
await build({
  entrypoints: ["./src-plugins/cursorless.ts", "./src-plugins/speak.ts"],
  outdir: "./plugins",
  format: "iife",
  minify: false,
  target: "browser",
})

console.log("dist/parlay-agent.js + pulse-agent.js + plugins built")

// Deploy = live upgrade: tell connected panels to reload; each page's SSE
// reconnect also runs the version handshake, so even missed broadcasts heal.
try {
  await fetch("http://127.0.0.1:31337/api/chat/reload", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
    signal: AbortSignal.timeout(2_000),
  })
  console.log("live clients told to reload")
} catch { console.log("(Pulse not reachable — clients will self-upgrade on next reconnect)") }
