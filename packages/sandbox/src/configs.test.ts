import { describe, expect, test } from 'bun:test'
import { ConfigError, DEFAULT_CONFIGS, MANIFEST_SCHEMA, configById, loadConfigs, parseConfigs } from './configs'

describe('the built-in configurations', () => {
  test('are several, each a real manifest the engine would accept', () => {
    expect(DEFAULT_CONFIGS.length).toBeGreaterThan(1)
    for (const c of DEFAULT_CONFIGS) {
      expect(c.manifest.schema).toBe(MANIFEST_SCHEMA)
      expect(c.manifest.commands.length).toBeGreaterThan(0)
      for (const cmd of c.manifest.commands) {
        expect(cmd.id).toBeTruthy()
        expect(cmd.phrases.length).toBeGreaterThan(0)
        // A command the engine cannot interpret would make the preview lie.
        expect(['sequence', 'handler']).toContain(cmd.emit.kind)
      }
    }
  })

  test('differ from one another, so the same string can resolve differently', () => {
    const ids = DEFAULT_CONFIGS.map((c) => c.manifest.commands.map((cmd) => cmd.id).sort().join(','))
    expect(new Set(ids).size).toBe(DEFAULT_CONFIGS.length)

    const panel = configById(DEFAULT_CONFIGS, 'panel-default')!
    const longform = configById(DEFAULT_CONFIGS, 'longform')!
    const panelSubmit = panel.manifest.commands.find((c) => c.id === 'submit')
    expect(panelSubmit).toBeDefined()
    // The longform configuration deliberately has no submit handler at all.
    expect(longform.manifest.commands.find((c) => c.id === 'submit')).toBeUndefined()

    // And they route the SAME phrase differently: panel clears, longform strips
    // the trigger and keeps the buffer.
    const phrase = 'change inside input'
    const panelClear = panel.manifest.commands.find((c) => c.phrases.includes(phrase))!
    const longStrip = longform.manifest.commands.find((c) => c.phrases.includes(phrase))!
    expect(panelClear.emit.actions).toEqual([{ verb: 'clear' }])
    expect(longStrip.emit.actions).not.toEqual([{ verb: 'clear' }])
  })
})

describe('parseConfigs', () => {
  const good = { id: 'x', label: 'X', platform: 'parlay', manifest: { schema: MANIFEST_SCHEMA, version: 'v', commands: [] } }

  test('accepts a bare array and a {configs: [...]} document', () => {
    expect(parseConfigs([good])).toHaveLength(1)
    expect(parseConfigs({ configs: [good] })).toHaveLength(1)
  })

  test('defaults platform to the engine default when omitted', () => {
    const { platform, ...noPlatform } = good
    expect(parseConfigs([noPlatform])[0].platform).toBe('')
    expect(platform).toBe('parlay')
  })

  test('refuses a document it cannot use, rather than rendering nothing', () => {
    expect(() => parseConfigs({})).toThrow(ConfigError)
    expect(() => parseConfigs([])).toThrow(ConfigError)
    expect(() => parseConfigs([{ label: 'no id' }])).toThrow(/has no id/)
    expect(() => parseConfigs([{ id: 'a', label: '' }])).toThrow(/has no label/)
    expect(() => parseConfigs([{ id: 'a', label: 'A', manifest: { schema: 'wrong', commands: [] } }])).toThrow(
      /manifest.schema/,
    )
    expect(() => parseConfigs([{ id: 'a', label: 'A', manifest: { schema: MANIFEST_SCHEMA } }])).toThrow(
      /commands must be an array/,
    )
  })
})

describe('loadConfigs', () => {
  test('reads the file beside the page when it is there', async () => {
    const fetchImpl = (async () =>
      new Response(
        JSON.stringify({ configs: [{ id: 'mine', label: 'Mine', manifest: { schema: MANIFEST_SCHEMA, version: 'v', commands: [] } }] }),
        { status: 200 },
      )) as unknown as typeof fetch
    const loaded = await loadConfigs(fetchImpl)
    expect(loaded.source).toBe('/sandbox.configs.json')
    expect(loaded.configs[0].id).toBe('mine')
    expect(loaded.error).toBeUndefined()
  })

  test('falls back to the built-ins and SAYS SO when the file is absent or broken', async () => {
    const missing = (async () => new Response('nope', { status: 404 })) as unknown as typeof fetch
    const a = await loadConfigs(missing)
    expect(a.configs).toBe(DEFAULT_CONFIGS)
    expect(a.error).toMatch(/HTTP 404/)

    const broken = (async () => new Response('{not json', { status: 200 })) as unknown as typeof fetch
    const b = await loadConfigs(broken)
    expect(b.configs).toBe(DEFAULT_CONFIGS)
    expect(b.error).toBeTruthy()
  })
})
