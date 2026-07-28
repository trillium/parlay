#!/usr/bin/env bun
// parlay — CLI for talking to a Parlay chat server.
// Server URL from PARLAY_SERVER (default http://localhost:4242).
//
// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).
//
// This file is only the command dispatcher; each concern lives in its own module:
//   config.ts    server URL + exit codes        args.ts     flag parser
//   http.ts      fetch helpers + die()          format.ts   message rendering
//   types.ts     wire shapes                    help.ts     usage + per-command help
//   commands.ts  one handler per subcommand     monitor.ts  relay-backed monitor

import { EXIT_USAGE } from "./config"
import { die } from "./http"
import { USAGE } from "./help"
import {
  cmdAgents,
  cmdAlert,
  cmdHistory,
  cmdLavishImport,
  cmdLaunch,
  cmdMonitor,
  cmdSend,
  cmdStats,
  cmdStatus,
  cmdSubscribers,
} from "./commands"
import { cmdListen } from "./listen"
import { cmdStatusVerb } from "./commands-status"
import { cmdNickname } from "./commands-nickname"
import { cmdIdentity, cmdSay, cmdScratchpad } from "./commands-identity"
import { cmdVariant } from "./commands-variant"
import { cmdGuard } from "./commands-guard"
import { cmdDoctor, cmdHealth } from "./commands-doctor"
import { cmdContextCheck } from "./commands-context-check"
import { cmdRobotsWatch } from "./commands-robots-watch"
import { cmdRobotsTail } from "./commands-robots-watch/tail"

async function main() {
  const [cmd, ...args] = process.argv.slice(2)
  switch (cmd) {
    case undefined:       return cmdStatus()          // bare `parlay` = panel/fleet snapshot
    case "status":        return cmdStatusVerb(args)  // fold §3.6 keyed status verb (was a redundant alias of bare `parlay`; retired → task-ve2v)
    case "subscribers":   return cmdSubscribers(args)
    case "agents":        return cmdAgents(args)
    case "nickname":      return cmdNickname(args)
    case "send":          return cmdSend(args)
    case "say":
    case "reply":         return cmdSay(args)
    case "scratchpad":    return cmdScratchpad(args)
    case "identity":      return cmdIdentity(args)
    case "alert":         return cmdAlert(args)
    case "history":       return cmdHistory(args)
    case "monitor":       return cmdMonitor(args)
    case "listen":
    case "agent-up":      return cmdListen(args)
    case "stats":         return cmdStats(args)
    case "doctor":        return cmdDoctor(args)
    case "context-check": return cmdContextCheck(args)
    case "robots-watch":  return cmdRobotsWatch(args)
    case "robots-tail":   return cmdRobotsTail(args)
    case "health":        return cmdHealth(args)
    case "launch":        return cmdLaunch(args)
    case "variant":       return cmdVariant(args)
    case "guard":         return cmdGuard(args)
    case "lavish-import": return cmdLavishImport(args)
    case "help":
    case "--help":
    case "-h":            console.log(USAGE); return
    default:
      die(`parlay: unknown command or flag "${cmd}" — run 'parlay help' for usage`, EXIT_USAGE)
  }
}

main()
