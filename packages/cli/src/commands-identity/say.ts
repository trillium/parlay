// `parlay say` / `parlay reply` — reply to YOUR OWN channel. Routes off
// PARLAY_AGENT_ID (parlay-spawn sets it), so no url/id/name/color/JSON — just
// the text. The server keeps the agent's registered name/color. Text comes from
// args, or stdin when no args are given (so long/multi-line replies pipe in).

import { EXIT_USAGE } from "../config"
import { die, postJSON } from "../http"
import { parseArgs } from "../args"
import { helpWanted } from "../help"

export async function cmdSay(args: string[]) {
  if (helpWanted("say", args)) return
  const { positionals, opts } = parseArgs("say", args, [], ["--agent"])
  const agent = (((opts["--agent"] as string | undefined) ?? process.env.PARLAY_AGENT_ID) ?? "").trim()
  if (!agent) return die("parlay say: no agent identity — run inside a parlay-spawn'd agent (it sets PARLAY_AGENT_ID) or pass --agent <id>", EXIT_USAGE)
  let text = positionals.join(" ").trim()
  if (!text && !process.stdin.isTTY) text = (await Bun.stdin.text()).trim()
  if (!text) return die("parlay say: message text required (as arguments or piped on stdin)", EXIT_USAGE)
  const r = await postJSON<{ ok?: boolean; id?: string; error?: string }>("/api/chat/reply", { text, agent })
  if (r.error) return die(`say failed: ${r.error}`)
  console.log(`said as ${agent} (id ${r.id})`)
}
