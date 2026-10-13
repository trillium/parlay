import '../../happydom' // registers DOM before the imports below
import { test, expect, describe } from 'bun:test'
import { setCommandContext, runCommandPass } from './registry'
import type { CommandContext } from './types'

// "next" alone in the input box switches to the next agent. The shipped
// next-tab phrases are read from builtins; the cyclic wrap lives in ctx.tabs.next.
function harness() {
  const calls: string[] = []
  const ctx = {
    settings: { get: () => ({ voiceEnabled: true }) },
    tabs: { next: () => calls.push('next'), prev: () => calls.push('prev') },
    input: { clear: () => calls.push('clear') },
  } as unknown as CommandContext
  setCommandContext(ctx)
  return calls
}

async function registerNextTab() {
  const { registerBuiltins } = await import('./builtins')
  registerBuiltins()
}

describe('bare "next" switches to the next agent', () => {
  for (const input of ['next', 'Next', '  next  ', 'next.', 'NEXT!']) {
    test(`${JSON.stringify(input)} fires next-tab`, async () => {
      const calls = harness()
      await registerNextTab()
      expect(runCommandPass(input)).toBe('next-tab')
      expect(calls).toEqual(['next', 'clear'])
    })
  }

  for (const input of ['next steps', 'what is next', 'nextgen', 'next to the door']) {
    test(`${JSON.stringify(input)} is a normal message`, async () => {
      const calls = harness()
      await registerNextTab()
      expect(runCommandPass(input)).not.toBe('next-tab')
      expect(calls).not.toContain('next')
    })
  }
})
