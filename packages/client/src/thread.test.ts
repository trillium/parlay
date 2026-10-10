import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach, afterEach } from 'bun:test'
import { injectDOM, bindDOMRefs, thread, tabsEl } from './dom'
import { _appendMsgEl, addThinkEl, rmThinkEl, markMsgReceived, loadHistory } from './thread'
import { renderTabs } from './tabs'
import { renderSheet } from './switcher'
import { agentInfo, msgs, setActiveChannel, setThinkingState, setAtBottom, setChannelStatuses } from './state'

// ── Agent metadata must never escape its rendering context ───────────────────
// agentInfo is filled from server `agents` / `agent_register` SSE payloads,
// whose `color`/`name` come straight off the unauthenticated register-agent
// request body (see tools/cli/internal/identity + the go-server handler, which
// stores them verbatim). thread.ts interpolated `info.color` into a
// `style="..."` attribute and the name-derived initials into element text, so a
// registered color like `#000" onmouseover="alert(1)` closed the attribute and
// installed a live event handler on the avatar of every message in the tab.
// The color is now validated as a CSS colour token (falling back to the
// caller's default) and the initials are escaped.

function registerAgent(color: string, name = 'Mech') {
  agentInfo.set('mech', { id: 'mech', name, color })
  setActiveChannel('mech')
}

function avatar(): HTMLElement {
  return thread.querySelector('.pa-av') as HTMLElement
}

function msg(over: Record<string, unknown> = {}) {
  return { id: 'm1', role: 'agent', channel: 'mech', ts: new Date().toISOString(), text: 'hi', ...over }
}

beforeEach(() => {
  document.body.innerHTML = ''
  document.head.innerHTML = ''
  injectDOM()
  bindDOMRefs()
  agentInfo.clear()
  msgs.length = 0
  setActiveChannel(null)
  setThinkingState(false)
  setAtBottom(true)
})

afterEach(() => {
  rmThinkEl()
  document.body.innerHTML = ''
  document.head.innerHTML = ''
})

test('a hostile agent color cannot break out of the avatar style attribute', () => {
  registerAgent('#000" onmouseover="alert(1)')
  _appendMsgEl(msg(), true)
  const av = avatar()
  expect(av.getAttribute('onmouseover')).toBeNull()
  expect(thread.innerHTML).not.toContain('onmouseover')
  // The style attribute survives intact — the whole value is just ignored.
  expect(av.getAttribute('style')).toContain('color-mix')
})

test('a hostile agent color cannot break out on the action-request card either', () => {
  registerAgent('#000"><script>alert(1)</script')
  _appendMsgEl(msg({ type: 'action_request', action: { kind: 'switch_tab', channel: 'mech', label: 'go' } }), true)
  expect(thread.querySelector('script')).toBeNull()
  expect(thread.innerHTML).not.toContain('<script>')
})

test('the think indicator is hardened the same way', () => {
  registerAgent('#000" onmouseover="alert(1)')
  addThinkEl()
  const av = thread.querySelector('#pa-think .pa-av') as HTMLElement
  expect(av.getAttribute('onmouseover')).toBeNull()
  expect(av.getAttribute('style')).toContain('color-mix')
})

test('a legitimate color still renders', () => {
  registerAgent('#ff8800')
  _appendMsgEl(msg(), true)
  expect(avatar().getAttribute('style')).toContain('#ff8800')
  expect(avatar().getAttribute('style')).not.toContain('pa-green')
})

test('an unusable color falls back to the caller default, it is not dropped', () => {
  registerAgent('red; background-image:url(//evil.example/x)')
  _appendMsgEl(msg(), true)
  const style = avatar().getAttribute('style') ?? ''
  expect(style).toContain('var(--pa-green)')
  expect(style).not.toContain('url(')
})

test('an empty color falls back too', () => {
  registerAgent('')
  _appendMsgEl(msg(), true)
  expect(avatar().getAttribute('style')).toContain('var(--pa-green)')
})

test('name-derived initials are escaped, never parsed as markup', () => {
  registerAgent('#00ff00', '<img src=x onerror=alert(1)>')
  _appendMsgEl(msg(), true)
  const av = avatar()
  expect(av.querySelector('img')).toBeNull()
  expect(av.getAttribute('onerror')).toBeNull()
  // slice(0,2) of the name, as TEXT.
  expect(av.textContent).toBe('<I')
})

test('a nickname takes precedence over the name for the initials', () => {
  registerAgent('#00ff00', 'Fallen')
  agentInfo.get('mech')!.nicknames = ['<b>']
  _appendMsgEl(msg(), true)
  expect(avatar().textContent).toBe('<B')
  expect(avatar().querySelector('b')).toBeNull()
})
// The same registry colour reaches two more renderers, and there it goes
// through the CSSOM rather than innerHTML — no HTML breakout is possible, but
// `url(https://…)` IS a valid `background` shorthand and a `--tab-color`
// custom property, so an unvalidated colour would make the panel fetch an
// attacker-chosen URL (a visit beacon) on every tab render.
describe('the other two renderers of the same colour', () => {
  const HOSTILE = 'url(https://attacker.example/beacon.png)'

  beforeEach(() => {
    setChannelStatuses({ mech: 'listening' })
  })

  test('the header dot never carries a url() colour', () => {
    registerAgent(HOSTILE)
    renderTabs()
    const dot = document.getElementById('pa-dot') as HTMLElement
    expect(dot.style.background).not.toContain('url(')
    expect(dot.style.background).toContain('var(--pa-green)')
  })

  test('the mobile switcher rows never carry a url() colour', () => {
    registerAgent(HOSTILE)
    renderSheet()
    const row = document.querySelector('#pa-sheet-list .pa-sheet-row') as HTMLElement
    expect(row.style.getPropertyValue('--tab-color')).toBe('var(--pa-green)')
  })

  test('a legitimate colour still reaches both', () => {
    registerAgent('#ff8800')
    renderTabs()
    renderSheet()
    expect((document.getElementById('pa-dot') as HTMLElement).style.background).toContain('#ff8800')
    const row = document.querySelector('#pa-sheet-list .pa-sheet-row') as HTMLElement
    expect(row.style.getPropertyValue('--tab-color')).toBe('#ff8800')
  })
})

// The same registry id reaches the tab bar's unread-badge `id=` attribute. Agent
// ids are stored verbatim by the unauthenticated register-agent handler, so an
// id of `x" onmouseover="alert(1)` used to close that attribute and install a
// live event handler on the badge of every tab in every panel. esc() keeps the
// whole id inside the attribute value.
describe('an agent id in the tab unread badge', () => {
  const HOSTILE_ID = 'mech" onmouseover="alert(1)'

  function renderTwoTabs() {
    agentInfo.set('other', { id: 'other', name: 'Other', color: '#00ff00' })
    agentInfo.set(HOSTILE_ID, { id: HOSTILE_ID, name: 'Mech', color: '#ff8800' })
    setChannelStatuses({ other: 'listening', [HOSTILE_ID]: 'listening' })
    renderTabs()
  }

  test('a quote in the id cannot break out of the id attribute', () => {
    renderTwoTabs()
    const badge = document.getElementById('pa-tab-unread-' + HOSTILE_ID)
    expect(badge).not.toBeNull()
    // Assert on the PARSED DOM, not on innerHTML: the HTML serializer is
    // allowed to emit `<`, `>` and `onmouseover=` verbatim inside an attribute
    // value, so a substring check on the serialization proves nothing. What
    // matters is that no extra attribute and no extra element came into being.
    expect(badge!.getAttribute('onmouseover')).toBeNull()
    expect([...badge!.attributes].map(a => a.name).sort()).toEqual(['class', 'id'])
    expect(badge!.tagName).toBe('SPAN')
  })

  test('a script tag in the id never becomes an element', () => {
    const id = 'mech"><script>alert(1)</script'
    agentInfo.set('other', { id: 'other', name: 'Other', color: '#00ff00' })
    agentInfo.set(id, { id, name: 'Mech', color: '#ff8800' })
    setChannelStatuses({ other: 'listening', [id]: 'listening' })
    renderTabs()
    // Same reasoning as above: the payload stays inert text inside the id
    // attribute rather than becoming markup.
    expect(tabsEl.querySelector('script')).toBeNull()
    const badge = document.getElementById('pa-tab-unread-' + id)
    expect(badge).not.toBeNull()
    expect(badge!.tagName).toBe('SPAN')
  })

  test('the escaped badge is still findable by the id the SSE unread path uses', () => {
    // sse.ts and jump-button.ts both look the badge up with
    // getElementById(`pa-tab-unread-${ch}`) using the RAW channel, so escaping
    // must not change the element's id — the parser decodes the entities back.
    renderTwoTabs()
    const badge = document.getElementById('pa-tab-unread-' + HOSTILE_ID)
    expect(badge).not.toBeNull()
    expect(badge!.className).toContain('pa-tab-unread')
  })

  test('an ordinary agent id still gets its badge', () => {
    agentInfo.set('other', { id: 'other', name: 'Other', color: '#00ff00' })
    agentInfo.set('mech', { id: 'mech', name: 'Mech', color: '#ff8800' })
    setChannelStatuses({ other: 'listening', mech: 'listening' })
    renderTabs()
    expect(document.getElementById('pa-tab-unread-mech')).not.toBeNull()
  })
})

// The pip/dedup lookups used to splice the message id straight into a
// `[data-pa-id="${id}"]` selector. A balanced payload turned the lookup into
// TWO selectors and flipped a different message; an unbalanced one threw a
// DOMException out of the SSE handler that called it.
describe('a hostile message id in the data-pa-id lookups', () => {
  const HOSTILE = 'x"] , [data-pa-id="victim'

  function userMsg(id: string) { return msg({ id, role: 'user', received: false }) }

  test('a selector-shaped id flips only its own pip', () => {
    _appendMsgEl(userMsg('victim'), true)
    _appendMsgEl(userMsg(HOSTILE), true)
    expect(() => markMsgReceived(HOSTILE)).not.toThrow()
    expect([...thread.querySelectorAll('.pa-msg-status')].map(p => p.className))
      .toEqual(['pa-msg-status queued', 'pa-msg-status received'])
  })

  test('an unparseable id is a no-op rather than a thrown DOMException', () => {
    _appendMsgEl(userMsg('victim'), true)
    expect(() => markMsgReceived('a" *')).not.toThrow()
    expect(thread.querySelector('.pa-msg-status')!.className).toBe('pa-msg-status queued')
  })

  test('history dedup still collapses a true duplicate', () => {
    setActiveChannel('mech')
    loadHistory([userMsg(HOSTILE)])
    loadHistory([userMsg(HOSTILE)])
    expect(thread.querySelectorAll('[data-pa-id]').length).toBe(1)
  })
})
