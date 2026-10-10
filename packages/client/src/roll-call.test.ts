import { describe, expect, test } from 'bun:test'
import { resetRollCallState, setRollCallState, snapshot } from './roll-call'

describe('roll call snapshot', () => {
  test('carries version, device, load address and contributed state', () => {
    resetRollCallState()
    setRollCallState({ voiceArmed: true, lastPhrase: 'hello bravely' })
    const s = snapshot('dev-1')
    expect(s.device).toBe('dev-1')
    expect(s.version).toBeTruthy()
    expect(s.voiceArmed).toBe(true)
    expect(s.lastPhrase).toBe('hello bravely')
    expect(s.screenErrors).toBeUndefined()
  })
  test('on-screen connection banner is reported as a screen error', () => {
    resetRollCallState()
    expect(snapshot('d', 'reconnecting…').screenErrors).toEqual(['reconnecting…'])
  })
})
