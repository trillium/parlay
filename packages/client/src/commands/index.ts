import { registerCommand, listCommands, runCommandPass, setCommandContext } from './registry'
import { registerBuiltins } from './builtins'
import { buildContext } from './ctx'

export { registerCommand, listCommands, runCommandPass }
export type { Command, CommandContext, CommandMatch, MatchMode } from './types'

// Wire the subsystem: build the ctx, register built-ins, expose the public
// extension point for standalone parlay-agent embedders.
export function initCommands(): void {
  setCommandContext(buildContext())
  registerBuiltins()
  const pub = ((window as any).__parlay ??= {})
  pub.registerCommand = registerCommand
  pub.listCommands = listCommands
}
