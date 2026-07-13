import type { Command, CommandContext, CommandMatch } from './types'
import { registerCommand } from './registry'
import { resolveAgent } from './ctx'
import { flagLastSpoken } from '../speech-highlight'

// ── Built-in commands ────────────────────────────────────────────────────────
// The original hardcoded behaviors (submit / clear / stop-speech) migrated onto
// the registry with their #6/#8 semantics intact, plus tab commands as the
// proof the abstraction generalizes. Users rebind any phrase list in Settings.

// submit: trailing phrase arms a 1s timer; if the buffer still ends with the
// phrase when it fires, the phrase strips and the message sends (Talon flow).
let submitTimer: ReturnType<typeof setTimeout> | null = null

const submit: Command = {
  id: 'submit',
  phrases: ['bravely', 'gravely', 'briefly', 'lap'],
  matchMode: 'trailing',
  priority: 30,
  description: 'End a message with this word to auto-send it after 1s',
  action(ctx: CommandContext, m: CommandMatch) {
    if (submitTimer) clearTimeout(submitTimer)
    const matchedTail = m.matchedText
    submitTimer = setTimeout(() => {
      submitTimer = null
      const val = ctx.input.value()
      // Re-verify the buffer still ends with what we matched before firing
      const idx = val.toLowerCase().lastIndexOf(matchedTail.toLowerCase())
      if (idx === -1 || val.slice(idx + matchedTail.length).trim().replace(/[.!?,;]+/g, '') !== '') return
      const stripped = val.slice(0, idx).trim()
      if (stripped) ctx.input.submit(stripped)
    }, 1000)
  },
  watch(_value, _ctx, matched) {
    if (!matched && submitTimer) { clearTimeout(submitTimer); submitTimer = null }
  },
}

const clear: Command = {
  id: 'clear',
  phrases: ['change inside in input'],
  matchMode: 'anywhere',
  priority: 10,
  description: 'Saying this anywhere in the input empties the whole box',
  action(ctx) { ctx.input.clear() },
}

const stopSpeech: Command = {
  id: 'stop-speech',
  phrases: ['spoken pause'],
  matchMode: 'trailing',
  priority: 5,
  description: 'End the input with this to instantly silence current speech',
  action(ctx, m) {
    ctx.speech.stop()
    const val = ctx.input.value()
    const idx = val.toLowerCase().lastIndexOf(m.matchedText.toLowerCase())
    ctx.input.setText(idx >= 0 ? val.slice(0, idx).trimEnd() : val)
  },
}

const switchTab: Command = {
  id: 'switch-tab',
  phrases: ['switch to {agent}', 'go to {agent}', 'show me {agent}'],
  matchMode: 'whole',
  priority: 20,
  description: 'Switch the active agent tab by name',
  action(ctx, m) {
    const id = resolveAgent(m.captures.agent ?? '')
    if (!id) return false                    // unknown agent — let 'go to {page}' try
    if (ctx.tabs.switch(id)) ctx.input.clear()
  },
}

const archiveTab: Command = {
  id: 'archive-tab',
  phrases: ['archive {agent}', 'archive tab {agent}'],
  matchMode: 'whole',
  priority: 20,
  description: 'Archive an agent tab by name',
  action(ctx, m) {
    const id = resolveAgent(m.captures.agent ?? '')
    if (!id) return false
    if (ctx.tabs.archive(id)) ctx.input.clear()
  },
}

// Parlay-as-shell (#16): open a Pulse page in the workspace pane. Lower
// priority than switch-tab, so agent names win and unknown names fall through.
const goToPage: Command = {
  id: 'go-to-page',
  phrases: ['go to {page}', 'open {page}', 'show {page}', 'workspace {page}'],
  matchMode: 'whole',
  priority: 25,
  description: 'Open a Pulse page in the workspace pane (e.g. "go to status")',
  action(ctx, m) {
    const raw = (m.captures.page ?? '').trim().toLowerCase().replace(/[.!?,;:]+$/, '')
    if (!raw) return false
    ctx.workspace.navigate(`/${raw.replace(/\s+/g, '-')}/`)
    ctx.input.clear()
  },
}

const nextTab: Command = {
  id: 'next-tab',
  phrases: ['next tab', 'next agent'],
  matchMode: 'whole',
  priority: 20,
  description: 'Switch to the next agent tab',
  action(ctx) { ctx.tabs.next(); ctx.input.clear() },
}

const prevTab: Command = {
  id: 'prev-tab',
  phrases: ['previous tab', 'previous agent', 'last tab'],
  matchMode: 'whole',
  priority: 20,
  description: 'Switch to the previous agent tab',
  action(ctx) { ctx.tabs.prev(); ctx.input.clear() },
}

const flagSpeech: Command = {
  id: 'flag-speech',
  phrases: ['flag speech', 'flag that'],
  matchMode: 'whole',
  priority: 8,
  description: 'Report the last-spoken sentence as mispronounced',
  action(ctx) {
    void flagLastSpoken()
    ctx.input.clear()
  },
}

export function registerBuiltins(): void {
  for (const c of [stopSpeech, flagSpeech, clear, switchTab, archiveTab, nextTab, prevTab, goToPage, submit]) registerCommand(c)
}
