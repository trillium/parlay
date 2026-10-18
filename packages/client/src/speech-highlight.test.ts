import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, describe, beforeEach } from 'bun:test'
import { spansFor, setPlayButton, wrapBlocks } from './speech-highlight'

// The speak-time lookups found their target by splicing the message id into
// `[data-pa-id="${msgId}"]`. A quote in an id made the selector unparseable —
// a DOMException thrown in the middle of speech — or, with a balanced payload,
// pointed at a DIFFERENT message's bubble.
describe('a hostile message id in the speech lookups', () => {
  const HOSTILE = 'x"] , [data-pa-id="victim'

  function bubbleWith(msgId: string, ...sentences: string[]) {
    const el = document.createElement('div')
    el.dataset.paId = msgId
    el.innerHTML = `<div class="pa-bubble"><div class="pa-para">`
      + sentences.map((s, i) => `<span class="pa-sb" data-bi="${i}">${s}</span>`).join('')
      + `</div><button class="pa-playpause">▶</button></div>`
    document.body.appendChild(el)
    return el
  }

  beforeEach(() => { document.body.innerHTML = '' })

  test('spansFor returns this message’s spans, not another message’s', () => {
    bubbleWith('victim', 'wrong one')
    bubbleWith(HOSTILE, 'right one')
    expect(spansFor(HOSTILE)!.map(s => s.textContent)).toEqual(['right one'])
  })

  test('an unparseable id yields null instead of throwing', () => {
    bubbleWith('victim', 'hello')
    expect(spansFor('a" *')).toBeNull()
    expect(setPlayButton('a" *', true)).toBeUndefined()
    expect(wrapBlocks('a" *', [{ raw: 'x' }])).toBeNull()
  })

  test('setPlayButton only restyles the addressed message', () => {
    bubbleWith('victim', 'wrong one')
    bubbleWith(HOSTILE, 'right one')
    setPlayButton(HOSTILE, true)
    const btns = [...document.querySelectorAll('.pa-playpause')].map(b => b.textContent)
    expect(btns).toEqual(['▶', '⏸'])
  })

  test('wrapBlocks falls back into this message’s bubble', () => {
    bubbleWith('victim', 'wrong one')
    const el = bubbleWith(HOSTILE, 'right one')
    const spans = wrapBlocks(HOSTILE, [{ raw: 'first' }, { raw: 'second' }])
    expect(spans!.map(s => s.textContent)).toEqual(['first', 'second'])
    expect(el.querySelector('.pa-speaking')).not.toBeNull()
  })
})
