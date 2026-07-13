import type { ParlaySettings } from '../settings-modal/types'

// ── Parlay command subsystem — types ─────────────────────────────────────────
// A command binds spoken/typed phrases to an action. Phrases are matched
// against the input buffer with dictation tolerance; the action only touches
// the panel through the CommandContext surface.

export type MatchMode =
  | 'trailing'   // phrase at the end of the buffer, text before it (submit-style)
  | 'anywhere'   // phrase anywhere in the buffer (clear-style, spec #8)
  | 'whole'      // the buffer IS the command (tab ops)

export interface CommandMatch {
  captures: Record<string, string>   // named {slot} captures, raw text
  matchedText: string                // what the phrase consumed
  value: string                      // full buffer at match time
}

export interface Command {
  id: string
  phrases: string[]        // defaults; users rebind via settings commandPhrases[id]
  matchMode: MatchMode
  priority: number         // lower wins; first match ends the pass
  description: string      // shown in settings + docs surfaces
  action: (ctx: CommandContext, m: CommandMatch) => void
  // Called EVERY input pass (matched or not) — lets stateful commands
  // (submit's 1s arm-and-verify timer) cancel themselves when the buffer
  // stops matching.
  watch?: (value: string, ctx: CommandContext, matched: boolean) => void
}

export interface CommandContext {
  input: {
    value(): string
    setText(t: string): void       // replaces buffer + resizes + draft-syncs
    clear(): void                  // empty + full draft hygiene
    submit(text: string): void     // send as the captain's message
  }
  tabs: {
    list(): { id: string; name: string }[]
    active(): string | null
    switch(id: string): boolean
    archive(id: string): boolean
    next(): void
    prev(): void
  }
  drawer: { open(): void }
  speech: { stop(): void }
  settings: { get(): ParlaySettings }
}
