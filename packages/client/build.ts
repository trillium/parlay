import { build } from "bun"
await build({
  entrypoints: ["./src/init.ts"],
  outdir: "./dist",
  naming: "parlay-agent.js",
  format: "iife",
  minify: false,
  target: "browser",
})
console.log("dist/parlay-agent.js built")
