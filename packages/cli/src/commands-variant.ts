// parlay CLI — variant agent commands: launch, list, merge, teardown.
// A variant is an isolated fork of a primary agent running in a git worktree.
// Naming: <primary-id>-<label> (sibling home, e.g. mechanic-wt1).

import { existsSync, readFileSync, writeFileSync, mkdirSync, rmSync, readdirSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { EXIT_USAGE } from "./config"
import { die, postJSON } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"
import { guardRepo, mainWorktreePath } from "./commands-guard"

const AGENTS_DIR  = join(homedir(), ".parlay", "agents")
const WKTREES_DIR = join(homedir(), ".parlay", "worktrees")

function parseFm(src: string): Record<string, string> {
  const m = src.match(/^---\n([\s\S]*?)\n---/)
  if (!m) return {}
  const out: Record<string, string> = {}
  for (const line of m[1].split("\n")) { const kv = line.match(/^(\w+):\s*"?([^"]*)"?\s*$/); if (kv) out[kv[1]] = kv[2] }
  return out
}
function writeFm(file: string, fm: Record<string, string>): void {
  const txt = existsSync(file) ? readFileSync(file, "utf-8") : ""
  const rest = txt.replace(/^---\n[\s\S]*?\n---\n/, "")
  const lines = Object.entries(fm).filter(([, v]) => v).map(([k, v]) => `${k}: ${/[:#'"\s]/.test(v) ? JSON.stringify(v) : v}`)
  writeFileSync(file, `---\n${lines.join("\n")}\n---\n${rest || `# Identity — ${fm.id ?? ""}\n\n`}`)
}
function readFm(agentId: string): Record<string, string> {
  const f = join(AGENTS_DIR, agentId, "identity.md")
  return existsSync(f) ? parseFm(readFileSync(f, "utf-8")) : {}
}
function sh(cmd: string, args: string[]): { ok: boolean; out: string; err: string } {
  const r = Bun.spawnSync([cmd, ...args], { stdout: "pipe", stderr: "pipe" })
  return { ok: r.exitCode === 0, out: new TextDecoder().decode(r.stdout).trim(), err: new TextDecoder().decode(r.stderr).trim() }
}

// Extract bullet facts/notes from a memory body, keyed by content (date-prefix stripped).
function facts(body: string): Array<{ line: string; key: string }> {
  return body.split("\n").filter(l => l.startsWith("- [")).map(l => ({
    line: l,
    key:  l.replace(/^- \[\d{4}-\d{2}-\d{2}\] /, "").replace(/\s*\[from: [^\]]+\]$/, "").trim(),
  }))
}

// Append variant's novel insights into primary's file. Returns count of new lines merged.
function mergeKind(variantId: string, primaryId: string, kind: "identity" | "scratchpad"): number {
  const vf = join(AGENTS_DIR, variantId, `${kind}.md`)
  const pf = join(AGENTS_DIR, primaryId, `${kind}.md`)
  if (!existsSync(vf)) return 0
  const vFacts = facts(readFileSync(vf, "utf-8").replace(/^---\n[\s\S]*?\n---\n/, ""))
  const pKeys  = new Set(existsSync(pf) ? facts(readFileSync(pf, "utf-8").replace(/^---\n[\s\S]*?\n---\n/, "")).map(f => f.key) : [])
  const fresh  = vFacts.filter(f => !pKeys.has(f.key))
  if (!fresh.length) return 0
  if (!existsSync(pf)) writeFileSync(pf, `# ${kind === "identity" ? "Identity" : "Scratchpad"} — ${primaryId}\n\n`)
  writeFileSync(pf, readFileSync(pf, "utf-8").trimEnd() + "\n" + fresh.map(f => `${f.line} [from: ${variantId}]`).join("\n") + "\n")
  return fresh.length
}
function unmergedCount(variantId: string, primaryId: string, kind: "identity" | "scratchpad"): number {
  const vf = join(AGENTS_DIR, variantId, `${kind}.md`)
  const pf = join(AGENTS_DIR, primaryId, `${kind}.md`)
  if (!existsSync(vf)) return 0
  const vFacts = facts(readFileSync(vf, "utf-8").replace(/^---\n[\s\S]*?\n---\n/, ""))
  const pKeys  = new Set(existsSync(pf) ? facts(readFileSync(pf, "utf-8").replace(/^---\n[\s\S]*?\n---\n/, "")).map(f => f.key) : [])
  return vFacts.filter(f => !pKeys.has(f.key)).length
}

function autoLabel(primaryId: string): string {
  if (!existsSync(AGENTS_DIR)) return "wt1"
  const nums = readdirSync(AGENTS_DIR).filter(d => d.startsWith(`${primaryId}-wt`))
    .map(d => parseInt(d.slice(primaryId.length + 3), 10)).filter(n => !isNaN(n))
  return `wt${nums.length ? Math.max(...nums) + 1 : 1}`
}

export async function cmdVariantLaunch(args: string[]) {
  if (helpWanted("variant launch", args)) return
  const { positionals, opts } = parseArgs("variant launch", args, [], ["--label", "--model"])
  const primaryId = positionals[0]?.trim()
  if (!primaryId) return die("parlay variant launch: primary agent id required", EXIT_USAGE)
  const fm = readFm(primaryId)
  if (!fm.id) return die(`parlay variant launch: no known agent '${primaryId}' — run 'parlay launch' to list`, EXIT_USAGE)
  const label     = ((opts["--label"] as string | undefined)?.trim()) || autoLabel(primaryId)
  const variantId = `${primaryId}-${label}`
  if (existsSync(join(AGENTS_DIR, variantId))) return die(`parlay variant launch: variant '${variantId}' already exists — choose a different --label`, EXIT_USAGE)
  const cwd     = fm.cwd || homedir()
  const gitRoot = sh("git", ["-C", cwd, "rev-parse", "--show-toplevel"])
  if (!gitRoot.ok) return die(`parlay variant launch: '${primaryId}' cwd '${cwd}' is not in a git repo — variants require git`, EXIT_USAGE)
  // Runtime tangle backstop (task-ttza C4): before spawning another variant, alarm
  // if the PRIMARY is already stranded on a feature branch — a prior agent likely
  // branched/committed in the primary instead of its own worktree. Advisory only.
  guardRepo(mainWorktreePath(gitRoot.out) || gitRoot.out)
  mkdirSync(WKTREES_DIR, { recursive: true })
  const wkPath = join(WKTREES_DIR, variantId)
  const branch = `parlay-variant/${variantId}`
  process.stderr.write(`parlay variant launch: creating worktree ${wkPath} (branch ${branch})…\n`)
  const wt = sh("git", ["-C", gitRoot.out, "worktree", "add", wkPath, "-b", branch])
  if (!wt.ok) return die(`parlay variant launch: git worktree add failed — ${wt.err}`)
  const model      = (opts["--model"] as string | undefined)?.trim() || fm.model || ""
  const spawnArgs  = [variantId, `${fm.name} (${label})`, fm.color || "#6b7280",
    `You are ${variantId}, a variant of ${primaryId}. Your cwd is a fresh git worktree — isolated from the primary. Use your OWN scratchpad + identity; the primary's are untouched. Recovery chain: identity → handoff → scratchpad. After recovering, await the captain.`,
    "--cwd", wkPath, ...(model ? ["--model", model] : [])]
  process.stderr.write(`parlay variant launch: spawning ${variantId}…\n`)
  Bun.spawnSync(["parlay-spawn", ...spawnArgs], { stdio: ["inherit", "inherit", "inherit"] })
  const idFile = join(AGENTS_DIR, variantId, "identity.md")
  if (existsSync(idFile)) { const efm = parseFm(readFileSync(idFile, "utf-8")); efm.variant_of = primaryId; writeFm(idFile, efm) }
  console.log(`variant ${variantId} launched — worktree: ${wkPath}`)
  process.stderr.write(`merge later: parlay variant merge ${variantId}\nteardown:    parlay variant teardown ${variantId}\n`)
}

export async function cmdVariantList(args: string[]) {
  if (helpWanted("variant list", args)) return
  const { positionals } = parseArgs("variant list", args)
  const filter = positionals[0]?.trim()
  const variants: Array<{ id: string; primary: string }> = []
  if (existsSync(AGENTS_DIR)) {
    for (const d of readdirSync(AGENTS_DIR, { withFileTypes: true })) {
      if (!d.isDirectory()) continue
      const f = join(AGENTS_DIR, d.name, "identity.md")
      if (!existsSync(f)) continue
      const fm = parseFm(readFileSync(f, "utf-8"))
      if (!fm.variant_of) continue
      if (filter && fm.variant_of !== filter) continue
      variants.push({ id: d.name, primary: fm.variant_of })
    }
  }
  if (!variants.length) { console.log(filter ? `0 variants of '${filter}'` : "0 variants"); return }
  for (const v of variants) console.log(`${v.id.padEnd(24)} → ${v.primary}`)
}

export async function cmdVariantMerge(args: string[]) {
  if (helpWanted("variant merge", args)) return
  const { positionals } = parseArgs("variant merge", args)
  const variantId = positionals[0]?.trim()
  if (!variantId) return die("parlay variant merge: variant id required", EXIT_USAGE)
  const fm = readFm(variantId)
  if (!fm.variant_of) return die(`parlay variant merge: '${variantId}' is not a variant (no variant_of field)`, EXIT_USAGE)
  const pId = fm.variant_of
  const idN = mergeKind(variantId, pId, "identity")
  const spN = mergeKind(variantId, pId, "scratchpad")
  console.log(`merged ${variantId} → ${pId}: ${idN} identity fact(s), ${spN} scratchpad note(s)`)
}

export async function cmdVariantTeardown(args: string[]) {
  if (helpWanted("variant teardown", args)) return
  const { positionals, opts } = parseArgs("variant teardown", args, ["--force"])
  const variantId = positionals[0]?.trim()
  if (!variantId) return die("parlay variant teardown: variant id required", EXIT_USAGE)
  const fm = readFm(variantId)
  if (!fm.variant_of) return die(`parlay variant teardown: '${variantId}' is not a variant (no variant_of field)`, EXIT_USAGE)
  const pId = fm.variant_of
  const unId = unmergedCount(variantId, pId, "identity")
  const unSp = unmergedCount(variantId, pId, "scratchpad")
  if (unId + unSp > 0 && opts["--force"] !== true)
    return die(`parlay variant teardown: ${variantId} has ${unId} unmerged identity fact(s) + ${unSp} scratchpad note(s). Run 'parlay variant merge ${variantId}' first, or --force to discard.`, EXIT_USAGE)
  const iN = mergeKind(variantId, pId, "identity"); const sN = mergeKind(variantId, pId, "scratchpad")
  if (iN + sN > 0) console.log(`auto-merged ${iN} identity + ${sN} scratchpad into ${pId}`)
  const wkPath = join(WKTREES_DIR, variantId)
  if (existsSync(wkPath)) {
    // Tangle backstop on teardown too: check the PRIMARY (not this variant's own
    // worktree) so a stranded primary surfaces on the next fleet action. Advisory.
    const primary = mainWorktreePath(wkPath)
    if (primary) guardRepo(primary)
    const root = sh("git", ["-C", wkPath, "rev-parse", "--show-toplevel"])
    if (root.ok) { const r = sh("git", ["-C", root.out, "worktree", "remove", "--force", wkPath]); if (!r.ok) process.stderr.write(`warn: worktree remove failed — ${r.err}\n`) }
  }
  try { await postJSON<{ ok?: boolean }>("/api/chat/unregister", { id: variantId }) } catch { /* best-effort */ }
  if (existsSync(join(AGENTS_DIR, variantId))) rmSync(join(AGENTS_DIR, variantId), { recursive: true, force: true })
  console.log(`variant ${variantId} torn down`)
}

export async function cmdVariant(args: string[]) {
  if (!args.length || args[0] === "--help" || args[0] === "-h") {
    console.log("Usage: parlay variant <subcommand> ...\n  launch <primary-id> [--label <suffix>] [--model MODEL]\n  list [<primary-id>]\n  merge <variant-id>\n  teardown <variant-id> [--force]"); return
  }
  const [sub, ...rest] = args
  switch (sub) {
    case "launch":   return cmdVariantLaunch(rest)
    case "list":     return cmdVariantList(rest)
    case "merge":    return cmdVariantMerge(rest)
    case "teardown": return cmdVariantTeardown(rest)
    default: die(`parlay variant: unknown subcommand '${sub}' — try: launch, list, merge, teardown`, EXIT_USAGE)
  }
}
