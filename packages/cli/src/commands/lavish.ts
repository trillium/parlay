import { parseArgs } from "../args"
import { nextStep } from "../format"
import { helpWanted } from "../help"

export async function cmdLavishImport(args: string[]) {
  if (helpWanted("lavish-import", args)) return
  parseArgs("lavish-import", args)
  const { spawnSync } = await import("bun")
  const script = new URL("../lavish-import.ts", import.meta.url).pathname
  spawnSync(["bun", script], { stdio: ["inherit", "inherit", "inherit"], env: process.env })
  nextStep("parlay history 5")
}
