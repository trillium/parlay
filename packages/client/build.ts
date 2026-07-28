import { build } from "bun"

// Bun.build() does NOT throw on a compile error — it returns {success:false,
// logs} and leaves the previous output in place. Unchecked, that silently ships
// a STALE bundle while still printing "built". Fail loud instead.
async function buildOrThrow(opts: Parameters<typeof build>[0], label: string) {
  const r = await build(opts)
  if (!r.success) {
    for (const l of r.logs) console.error(l)
    throw new Error(`build failed: ${label} — stale bundle left untouched`)
  }
}

// parlay-agent.js — canonical output name for standalone users
await buildOrThrow({
  entrypoints: ["./src/init.ts"],
  outdir: "./dist",
  naming: "parlay-agent.js",
  format: "iife",
  minify: false,
  target: "browser",
}, "parlay-agent.js")

// pulse-agent.js — compatibility alias served by Pulse via ~/pulse-pages/annotate/ symlink
await buildOrThrow({
  entrypoints: ["./src/init.ts"],
  outdir: ".",
  naming: "pulse-agent.js",
  format: "iife",
  minify: false,
  target: "browser",
}, "pulse-agent.js")

// Plugins — one IIFE per src-plugins entry, served at /annotate/plugins/<id>.js
await buildOrThrow({
  entrypoints: ["./src-plugins/cursorless.ts", "./src-plugins/speak.ts"],
  outdir: "./plugins",
  format: "iife",
  minify: false,
  target: "browser",
}, "plugins")

// index.js — ESM library entry for external host apps (e.g. herdr-web) that
// import @parlay/client's server-eval input dispatcher instead of embedding
// the full in-page panel. See src/lib.ts for the exported surface.
await buildOrThrow({
  entrypoints: ["./src/lib.ts"],
  outdir: "./dist",
  naming: "index.js",
  format: "esm",
  minify: false,
  target: "browser",
}, "index.js (library)")

console.log("dist/parlay-agent.js + pulse-agent.js + index.js + plugins built")

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
