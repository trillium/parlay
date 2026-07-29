import { build } from "bun"

async function buildOrThrow(opts: Parameters<typeof build>[0], label: string) {
  const r = await build(opts)
  if (!r.success) {
    for (const l of r.logs) console.error(l)
    throw new Error(`build failed: ${label} — stale bundle left untouched`)
  }
}

await buildOrThrow({
  entrypoints: ["./src/index.ts"],
  outdir: "./dist",
  format: "esm",
  minify: false,
  target: "browser",
  external: ["@parlay/input"],
}, "index.js")

console.log("dist/index.js built (run `tsc --emitDeclarationOnly` for dist/index.d.ts)")
