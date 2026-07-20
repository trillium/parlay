// The scratchpad/identity dispatcher. Self-contained lifecycle verbs (launch,
// mint, rename, reap) run first via ./lifecycle; the remaining agent-scoped
// verbs (register, handoff/submit, complete) and the read/append default live
// here. `cmdScratchpad` / `cmdIdentity` are the exported entry points.

import { existsSync, readFileSync, writeFileSync, appendFileSync } from "fs"
import { join } from "path"
import { EXIT_USAGE } from "../config"
import { die, postJSON } from "../http"
import { parseArgs } from "../args"
import { helpWanted } from "../help"
import { resolveCurrentHandoff } from "../resolve-handoff"
import {
  agentsRoot, writeContextJson, contextResetCmd, memFile, readFrontmatter, writeFrontmatter,
  MEM_BOOL_FLAGS, MEM_VALUE_FLAGS, type MemKind,
} from "./store"
import { handleLaunch, handleMintEphemeral, handleRename, handleReapEphemeral } from "./lifecycle"

async function cmdMem(kind: MemKind, args: string[]) {
  if (helpWanted(kind, args)) return
  // --handoff / --submit are BOOLEAN: their handoff id is OPTIONAL (a positional).
  // Bare `identity --submit` resolves the current open handoff from the store, so
  // `handoff create … && identity --submit` is one atomic act. --complete stays a
  // value flag: auto-guessing which work item to close would be destructive.
  const { positionals, opts } = parseArgs(kind, args, MEM_BOOL_FLAGS, MEM_VALUE_FLAGS)

  // Self-contained lifecycle verbs (id is a flag value) run BEFORE memFile, which
  // would otherwise demand an --agent / PARLAY_AGENT_ID.
  if (handleLaunch(kind, opts)) return
  if (handleMintEphemeral(kind, opts)) return
  if (await handleRename(kind, opts)) return
  if (handleReapEphemeral(kind, opts)) return

  const { agent, file } = memFile(kind, opts["--agent"] as string | undefined)

  if (opts["--path"]) { console.log(file); return }
  if (opts["--clear"]) { writeFileSync(file, ""); console.log(`${kind} cleared for ${agent}`); return }

  // --register: seed/update this identity's launch spec (frontmatter: id/name/
  // color/model/cwd). parlay-spawn calls this so the identity fully describes how
  // to relaunch the agent. Facts and the handoff pointer below it are preserved.
  if (opts["--register"]) {
    if (kind !== "identity") return die(`parlay ${kind}: --register is identity-only`, EXIT_USAGE)
    const fm = readFrontmatter(file)
    fm.id = agent
    for (const k of ["name", "color", "model", "cwd"] as const) {
      const v = (opts[`--${k}`] as string | undefined)?.trim()
      if (v) fm[k] = v
    }
    // --ephemeral marks a hash-identity agent. The field lands after cwd so the
    // frontmatter reads id/name/color/model/cwd/ephemeral.
    if (opts["--ephemeral"]) fm.ephemeral = "true"
    writeFrontmatter(file, fm)
    // context.json is the panel's reply-attribution record — write it for EVERY
    // registered id so attribution never depends on a prior server round-trip.
    writeContextJson(join(agentsRoot(), agent), { id: agent, name: fm.name, color: fm.color })
    console.log(`identity registered launch spec for ${agent} (${Object.keys(fm).filter(k => k !== "id").join(", ") || "id only"})`)
    return
  }

  // --handoff [<id>]: pin a pointer to the agent's current handoff bead at the top
  //   of the file, so a reset agent knows which handoff holds its full state. Pin
  //   only — does not restart.
  // --submit [<id>]: pin the pointer AND trigger a context reset — the handoff act
  //   itself restarts the agent (kills this session, relaunches recovering via
  //   identity → handoff → scratchpad). Add --dry to preview without killing.
  // The id is OPTIONAL for both: given as a positional, else auto-resolved from the
  //   handoff store's current open bead — closing the create→submit death window.
  const wantHandoff = opts["--handoff"] === true
  const wantSubmit  = opts["--submit"]  === true
  if (wantHandoff || wantSubmit) {
    const submitId = wantSubmit
    if (submitId && kind !== "identity") return die(`parlay ${kind}: --submit is identity-only`, EXIT_USAGE)
    // Id precedence: explicit positional, else this agent's newest open handoff.
    const pinId = (positionals[0]?.trim()) || resolveCurrentHandoff(undefined, agent)
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
    const dry = opts["--dry"] === true
    console.log(`identity submitted for ${agent} — handoff ${pinId} pinned; ${dry ? "previewing" : "triggering"} context reset…`)
    const { spawnSync } = require("child_process") as typeof import("child_process")
    const cmd = contextResetCmd()
    const res = spawnSync(cmd, dry ? ["--reboot", "--dry"] : ["--reboot"], { stdio: "inherit" })
    if (res.error) return die(`identity --submit: could not run ${cmd} — ${res.error.message}`)
    return
  }

  // --complete <store-item>: a SINGLE-USE agent signals its work is done and ENDS
  // for good — no context reset. Closes the federated store item (prefix = store,
  // e.g. task-abc → `task close task-abc`), then terminates. Add --dry to preview.
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
    // Hide the launch-spec frontmatter (machine-facing); show the human identity.
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
