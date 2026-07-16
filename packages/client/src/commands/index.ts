import { registerCommand, listCommands, setCommandContext, passCount, compileCount } from './registry'
import { registerBuiltins } from './builtins'
import { buildContext } from './ctx'
import { setDispatcherContext, telemetry } from './dispatcher'

export { registerCommand, listCommands }
export { setDispatcherContext, applyEnvelope, scheduleEval, bumpInputVersion, currentInputVersion, telemetry, renderOverlay } from './dispatcher'
export type { Action, ActionEnvelope, ApplyResult } from './dispatcher'
export type { Command, CommandContext, CommandMatch, MatchMode } from './types'

// Wire the subsystem: build the ctx, register built-ins, expose the public
// extension point for standalone parlay-agent embedders.
export function initCommands(): void {
  const ctx = buildContext()
  setCommandContext(ctx)
  // The server-eval dispatcher applies server-computed actions through the SAME
  // CommandContext the command registry uses.
  setDispatcherContext(ctx)
  registerBuiltins()
  const pub = ((window as any).__parlay ??= {})
  pub.registerCommand = registerCommand
  pub.listCommands = listCommands
  pub.perf = { passes: passCount, compiles: compileCount }   // #20 verification/debug
  pub.evalTelemetry = () => ({ ...telemetry })               // server-eval observe surface

  // Input registry — page scripts call window.__paRegisterInput (from parlay-ui.js)
  // to enroll inputs; this map is the live roster for future command routing.
  const registeredInputs = new Map<string, { el: HTMLElement; opts: Record<string, unknown> }>()
  pub.registerInput = (id: string, el: HTMLElement, opts: Record<string, unknown> = {}) => {
    if (!id || !el) return
    registeredInputs.set(id, { el, opts })
    el.dataset.parlayInputId = id
  }
  pub.registeredInputs = registeredInputs
  // Drain any inputs queued by parlay-ui.js before the panel finished loading.
  ;(window as any).__paOnParlay?.(pub)
}
