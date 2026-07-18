// parlay-split CLI — argument parsing and subcommand dispatch.
//
// Subcommands:
//   sandbox up   --name <n> [--branch-dir <path>] [--with-engine]
//   sandbox down --name <n>
//   sandbox list
//   two-door     --a <urlA> --b <urlB> [--soak <seconds>]
//   two-stack    --a-dir <worktreeA> --b-dir <worktreeB> [--with-engine]

import { resolve } from "path"
import { sandboxUp, sandboxDown } from "./sandbox"
import { listSandboxes, readManifest, pidAlive } from "./manifest"
import { runTwoDoor, printTwoDoorReport } from "./two-door"
import { runTwoStack, printTwoStackReport } from "./two-stack"

const EXIT_USAGE = 2
const EXIT_FAIL = 1

/** Parse `--flag value` and `--bool` args into a map. Positionals collected too. */
function parseArgs(argv: string[]): { flags: Record<string, string | boolean>; positionals: string[] } {
  const flags: Record<string, string | boolean> = {}
  const positionals: string[] = []
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a.startsWith("--")) {
      const key = a.slice(2)
      const next = argv[i + 1]
      if (next !== undefined && !next.startsWith("--")) {
        flags[key] = next
        i++
      } else {
        flags[key] = true
      }
    } else {
      positionals.push(a)
    }
  }
  return { flags, positionals }
}

function die(msg: string, code = EXIT_USAGE): never {
  console.error(msg)
  process.exit(code)
}

function repoRoot(): string {
  // The tool lives at <repo>/tools/split-test/src/cli.ts — root is three up.
  return resolve(import.meta.dir, "..", "..", "..")
}

const USAGE = `parlay-split — Parlay split-testing tool (isolated sandboxes, zero prod contact)

USAGE:
  parlay-split sandbox up   --name <n> [--branch-dir <path>] [--with-engine]
  parlay-split sandbox down --name <n>
  parlay-split sandbox list
  parlay-split two-door     --a <urlA> --b <urlB> [--soak <seconds>]
  parlay-split two-stack    --a-dir <worktreeA> --b-dir <worktreeB> [--with-engine]

EXAMPLES:
  # Boot an isolated stack and prove env overrides are respected
  parlay-split sandbox up --name probe1

  # Same store, two front doors: direct (:31337) vs proxy (:31339), 60s soak
  parlay-split two-door --a http://localhost:31337 --b http://localhost:31339 --soak 60

  # Split-test baseline vs feature branch, each in its own isolated stack
  parlay-split two-stack --a-dir ~/code/parlay --b-dir ~/code/parlay/.worktrees/feature

Safety: sandboxes bind free high ports (42000+), never 31337/31339/4343/31338/4242,
never launchctl, and never touch prod spools/runtime. See README.md.`

async function cmdSandbox(rest: string[]): Promise<void> {
  const { flags, positionals } = parseArgs(rest)
  const sub = positionals[0]

  if (sub === "up") {
    const name = typeof flags.name === "string" ? flags.name : die("sandbox up: --name <n> required")
    const branchDir = typeof flags["branch-dir"] === "string" ? flags["branch-dir"] : repoRoot()
    const withEngine = flags["with-engine"] === true
    try {
      const m = await sandboxUp({ name, branchDir, withEngine })
      console.log(`\n✅ sandbox "${name}" UP`)
      console.log(`   server:  ${m.env.PARLAY_SERVER}`)
      console.log(`   data:    ${m.env.PARLAY_DATA_DIR}`)
      console.log(`   runtime: ${m.env.PARLAY_RELAY_RUNTIME}`)
      if (m.env.PARLAY_EVAL_ENGINE_URL) console.log(`   engine:  ${m.env.PARLAY_EVAL_ENGINE_URL}`)
      console.log(`   pids:    ${m.components.map((c) => `${c.kind}=${c.pid}`).join(" ")}`)
    } catch (err) {
      die(`\n❌ sandbox up failed: ${String(err instanceof Error ? err.message : err)}`, EXIT_FAIL)
    }
    return
  }

  if (sub === "down") {
    const name = typeof flags.name === "string" ? flags.name : die("sandbox down: --name <n> required")
    const res = sandboxDown(name)
    console.log(`✅ sandbox "${name}" DOWN`)
    for (const k of res.killed) console.log(`   ${k.kind} pid ${k.pid}: ${k.result}`)
    return
  }

  if (sub === "list") {
    const names = listSandboxes()
    if (names.length === 0) {
      console.log("no sandboxes")
      return
    }
    for (const name of names) {
      const m = readManifest(name)
      if (!m) continue
      const live = m.components.filter((c) => pidAlive(c.pid)).length
      console.log(`${name}  (${live}/${m.components.length} live)  ${m.env.PARLAY_SERVER}`)
    }
    return
  }

  die(`sandbox: unknown subcommand "${sub ?? ""}" — expected up|down|list`)
}

async function cmdTwoDoor(rest: string[]): Promise<void> {
  const { flags } = parseArgs(rest)
  const a = typeof flags.a === "string" ? flags.a : die("two-door: --a <urlA> required")
  const b = typeof flags.b === "string" ? flags.b : die("two-door: --b <urlB> required")
  const soak = typeof flags.soak === "string" ? Number(flags.soak) : 0
  if (Number.isNaN(soak) || soak < 0) die("two-door: --soak must be a non-negative number of seconds")

  const res = await runTwoDoor({ doorA: a, doorB: b, soakSeconds: soak, labelA: `A ${a}`, labelB: `B ${b}` })
  printTwoDoorReport(res, `A ${a}`, `B ${b}`)
  if (res.verdict === "FAIL") process.exit(EXIT_FAIL)
}

async function cmdTwoStack(rest: string[]): Promise<void> {
  const { flags } = parseArgs(rest)
  const aDir = typeof flags["a-dir"] === "string" ? flags["a-dir"] : die("two-stack: --a-dir <path> required")
  const bDir = typeof flags["b-dir"] === "string" ? flags["b-dir"] : die("two-stack: --b-dir <path> required")
  const withEngine = flags["with-engine"] === true

  const res = await runTwoStack({ aDir: resolve(aDir), bDir: resolve(bDir), withEngine })
  printTwoStackReport(res, resolve(aDir), resolve(bDir))
  if (res.verdict === "FAIL") process.exit(EXIT_FAIL)
}

async function main(): Promise<void> {
  const argv = process.argv.slice(2)
  const cmd = argv[0]
  const rest = argv.slice(1)

  if (!cmd || cmd === "help" || cmd === "--help" || cmd === "-h") {
    console.log(USAGE)
    return
  }

  switch (cmd) {
    case "sandbox":
      await cmdSandbox(rest)
      break
    case "two-door":
      await cmdTwoDoor(rest)
      break
    case "two-stack":
      await cmdTwoStack(rest)
      break
    default:
      die(`unknown command "${cmd}"\n\n${USAGE}`)
  }
}

main().catch((err) => {
  console.error(`fatal: ${String(err instanceof Error ? err.stack : err)}`)
  process.exit(EXIT_FAIL)
})
