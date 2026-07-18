import { mkdirSync, existsSync, readFileSync, writeFileSync, appendFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { spawnSync as _spawnSync } from "child_process"
import { EXIT_USAGE } from "./config"
import { die, postJSON } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"
import { resolveCurrentHandoff } from "./resolve-handoff"

// The self-restart script was renamed reincarnate → context-reset. The new name is
// not yet on PATH everywhere (only ~/.local/bin/reincarnate is, and that now forwards
// to bin/context-reset via a back-compat wrapper). To keep behavior IDENTICAL today
// while moving call sites to the new name, resolve it lazily: prefer `context-reset`
// if it is resolvable on PATH, otherwise fall back to the legacy `reincarnate` name
// (which is on PATH and execs context-reset). Once a `~/.local/bin/context-reset`
// symlink is deployed, the new name wins with no code change. See the FORGE report /
// deploy note for the required symlink step.
function contextResetCmd(): string {
  const probe = _spawnSync("command", ["-v", "context-reset"], { shell: true, stdio: "ignore" })
  return probe.status === 0 ? "context-reset" : "reincarnate"
}

// Identity routes off PARLAY_AGENT_ID (parlay-spawn sets it in every spawned agent),
// so no url/id/name/color/JSON — just the text. The server keeps the agent's
// registered name/color. Text comes from args, or stdin when no args are given
// (so long or multi-line replies pipe in cleanly).
export async function cmdSay(args: string[]) {
  if (helpWanted("say", args)) return
  const { positionals, opts } = parseArgs("say", args, [], ["--agent"])
  const agent = (((opts["--agent"] as string | undefined) ?? process.env.PARLAY_AGENT_ID) ?? "").trim()
  if (!agent) return die("parlay say: no agent identity — run inside a parlay-spawn'd agent (it sets PARLAY_AGENT_ID) or pass --agent <id>", EXIT_USAGE)
  let text = positionals.join(" ").trim()
  if (!text && !process.stdin.isTTY) text = (await Bun.stdin.text()).trim()
  if (!text) return die("parlay say: message text required (as arguments or piped on stdin)", EXIT_USAGE)

  // Auto-convert localhost URLs to MacBook (Tailscale) equivalents so the captain can open them on his phone.
  // Matches http://localhost:XXXX/... or http://127.0.0.1:XXXX/... and replaces with macbook:XXXX/...
  text = text.replace(/https?:\/\/(localhost|127\.0\.0\.1):(\d+)(\/[^\s]*)?/g, "macbook:$2$3")

  const r = await postJSON<{ ok?: boolean; id?: string; error?: string }>("/api/chat/reply", { text, agent })
  if (r.error) return die(`say failed: ${r.error}`)
  console.log(`said as ${agent} (id ${r.id})`)
}

// ── Per-agent durable memory: scratchpad (task notes) + identity (self-knowledge) ──
// Local files under ${PARLAY_AGENT_HOME:-~/.parlay/agents}/<id>/, keyed by
// PARLAY_AGENT_ID. No server call. The bare `scratchpad` / `identity` commands
// are thin wrappers over these subcommands — one tool under the hood.
function memFile(kind: "scratchpad" | "identity", agentOverride?: string): { agent: string; file: string } {
  const agent = ((agentOverride ?? process.env.PARLAY_AGENT_ID) ?? "").trim()
  if (!agent) die(`parlay ${kind}: no agent identity — run inside a parlay-spawn'd agent (sets PARLAY_AGENT_ID) or pass --agent <id>`, EXIT_USAGE)
  const base = process.env.PARLAY_AGENT_HOME || join(homedir(), ".parlay", "agents")
  const dir = join(base, agent)
  mkdirSync(dir, { recursive: true })
  return { agent, file: join(dir, `${kind}.md`) }
}

// Identity frontmatter IS the agent's launch spec (id, name, color, model, cwd).
// parlay-spawn seeds it via `identity --register`; `identity --launch <id>` reads it
// and reconstitutes the agent from ONE template. Relaunch = "run the identity".
function readFrontmatter(file: string): Record<string, string> {
  const txt = existsSync(file) ? readFileSync(file, "utf8") : ""
  const m = txt.match(/^---\n([\s\S]*?)\n---\n/)
  const fm: Record<string, string> = {}
  if (m) for (const line of m[1].split("\n")) {
    const i = line.indexOf(":")
    if (i > 0) fm[line.slice(0, i).trim()] = line.slice(i + 1).trim().replace(/^["']|["']$/g, "")
  }
  return fm
}
function writeFrontmatter(file: string, fm: Record<string, string>): void {
  const txt = existsSync(file) ? readFileSync(file, "utf8") : ""
  const rest = txt.replace(/^---\n[\s\S]*?\n---\n/, "")
  const lines = Object.entries(fm).filter(([, v]) => v).map(([k, v]) => `${k}: ${/[:#'"\s]/.test(v) ? JSON.stringify(v) : v}`)
  writeFileSync(file, `---\n${lines.join("\n")}\n---\n${rest || `# Identity — ${fm.id ?? ""}\n\n`}`)
}

async function cmdMem(kind: "scratchpad" | "identity", args: string[]) {
  if (helpWanted(kind, args)) return
  // --handoff / --submit are BOOLEAN: their handoff id is OPTIONAL (given as a
  // positional). Bare `identity --submit` resolves the current open handoff from the
  // store, so `handoff create … && identity --submit` is one atomic act with nothing
  // interposed, and a stranded create is recovered by a bare submit. --complete stays
  // a value flag: auto-guessing which work item to close would be destructive.
  const { positionals, opts } = parseArgs(kind, args, ["--clear", "--path", "--dry", "--register", "--handoff", "--submit"], ["--agent", "--complete", "--launch", "--name", "--color", "--model", "--cwd"])

  // --launch <id>: reconstitute an agent from its identity's launch spec (frontmatter)
  // via ONE template — "run the identity". Self-contained (the id is the value), so it
  // runs BEFORE memFile, which would otherwise demand an --agent. --dry previews.
  const launchId = (opts["--launch"] as string | undefined)?.trim()
  if (launchId) {
    if (kind !== "identity") return die(`parlay ${kind}: --launch is identity-only`, EXIT_USAGE)
    const fm = readFrontmatter(memFile("identity", launchId).file)
    const id = fm.id || launchId
    const name = fm.name || id
    const color = fm.color || "#6b7280"
    const cwd = fm.cwd || process.cwd()
    const model = fm.model || ""
    const recovery = `You are ${id}, restarted with a FRESH context after a context reset. Before anything else, recover yourself: run 'identity' (it shows a pinned handoff pointer), then 'handoff show <that-id>' for full state, then 'scratchpad' for your working notes. Then re-enroll, tell the captain via 'reply' that you are back after a context reset, and resume where you left off.`
    const spawnArgs = [id, name, color, recovery, "--cwd", cwd, ...(model ? ["--model", model] : [])]
    if (opts["--dry"] === true) { console.log(`identity --launch ${id} [dry] → parlay-spawn ${spawnArgs.map(a => JSON.stringify(a)).join(" ")}`); return }
    const { spawnSync } = require("child_process") as typeof import("child_process")
    const r = spawnSync("parlay-spawn", spawnArgs, { stdio: "inherit" })
    if (r.error) return die(`identity --launch: parlay-spawn failed — ${r.error.message}`)
    return
  }

  const { agent, file } = memFile(kind, opts["--agent"] as string | undefined)

  if (opts["--path"]) { console.log(file); return }
  if (opts["--clear"]) { writeFileSync(file, ""); console.log(`${kind} cleared for ${agent}`); return }

  // --register: seed/update this identity's launch spec (frontmatter: id/name/color/
  // model/cwd). parlay-spawn calls this at spawn so the identity fully describes how
  // to relaunch the agent. Facts and the handoff pointer below it are preserved.
  if (opts["--register"]) {
    if (kind !== "identity") return die(`parlay ${kind}: --register is identity-only`, EXIT_USAGE)
    const fm = readFrontmatter(file)
    fm.id = agent
    for (const k of ["name", "color", "model", "cwd"] as const) {
      const v = (opts[`--${k}`] as string | undefined)?.trim()
      if (v) fm[k] = v
    }
    writeFrontmatter(file, fm)
    console.log(`identity registered launch spec for ${agent} (${Object.keys(fm).filter(k => k !== "id").join(", ") || "id only"})`)
    return
  }

  // --handoff [<id>]: pin a pointer to the agent's current handoff bead at the top of
  //   the file, so a reset agent reading its identity knows which handoff holds its
  //   full session state (`handoff show <id>`). Pin only — does not restart.
  // --submit [<id>]: pin the pointer AND trigger a context reset — the handoff act itself
  //   restarts the agent. It spawns an external watcher, kills this session, verifies
  //   it closed, and relaunches with a recovering context (identity → handoff →
  //   scratchpad). No separate sudoku/context-reset step; submitting the handoff IS the
  //   shutdown. Add --dry to pin + preview the context reset without killing anything.
  // The id is OPTIONAL for both: given as a positional, else auto-resolved from the
  //   handoff store's current open bead. That closes the create→submit death window —
  //   `handoff create … && identity --submit` is atomic with nothing to interpose, and
  //   a submit stranded after a bare create still finds and pins its own handoff.
  const wantHandoff = opts["--handoff"] === true
  const wantSubmit  = opts["--submit"]  === true
  if (wantHandoff || wantSubmit) {
    const submitId = wantSubmit
    if (submitId && kind !== "identity") return die(`parlay ${kind}: --submit is identity-only`, EXIT_USAGE)
    // Id precedence: explicit positional, else the store's current open handoff.
    const pinId = (positionals[0]?.trim()) || resolveCurrentHandoff()
    if (!pinId) return die(
      `parlay ${kind}: no handoff id given and none active in the handoff store — create one first (handoff create …) or pass the id`,
      EXIT_USAGE,
    )
    const header = `# ${kind === "identity" ? "Identity" : "Scratchpad"} — ${agent}`
    const marker = "> 📎 Handoff:"
    const pointer = `${marker} ${pinId} — run \`handoff show ${pinId}\` for full session state`
    const body = (existsSync(file) ? readFileSync(file, "utf8") : "").split("\n").filter(l => !l.startsWith(marker))
    let h = body.findIndex(l => l.startsWith("# "))   // the "# Identity" header — may sit below frontmatter
    if (h < 0) { body.unshift(header, ""); h = 0 }
    const at = body[h + 1]?.trim() === "" ? h + 2 : h + 1
    body.splice(at, 0, pointer, "")
    writeFileSync(file, body.join("\n").replace(/\n{3,}/g, "\n\n"))
    if (!submitId) { console.log(`${kind} handoff pointer set for ${agent} → ${pinId}`); return }
    // Submit: pointer pinned — now trigger a context reset (this ends the session).
    const dry = opts["--dry"] === true
    console.log(`identity submitted for ${agent} — handoff ${pinId} pinned; ${dry ? "previewing" : "triggering"} context reset…`)
    const { spawnSync } = require("child_process") as typeof import("child_process")
    const cmd = contextResetCmd()
    const res = spawnSync(cmd, dry ? ["--reboot", "--dry"] : ["--reboot"], { stdio: "inherit" })
    if (res.error) return die(`identity --submit: could not run ${cmd} — ${res.error.message}`)
    return
  }

  // --complete <store-item>: a SINGLE-USE agent signals its work is done and ENDS
  // for good — no context reset. It closes the federated store item it was working
  // (the store is the item's prefix, e.g. task-abc → `task close task-abc`), then
  // terminates the session (context-reset with no --reboot = verified clean shutdown).
  // The counterpart to --submit: --submit restarts a persistent agent; --complete
  // ends a single-use one. Add --dry to preview without closing or killing.
  const completeId = (opts["--complete"] as string | undefined)?.trim()
  if (completeId) {
    if (kind !== "identity") return die(`parlay ${kind}: --complete is identity-only`, EXIT_USAGE)
    const store = completeId.split("-")[0]
    const dry = opts["--dry"] === true
    const { spawnSync } = require("child_process") as typeof import("child_process")
    console.log(`identity --complete: ${agent} finished — closing ${completeId} in '${store}' store${dry ? " [dry]" : ""}…`)
    if (!dry) {
      const c = spawnSync(store, ["close", completeId], { stdio: "inherit" })
      if (c.error || c.status !== 0) console.log(`  (warn: could not close ${completeId} — ${c.error?.message ?? "exit " + c.status}; ending anyway)`)
    }
    console.log(`identity --complete: single-use agent ending, no restart${dry ? " [dry — not killing]" : ""}…`)
    if (!dry) {
      const cmd = contextResetCmd()
      const r = spawnSync(cmd, [], { stdio: "inherit" })  // no --reboot → verify shutdown only, then sudoku
      if (r.error) return die(`identity --complete: could not run ${cmd} — ${r.error.message}`)
    }
    return
  }

  const first = positionals[0]
  const readMode = positionals.length === 0 || first === "show" || first === "read"
  if (readMode) {
    // Hide the launch-spec frontmatter (machine-facing); show the human/agent identity.
    const body = (existsSync(file) ? readFileSync(file, "utf8") : "").replace(/^---\n[\s\S]*?\n---\n/, "").trimEnd()
    if (body) console.log(body)
    else console.log(kind === "identity"
      ? `(no identity recorded yet for ${agent} — add with: identity 'a fact about yourself')`
      : `(scratchpad empty for ${agent} — write with: scratchpad 'note')`)
    return
  }

  let text = positionals.join(" ").trim()
  if (!text && !process.stdin.isTTY) text = (await Bun.stdin.text()).trim()
  if (!text) return die(`parlay ${kind}: nothing to ${kind === "identity" ? "add" : "write"} (args or stdin)`, EXIT_USAGE)

  const existing = existsSync(file) ? readFileSync(file, "utf8") : ""
  if (!existing.trim()) writeFileSync(file, `# ${kind === "identity" ? "Identity" : "Scratchpad"} — ${agent}\n\n`)
  const stamp = kind === "identity"
    ? new Date().toLocaleDateString("sv-SE")
    : new Date().toLocaleString("sv-SE").slice(0, 16)
  appendFileSync(file, `- [${stamp}] ${text}\n`)
  const count = (readFileSync(file, "utf8").match(/^- \[/gm) || []).length
  console.log(`${kind} += ${agent} (${count} ${kind === "identity" ? "facts" : "notes"})`)
}

export const cmdScratchpad = (args: string[]) => cmdMem("scratchpad", args)
export const cmdIdentity = (args: string[]) => cmdMem("identity", args)
