// parlay nickname — assign one or more voice nicknames to an agent so the
// channel picker (and voice channel-switch) can resolve it by an easy spoken
// word. This is the missing management surface: nicknames were only settable
// as a side effect of a message; register-agent is the metadata-only upsert.
//   parlay nickname <nick> [<nick2> …]     set nicknames for THIS agent (PARLAY_AGENT_ID)
//   parlay nickname --agent <id> <nick> …  set nicknames for a named agent
//   parlay nickname --agent <id> --clear   remove all nicknames

import { EXIT_USAGE } from "./config"
import { die, postJSON } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

export async function cmdNickname(args: string[]) {
  if (helpWanted("nickname", args)) return
  const { opts, positionals } = parseArgs("nickname", args, ["--clear"], ["--agent"])
  const id = (opts["--agent"] as string | undefined)?.trim() || (process.env.PARLAY_AGENT_ID ?? "").trim()
  if (!id) return die("parlay nickname: no agent — pass --agent <id> or set PARLAY_AGENT_ID", EXIT_USAGE)
  const clear = opts["--clear"] === true
  const nicknames = clear ? [] : positionals.map(s => s.trim()).filter(Boolean)
  if (!clear && nicknames.length === 0) return die("parlay nickname: give at least one nickname (or --clear)", EXIT_USAGE)
  const r = await postJSON<{ ok?: boolean; id?: string; nicknames?: string[]; error?: string }>("/api/chat/register-agent", { id, nicknames })
  if (r.error) return die(`parlay nickname: ${r.error}`)
  console.log(clear
    ? `cleared nicknames for ${id}`
    : `${id} nicknames: ${(r.nicknames ?? nicknames).join(", ")}`)
}
