import { registerCommand, listCommands, runCommandPass, setCommandContext, passCount, compileCount } from './registry'
import { registerBuiltins } from './builtins'
import { buildContext } from './ctx'
import { setDispatcherContext, telemetry } from './dispatcher'

export { registerCommand, listCommands, runCommandPass }
export { setDispatcherContext, applyEnvelope, scheduleEval, bumpInputVersion, currentInputVersion, telemetry, renderOverlay } from './dispatcher'
export type { Action, ActionEnvelope, ApplyResult } from './dispatcher'
export type { Command, CommandContext, CommandMatch, MatchMode } from './types'

// Wire the subsystem: build the ctx, register built-ins, expose the public
// extension point for standalone parlay-agent embedders.
export function initCommands(): void {
  const ctx = buildContext()
  setCommandContext(ctx)
  // The server-eval dispatcher applies server-computed actions through the SAME
  // CommandContext the local commands use (feat/server-side-eval).
  setDispatcherContext(ctx)
  registerBuiltins()
  const pub = ((window as any).__parlay ??= {})
  pub.registerCommand = registerCommand
  pub.listCommands = listCommands
  pub.perf = { passes: passCount, compiles: compileCount }   // #20 verification/debug
  pub.evalTelemetry = () => ({ ...telemetry })               // server-eval observe surface
}
