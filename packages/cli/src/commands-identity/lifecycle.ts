// Self-contained identity lifecycle verbs — the ones whose id is a flag VALUE
// (so they run BEFORE memFile, which would otherwise demand PARLAY_AGENT_ID):
//   --launch <id>          reconstitute an agent from its launch spec
//   --mint-ephemeral       generate + seed a random hash identity
//   --rename <old> --to    move a store to a new id and re-register
//   --reap-ephemeral       GC idle ephemeral agents
//
// Each handler returns true if it consumed the command, false to fall through to
// the mem dispatcher. They are identity-only; a scratchpad invocation dies.

import { existsSync, mkdirSync, readFileSync, writeFileSync, readdirSync, renameSync, rmSync, statSync } from "fs"
import { join } from "path"
import { EXIT_USAGE } from "../config"
import { die, postJSON } from "../http"
import { generateEphemeralId, ephemeralIdentity } from "../identity-ephemeral"
import { agentsRoot, writeContextJson, readFrontmatter, writeFrontmatter, memFile, type MemKind } from "./store"

type Opts = Record<string, string | true>

// --launch <id>: reconstitute an agent from its identity's launch spec.
export function handleLaunch(kind: MemKind, opts: Opts): boolean {
  const launchId = (opts["--launch"] as string | undefined)?.trim()
  if (!launchId) return false
  if (kind !== "identity") return die(`parlay ${kind}: --launch is identity-only`, EXIT_USAGE)
  const fm = readFrontmatter(memFile("identity", launchId).file)
  const id = fm.id || launchId
  const name = fm.name || id
  const color = fm.color || "#6b7280"
  const cwd = fm.cwd || process.cwd()
  const model = fm.model || ""
  const recovery = `You are ${id}, restarted with a FRESH context after a context reset. Before anything else, recover yourself: run 'identity' (it shows a pinned handoff pointer), then 'handoff show <that-id>' for full state, then 'scratchpad' for your working notes. Then re-enroll, tell the captain via 'reply' that you are back after a context reset, and resume where you left off.`
  const spawnArgs = [id, name, color, recovery, "--cwd", cwd, ...(model ? ["--model", model] : [])]
  if (opts["--dry"] === true) { console.log(`identity --launch ${id} [dry] → parlay-spawn ${spawnArgs.map(a => JSON.stringify(a)).join(" ")}`); return true }
  const { spawnSync } = require("child_process") as typeof import("child_process")
  const r = spawnSync("parlay-spawn", spawnArgs, { stdio: "inherit" })
  if (r.error) return die(`identity --launch: parlay-spawn failed — ${r.error.message}`)
  return true
}

// --mint-ephemeral: generate a hash identity, seed its store (context.json +
// identity.md with ephemeral: true after cwd), print a TAB-separated
// "<id>\t<name>\t<color>" line for parlay-spawn (name contains a space).
export function handleMintEphemeral(kind: MemKind, opts: Opts): boolean {
  if (!opts["--mint-ephemeral"]) return false
  if (kind !== "identity") return die(`parlay ${kind}: --mint-ephemeral is identity-only`, EXIT_USAGE)
  const root = agentsRoot()
  const id = generateEphemeralId((candidate) => existsSync(join(root, candidate)))
  if (existsSync(join(root, id))) return die(`identity --mint-ephemeral: id collision on ${id} — retry`, EXIT_USAGE)
  const { name, color } = ephemeralIdentity(id)
  const dir = join(root, id)
  mkdirSync(dir, { recursive: true }) // writeFrontmatter does not create parents
  const fm: Record<string, string> = { id, name, color }
  const cwd = (opts["--cwd"] as string | undefined)?.trim()
  const model = (opts["--model"] as string | undefined)?.trim()
  if (model) fm.model = model
  if (cwd) fm.cwd = cwd
  fm.ephemeral = "true"
  writeFrontmatter(join(dir, "identity.md"), fm)
  writeContextJson(dir, { id, name, color })
  console.log(`${id}\t${name}\t${color}`)
  return true
}

// --rename <old-id> --to <new-id>: move the store, rewrite id in context.json +
// frontmatter, apply overrides, re-register with the server, log a reincarnation.
export async function handleRename(kind: MemKind, opts: Opts): Promise<boolean> {
  const renameOld = (opts["--rename"] as string | undefined)?.trim()
  if (!renameOld) return false
  if (kind !== "identity") return die(`parlay ${kind}: --rename is identity-only`, EXIT_USAGE)
  const newId = (opts["--to"] as string | undefined)?.trim()
  if (!newId) return die(`parlay identity --rename: --to <new-id> is required`, EXIT_USAGE)
  if (newId === renameOld) return die(`parlay identity --rename: --to must differ from <old-id>`, EXIT_USAGE)
  const root = agentsRoot()
  const oldDir = join(root, renameOld)
  const newDir = join(root, newId)
  if (!existsSync(oldDir)) return die(`parlay identity --rename: no agent store at ${oldDir}`, EXIT_USAGE)
  // Guard against clobbering an existing target id.
  if (existsSync(newDir)) return die(`parlay identity --rename: target id already exists (${newDir}) — refusing to clobber`, EXIT_USAGE)

  // 1. Move the store directory.
  renameSync(oldDir, newDir)

  // Resolve effective name/color: overrides win, else the moved fm/context.json.
  const idFile = join(newDir, "identity.md")
  const fm = readFrontmatter(idFile)
  const ctxPath = join(newDir, "context.json")
  let prevCtx: { id?: string; name?: string; color?: string } = {}
  if (existsSync(ctxPath)) {
    try { prevCtx = JSON.parse(readFileSync(ctxPath, "utf8")) } catch { prevCtx = {} }
  }
  const nameOverride = (opts["--name"] as string | undefined)?.trim()
  const colorOverride = (opts["--color"] as string | undefined)?.trim()
  const cwdOverride = (opts["--cwd"] as string | undefined)?.trim()
  const modelOverride = (opts["--model"] as string | undefined)?.trim()
  const finalName = nameOverride ?? fm.name ?? prevCtx.name
  const finalColor = colorOverride ?? fm.color ?? prevCtx.color

  // 2. Rewrite context.json with the new id + any name/color overrides.
  writeContextJson(newDir, { id: newId, name: finalName, color: finalColor })

  // 3. Rewrite identity.md frontmatter: new id + provided field overrides.
  fm.id = newId
  if (nameOverride) fm.name = nameOverride
  if (colorOverride) fm.color = colorOverride
  if (cwdOverride) fm.cwd = cwdOverride
  if (modelOverride) fm.model = modelOverride
  // --preserve: adopt an ephemeral into a durable identity — drop the marker so
  // it is no longer reaped as un-adopted.
  if (opts["--preserve"]) delete fm.ephemeral
  writeFrontmatter(idFile, fm)

  // 4. Re-register with the server under the new id/name/color.
  await postJSON<{ ok?: boolean; error?: string }>("/api/chat/register-agent", {
    id: newId,
    ...(finalName ? { name: finalName } : {}),
    ...(finalColor ? { color: finalColor } : {}),
  })

  // 5. Log the rename into reincarnations.log if the agent keeps one.
  const reincLog = join(newDir, "reincarnations.log")
  if (existsSync(reincLog)) {
    const existing = readFileSync(reincLog, "utf8")
    const entry = JSON.stringify({ ts: new Date().toISOString(), event: "renamed", from: renameOld, to: newId })
    writeFileSync(reincLog, existing.trimEnd() + "\n" + entry + "\n")
  }

  console.log(`identity renamed: ${renameOld} → ${newId} (store moved, re-registered; relaunch with: parlay identity --launch ${newId})`)
  return true
}

// --reap-ephemeral [--older-than <hours>h] [--dry]: GC ephemeral agents whose
// identity.md is idle past the window (default 24h).
export function handleReapEphemeral(kind: MemKind, opts: Opts): boolean {
  if (!opts["--reap-ephemeral"]) return false
  if (kind !== "identity") return die(`parlay ${kind}: --reap-ephemeral is identity-only`, EXIT_USAGE)
  const raw = ((opts["--older-than"] as string | undefined) ?? "24h").trim()
  const m = raw.match(/^(\d+(?:\.\d+)?)h?$/i)
  if (!m) return die(`parlay identity --reap-ephemeral: --older-than must look like '24h' (got '${raw}')`, EXIT_USAGE)
  const hours = parseFloat(m[1])
  const cutoffMs = Date.now() - hours * 3600_000
  const dry = opts["--dry"] === true
  const root = agentsRoot()
  if (!existsSync(root)) { console.log(`identity --reap-ephemeral: no agent store at ${root}`); return true }

  const reaped: string[] = []
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue
    const dir = join(root, entry.name)
    const idFile = join(dir, "identity.md")
    if (!existsSync(idFile)) continue
    if (readFrontmatter(idFile).ephemeral !== "true") continue
    const mtime = statSync(idFile).mtimeMs
    if (mtime > cutoffMs) continue // touched recently — keep
    const ageH = ((Date.now() - mtime) / 3600_000).toFixed(1)
    console.log(`${dry ? "would reap" : "reaping"}: ${entry.name} (idle ${ageH}h)`)
    if (!dry) rmSync(dir, { recursive: true, force: true })
    reaped.push(entry.name)
  }
  console.log(`identity --reap-ephemeral: ${reaped.length} ephemeral${reaped.length === 1 ? "" : "s"} ${dry ? "would be reaped" : "reaped"} (older than ${hours}h)`)
  return true
}
