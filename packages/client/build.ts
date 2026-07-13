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

console.log("dist/parlay-agent.js + pulse-agent.js built")
