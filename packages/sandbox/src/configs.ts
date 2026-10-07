/**
 * The configurations the sandbox tests.
 *
 * A "configuration" here is exactly what parlay already means by one: a command
 * manifest (`parlay.commands/v1`, the same document the eval engine loads from
 * `commands.json` / `PARLAY_COMMANDS`) plus the platform it applies to. The
 * page never interprets a manifest itself — it hands the chosen one to the
 * server as the per-request `commands` override, which the engine's documented
 * precedence (`request > file > embedded`) already supports, and the REAL
 * compiled engine decides what the string resolves to.
 *
 * That is what makes the preview trustworthy rather than a mock: a client-side
 * phrase matcher would be a second implementation of the engine's decisions,
 * and the sandbox exists precisely to remove that kind of second opinion.
 *
 * `sandbox.configs.json` beside the page overrides the built-ins, so the user
 * can point the sandbox at their own configurations without editing code. The
 * built-ins differ deliberately: the SAME string resolves to different actions
 * under each, which is the property criterion 1 asks to be able to observe.
 */

export interface CommandEntry {
  id: string
  phrases: string[]
  mode: string
  priority: number
  description?: string
  enabled?: boolean
  platforms?: string[]
  emit: { kind: string; actions?: unknown[]; handler?: string; config?: unknown }
}

export interface Manifest {
  schema: string
  version: string
  commands: CommandEntry[]
}

export interface SandboxConfig {
  id: string
  label: string
  /** The surface this configuration applies to ("" ⇒ the engine default, parlay). */
  platform: string
  manifest: Manifest
}

/** The one schema id the engine accepts. A manifest with any other is refused
 *  before it is sent, so a typo cannot be reported as "the engine matched
 *  nothing". */
export const MANIFEST_SCHEMA = 'parlay.commands/v1'

const submit = { kind: 'handler', handler: 'submit', config: { delayMs: 1000, requireTail: true } }

function manifest(commands: CommandEntry[]): Manifest {
  return { schema: MANIFEST_SCHEMA, version: 'sandbox-1', commands }
}

/** Configurations that ship with the sandbox. Each is a real, minimal manifest:
 *  the point is which commands exist and what they emit, not completeness. */
export const DEFAULT_CONFIGS: SandboxConfig[] = [
  {
    id: 'panel-default',
    label: 'Panel (default)',
    platform: 'parlay',
    manifest: manifest([
      {
        id: 'clear',
        phrases: ['change inside input', 'clear that'],
        mode: 'trailing',
        priority: 10,
        description: 'Empty the focused input',
        emit: { kind: 'sequence', actions: [{ verb: 'clear' }] },
      },
      {
        id: 'submit',
        phrases: ['send it', 'send that'],
        mode: 'trailing',
        priority: 5,
        description: 'Submit after the engine-owned countdown',
        emit: submit,
      },
      {
        id: 'switch',
        phrases: ['go to {agent}'],
        mode: 'whole',
        priority: 8,
        description: 'Switch to a named channel',
        emit: { kind: 'sequence', actions: [{ verb: 'switchTab', args: { id: { resolve: 'agent', from: '{agent}' } } }] },
      },
    ]),
  },
  {
    id: 'herdr-observer',
    label: 'Herdr (observer)',
    platform: 'herdr',
    manifest: manifest([
      {
        id: 'clear',
        phrases: ['change inside input'],
        mode: 'trailing',
        priority: 10,
        description: 'Empty the focused input',
        emit: { kind: 'sequence', actions: [{ verb: 'clear' }] },
      },
      {
        id: 'submit',
        phrases: ['send it'],
        mode: 'trailing',
        priority: 5,
        description: 'Submit after the engine-owned countdown',
        emit: submit,
      },
    ]),
  },
  {
    id: 'longform',
    label: 'Longform (no auto-submit)',
    platform: 'parlay',
    manifest: manifest([
      {
        id: 'clear',
        phrases: ['clear that'],
        mode: 'trailing',
        priority: 10,
        description: 'Empty the focused input',
        emit: { kind: 'sequence', actions: [{ verb: 'clear' }] },
      },
      {
        id: 'strip-trigger',
        phrases: ['change inside input'],
        mode: 'trailing',
        priority: 9,
        description: 'Strip the trigger and keep the rest of the buffer',
        emit: {
          kind: 'sequence',
          actions: [{ verb: 'setText', args: { text: { transform: 'stripTrigger', from: 'buffer' } } }],
        },
      },
    ]),
  },
]

/** ConfigError is thrown for a configurations file the sandbox cannot use.
 *  It is thrown, never swallowed into an empty list: a config list that
 *  silently went missing would render "no configurations" over a typo. */
export class ConfigError extends Error {}

/** parseConfigs validates a `sandbox.configs.json` document. */
export function parseConfigs(raw: unknown): SandboxConfig[] {
  const doc = raw as { configs?: unknown }
  const list = Array.isArray(raw) ? raw : doc?.configs
  if (!Array.isArray(list) || list.length === 0) {
    throw new ConfigError('expected a non-empty array (or {"configs": [...]}) of configurations')
  }
  return list.map((entry, i) => {
    const e = entry as Partial<SandboxConfig>
    if (!e || typeof e.id !== 'string' || e.id === '') throw new ConfigError(`configs[${i}] has no id`)
    if (typeof e.label !== 'string' || e.label === '') throw new ConfigError(`configs[${i}] has no label`)
    const m = e.manifest as Manifest | undefined
    if (!m || m.schema !== MANIFEST_SCHEMA) {
      throw new ConfigError(`configs[${i}] (${e.id}): manifest.schema must be ${MANIFEST_SCHEMA}`)
    }
    if (!Array.isArray(m.commands)) throw new ConfigError(`configs[${i}] (${e.id}): manifest.commands must be an array`)
    return { id: e.id, label: e.label, platform: e.platform ?? '', manifest: m }
  })
}

export interface LoadedConfigs {
  configs: SandboxConfig[]
  /** Where they came from, so the page can say so instead of implying a source. */
  source: string
  error?: string
}

/**
 * loadConfigs reads `sandbox.configs.json` next to the page, falling back to the
 * built-ins. A load failure is REPORTED and the built-ins are used, because a
 * sandbox that refuses to start over an optional file is worse than one that
 * says which file it could not read.
 */
export async function loadConfigs(fetchImpl: typeof fetch, url = '/sandbox.configs.json'): Promise<LoadedConfigs> {
  try {
    const res = await fetchImpl(url, { headers: { accept: 'application/json' } })
    if (!res.ok) {
      return { configs: DEFAULT_CONFIGS, source: 'built-in', error: `${url}: HTTP ${res.status}` }
    }
    return { configs: parseConfigs(await res.json()), source: url }
  } catch (err) {
    return { configs: DEFAULT_CONFIGS, source: 'built-in', error: `${url}: ${String(err)}` }
  }
}

/** configById finds one configuration, or undefined. */
export function configById(configs: SandboxConfig[], id: string): SandboxConfig | undefined {
  return configs.find((c) => c.id === id)
}
