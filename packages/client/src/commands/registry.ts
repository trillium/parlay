import type { Command, CommandContext, CommandMatch, MatchMode } from './types'
import { getSettingsVersion } from '../settings-modal/io'

// ── Registry + matching engine ───────────────────────────────────────────────
// One watcher normalizes the buffer and runs registered commands by priority;
// first match wins. Phrase → regex building carries the house dictation
// tolerance everywhere: punctuation/commas allowed between phrase words,
// interior words of ≤3 chars optional (dictation drops them), case-insensitive.
// {slot} tokens become lazy named captures validated by the command's action.

const SEP = '[\\s,.!?;:]+'
const BOUND = '[\\s,.!?;:]'

const commands: Command[] = []
let _ctx: CommandContext | null = null

export function registerCommand(cmd: Command): void {
  const i = commands.findIndex(c => c.id === cmd.id)
  if (i !== -1) commands.splice(i, 1)   // re-register replaces (idempotent)
  commands.push(cmd)
  commands.sort((a, b) => a.priority - b.priority)
  matcherCache.delete(cmd.id)
}

export function listCommands(): Command[] { return [...commands] }

export function setCommandContext(ctx: CommandContext): void { _ctx = ctx }

const escRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// Build the tolerant core for one phrase. `{slot}` words become named captures.
export function phraseCore(phrase: string): string {
  const words = phrase.trim().split(/\s+/)
  return words.map((w, i) => {
    const last = i === words.length - 1
    const slot = w.match(/^\{([a-zA-Z][a-zA-Z0-9_]*)\}$/)
    if (slot) return `(?<${slot[1]}>.+?)${last ? '' : SEP}`
    const interior = i > 0 && !last
    if (interior && w.length <= 3) return `(?:${escRe(w)}${SEP})?`
    return `${escRe(w)}${last ? '' : SEP}`
  }).join('')
}

function modeRegex(core: string, mode: MatchMode): RegExp {
  switch (mode) {
    case 'trailing': return new RegExp(`\\s+(${core})[.!?,;]*\\s*$`, 'i')
    case 'anywhere': return new RegExp(`(?:^|${BOUND})(${core})(?=$|${BOUND})`, 'i')
    case 'whole':    return new RegExp(`^\\s*(${core})[.!?,;]*\\s*$`, 'i')
  }
}

// Effective phrases: user rebinding via settings.commandPhrases[id], then the
// legacy paired fields for the two original built-ins, then shipped defaults.
function effectivePhrases(cmd: Command): string[] {
  const s = _ctx?.settings.get() as any
  const custom: string[] | undefined = s?.commandPhrases?.[cmd.id]
  if (Array.isArray(custom) && custom.length) return custom
  if (cmd.id === 'submit' && Array.isArray(s?.voiceSubmitPhrases) && s.voiceSubmitPhrases.length) return s.voiceSubmitPhrases
  if (cmd.id === 'clear' && Array.isArray(s?.voiceClearPhrases) && s.voiceClearPhrases.length) return s.voiceClearPhrases
  if (cmd.id === 'stop-speech' && typeof s?.voiceStopPhrase === 'string' && s.voiceStopPhrase.trim()) return [s.voiceStopPhrase]
  return cmd.phrases
}

// ── Compiled-matcher cache (#20 perf) ────────────────────────────────────────
// Building phrase regexes on every pass was part of the mobile typing lag.
// Matchers compile once per command and invalidate only when settings change
// (phrase rebinding) or a command re-registers.
let _cacheSettingsVersion = -1
const matcherCache = new Map<string, RegExp[]>()
let _compileCount = 0
export function compileCount(): number { return _compileCount }

function matchersFor(cmd: Command): RegExp[] {
  const sv = getSettingsVersion()
  if (sv !== _cacheSettingsVersion) { matcherCache.clear(); _cacheSettingsVersion = sv }
  let ms = matcherCache.get(cmd.id)
  if (!ms) {
    ms = effectivePhrases(cmd).filter(p => p.trim()).map(p => { _compileCount++; return modeRegex(phraseCore(p), cmd.matchMode) })
    matcherCache.set(cmd.id, ms)
  }
  return ms
}

let _passCount = 0
export function passCount(): number { return _passCount }

// Run one pass over the buffer. Returns the id of the command that fired.
export function runCommandPass(value: string): string | null {
  if (!_ctx) return null
  const s = _ctx.settings.get()
  if (!s.voiceEnabled) return null
  _passCount++
  let fired: string | null = null
  for (const cmd of commands) {
    let matched = false
    if (!fired) {
      for (const re of matchersFor(cmd)) {
        const m = value.match(re)
        if (m) {
          const match: CommandMatch = { captures: { ...(m.groups ?? {}) }, matchedText: m[1] ?? m[0], value }
          let handled: void | boolean = undefined
          try { handled = cmd.action(_ctx, match) } catch { /* a command must never break input */ }
          if (handled === false) continue   // not handled — try the command's next phrase / later commands
          matched = true
          fired = cmd.id
          break
        }
      }
    }
    try { cmd.watch?.(value, _ctx, matched) } catch { /* same */ }
  }
  return fired
}
