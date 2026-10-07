import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { App } from './App'
import { MANIFEST_SCHEMA } from './configs'
import { fakeServer, StubEventSource } from './testkit'

let host: HTMLElement
let root: Root

async function mount(fetchImpl: typeof fetch, es: StubEventSource) {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  await act(async () => {
    root.render(
      <App
        fetchImpl={fetchImpl}
        device="dev-1"
        eventSourceFactory={() => es as unknown as EventSource}
      />,
    )
  })
}

/** type sets the composer value the way a user would, inside act.
 *
 * The native value setter is used rather than a plain `input.value = …`: React
 * installs its own value tracker on the element, and assigning `.value`
 * directly without going through the prototype setter makes React skip the
 * change it is supposed to react to. This is the standard way to drive a React
 * controlled input from a DOM-level test. */
async function type(value: string) {
  const input = host.querySelector<HTMLInputElement>('input[aria-label="test string"]')!
  const setValue = Object.getOwnPropertyDescriptor(globalThis.HTMLInputElement.prototype, 'value')!.set!
  await act(async () => {
    setValue.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function click(selector: string, index = 0) {
  const els = host.querySelectorAll<HTMLButtonElement>(selector)
  if (!els[index]) throw new Error(`no element matching ${selector}[${index}]`)
  await act(async () => {
    els[index].click()
  })
}

async function flush() {
  await act(async () => {
    await Promise.resolve()
  })
}

beforeEach(() => {
  document.body.innerHTML = ''
})

afterEach(async () => {
  await act(async () => {
    root?.unmount()
  })
})

describe('the sandbox page', () => {
  test('loads MULTIPLE configurations and one preview row per configuration', async () => {
    const { fetchImpl } = fakeServer()
    await mount(fetchImpl, new StubEventSource(''))
    await flush()

    const rows = host.querySelectorAll('tr[data-config]')
    expect(rows.length).toBeGreaterThan(1)
    // The built-in set is not reached over the network when the file is a 404
    // (the fake returns 404 for it), which is also the offline path.
    expect(host.textContent).toContain('Panel (default)')
    expect(host.textContent).toContain('Herdr (observer)')
  })

  test('PREVIEW evaluates every configuration with a preview stream id and its own manifest', async () => {
    const { calls, fetchImpl } = fakeServer()
    await mount(fetchImpl, new StubEventSource(''))
    await flush()
    calls.length = 0

    await type('change inside input')
    await click('button.primary')
    await flush()

    const evals = calls.filter((c) => c.url === '/api/chat/eval')
    expect(evals.length).toBe(host.querySelectorAll('tr[data-config]').length)
    for (const call of evals) {
      const body = call.body as { streamId: string; reason: string; commands: { schema: string }; text: string }
      // The marker the SERVER uses to refuse delivery.
      expect(body.streamId).toStartWith('sandbox-preview-')
      expect(body.reason).toBe('sandbox-preview')
      // The real manifest, handed to the real engine as a per-request override.
      expect(body.commands.schema).toBe(MANIFEST_SCHEMA)
      expect(body.text).toBe('change inside input')
      expect(body.device).toBe('dev-1')
    }
    // The row that resolved shows the input action → output actions pair. The
    // input action is the engine's `fired` command id, read back from the log
    // keyed by the stream id this page generated — so the preview table and the
    // log are answering from one source.
    const firstConfigColumn = host.querySelector('tr[data-config] td:nth-child(3)')!
    expect(firstConfigColumn.textContent).toContain('clear')
    expect(firstConfigColumn.textContent).toContain('→ clear')
    expect(firstConfigColumn.textContent).not.toContain('— →')
  })

  test('FIRE uses a fire stream id — the same path with the delivery marker absent', async () => {
    const { calls, fetchImpl } = fakeServer()
    const es = new StubEventSource('')
    await mount(fetchImpl, es)
    await flush()

    await type('change inside input')
    await click('button.primary')
    await flush()
    calls.length = 0

    await click('button.fire')
    await flush()

    const fires = calls.filter((c) => c.url === '/api/chat/eval')
    expect(fires.length).toBe(1)
    expect((fires[0].body as { streamId: string }).streamId).toStartWith('sandbox-fire-')
    expect((fires[0].body as { reason: string }).reason).toBe('sandbox-fire')
  })

  test('Fire is unavailable until a preview has run, so nothing fires implicitly', async () => {
    const { calls, fetchImpl } = fakeServer()
    await mount(fetchImpl, new StubEventSource(''))
    await flush()
    await type('send it')
    calls.length = 0

    const fireButton = host.querySelector<HTMLButtonElement>('button.fire')!
    expect(fireButton.disabled).toBe(true)
    await click('button.fire')
    await flush()
    expect(calls.filter((c) => c.url === '/api/chat/eval')).toHaveLength(0)
  })

  test('editing the string invalidates the preview, so Fire cannot deliver text never previewed', async () => {
    const { calls, fetchImpl } = fakeServer()
    await mount(fetchImpl, new StubEventSource(''))
    await flush()

    await type('clear that')
    await click('button.primary')
    await flush()
    expect(host.querySelector<HTMLButtonElement>('button.fire')!.disabled).toBe(false)
    expect(host.querySelector('tr[data-config]')!.textContent).toContain('clear')

    // The user edits the string. The successful preview belongs to the OLD text,
    // so it must stop licensing a real delivery — otherwise the "Fire requires a
    // preview of this row" gate is a lie and one click would deliver an action
    // for a string that was never evaluated.
    await type('send it')
    await flush()
    expect(host.querySelector<HTMLButtonElement>('button.fire')!.disabled).toBe(true)
    expect(host.querySelector('tr[data-config]')!.textContent).not.toContain('clear')

    calls.length = 0
    await click('button.fire')
    await flush()
    expect(calls.filter((c) => c.url === '/api/chat/eval')).toHaveLength(0)
  })

  test('an empty string cannot be previewed or fired', async () => {
    const { calls, fetchImpl } = fakeServer()
    await mount(fetchImpl, new StubEventSource(''))
    await flush()
    await type('   ')
    calls.length = 0
    await click('button.primary')
    await flush()
    expect(host.querySelector<HTMLButtonElement>('button.primary')!.disabled).toBe(true)
    expect(calls.filter((c) => c.url === '/api/chat/eval')).toHaveLength(0)
  })

  test('renders what the server actually DELIVERED over SSE', async () => {
    const { fetchImpl } = fakeServer()
    const es = new StubEventSource('')
    await mount(fetchImpl, es)
    await flush()

    await act(async () => {
      es.emit('input_action', { streamId: 'sandbox-fire-panel-default-9', actions: [{ verb: 'submitNow' }] })
    })
    expect(host.textContent).toContain('sandbox-fire-panel-default-9')
    expect(host.textContent).toContain('submitNow')
  })

  test('the log names all four outcomes, filters them, and offers the off switch per row', async () => {
    const { calls, fetchImpl } = fakeServer()
    await mount(fetchImpl, new StubEventSource(''))
    await flush()

    // The closed outcome vocabulary is offered even for values with no rows.
    for (const outcome of ['delivered', 'queued', 'dropped', 'refused']) {
      expect(host.querySelector(`select[aria-label="filter by outcome"]`)?.textContent).toContain(outcome)
    }

    // Give the log some rows the way rows actually appear — by evaluating.
    await type('change inside input')
    await click('button.primary')
    await flush()

    // The facets populate the other axes from what the server actually holds.
    expect(host.querySelector('select[aria-label="filter by input action"]')?.textContent).toContain('clear')
    expect(host.querySelector('select[aria-label="filter by output action"]')?.textContent).toContain('clear')

    // Filtering is the SERVER's job: the page only sends the query.
    calls.length = 0
    const select = host.querySelector<HTMLSelectElement>('select[aria-label="filter by outcome"]')!
    await act(async () => {
      select.value = 'refused'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    expect(calls.some((c) => c.url.includes('/api/chat/action-log?outcome=refused'))).toBe(true)

    // The off switch is IN THE ROW — the last cell of each row — and it hits
    // the real route rather than hiding the row.
    calls.length = 0
    const offButton = host.querySelector<HTMLButtonElement>('tbody tr td:last-child button.chip')!
    expect(offButton).toBeDefined()
    const targetId = offButton.textContent!.replace(/^turn /, '').replace(/ off$/, '')
    await act(async () => {
      offButton.click()
    })
    await flush()
    const flip = calls.find((c) => c.url === '/api/chat/off-switch')
    expect(flip).toBeDefined()
    expect(flip!.body).toMatchObject({ kind: 'action', id: targetId, off: true, surface: 'website' })
  })
})

