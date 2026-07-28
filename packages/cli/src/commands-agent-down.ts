// parlay CLI — agent-down: general-purpose counterpart to 'variant teardown'
// (commands-variant.ts), which only unregisters git-worktree variants. Any
// spawner (e.g. firstmate's crewmate spawn/teardown) can call this on clean
// shutdown to deregister its channel, whatever kind of agent it is.

import { EXIT_USAGE } from "./config"
import { die, postJSON } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

// Thin wrapper over POST /api/chat/unregister, which already fails loud
// (non-2xx) on an unknown/already-gone id — postJSON's die() surfaces that.
export async function cmdAgentDown(args: string[]) {
  if (helpWanted("agent-down", args)) return
  const { positionals } = parseArgs("agent-down", args)
  const agentId = positionals[0]?.trim()
  if (!agentId) return die("parlay agent-down: agent id required", EXIT_USAGE)
  await postJSON<{ ok?: boolean; id?: string }>("/api/chat/unregister", { id: agentId })
  console.log(`agent ${agentId} deregistered`)
}
