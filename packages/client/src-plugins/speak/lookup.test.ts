import '../../happydom'
import { test, expect, describe, beforeEach } from 'bun:test'
import { markReady } from './player'
import { openCorrector } from './corrector'

// Every message lookup in these two modules used to splice the server-supplied
// message id into `[data-pa-id="${id}"]`. A balanced payload rewrote the
// selector and matched a DIFFERENT message; an unbalanced one made it
// unparseable and threw out of the playback/readiness/corrector path. These
// exercise the real exported entry points, so they fail on the pre-fix code.

const HOSTILE = 'x"] , [data-pa-id="victim'

function msg(id: string): HTMLElement {
  const el = document.createElement('div')
  el.className = 'pa-msg'
  el.setAttribute('data-pa-id', id)
  const para = document.createElement('div')
  para.className = 'pa-para'
  const sb = document.createElement('span')
  sb.className = 'pa-sb'
  sb.textContent = id
  para.appendChild(sb)
  const dot = document.createElement('span')
  dot.className = 'pa-replay-dot'
  dot.setAttribute('data-bi', '0')
  el.appendChild(para)
  el.appendChild(dot)
  return el
}

beforeEach(() => {
  document.body.innerHTML = ''
  ;(window as any).__parlay = { speechUi: {} }
})

describe('speak plugin attribute lookups', () => {
  test('markReady marks the matching message dot, not another message', () => {
    document.body.appendChild(msg('victim'))
    document.body.appendChild(msg(HOSTILE))
    markReady(HOSTILE, 0, true)
    const dots = document.querySelectorAll('.pa-replay-dot')
    expect(dots[0].classList.contains('ready')).toBe(false)
    expect(dots[1].classList.contains('ready')).toBe(true)
  })

  test('an unparseable message id is ignored instead of throwing', () => {
    document.body.appendChild(msg('victim'))
    expect(() => markReady('a" *', 0, true)).not.toThrow()
    expect(document.querySelector('.pa-replay-dot')!.classList.contains('ready')).toBe(false)
  })

  test('openCorrector anchors under the hostile message, not another one', () => {
    const hostile = msg(HOSTILE)
    document.body.appendChild(hostile)
    document.body.appendChild(msg('victim'))
    ;(window as any).__parlay.speechUi.getLastSpoken = () => ({ msgId: HOSTILE, sentence: 'wrong word' })
    openCorrector({ input: { submit() {} } })
    const box = document.querySelector('.pa-corrector')
    expect(box).not.toBeNull()
    expect((box as HTMLElement).previousElementSibling).toBe(hostile.querySelector('.pa-para'))
  })
})
