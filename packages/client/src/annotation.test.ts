import '../happydom' // registers DOM before the imports below; CWD-independent
import { test, expect, beforeEach, afterEach } from 'bun:test'
import { wireAnnotation, doSetAnnotate, sendAnnotations } from './annotation'
import { annotations } from './state'

function el(id: string) {
  const e = document.createElement(id === 'popupIn' ? 'textarea' : 'div')
  e.id = id
  document.body.appendChild(e)
  return e
}

// Popup children must live inside a container with id="pa-popup" — the
// document-level annotate click handler special-cases `.closest('#pa-popup')`
// to avoid re-arming annotate mode when the user clicks inside the popup
// itself (see annotation.ts's isSkipped / the raw click listener).
function popupChild(id: string, container: HTMLElement, tag = 'div') {
  const e = document.createElement(tag)
  e.id = id
  container.appendChild(e)
  return e
}

// Append fresh nodes without disturbing existing listeners — innerHTML +=
// would re-parse the whole body and drop every listener wireAnnotation attached.
function appendHtml(html: string): void {
  const wrap = document.createElement('div')
  wrap.innerHTML = html
  while (wrap.firstChild) document.body.appendChild(wrap.firstChild)
}

let sent: string[] = []

beforeEach(() => {
  document.body.innerHTML = ''
  annotations.length = 0
  sent = []
  const popup = document.createElement('div')
  popup.id = 'pa-popup'
  document.body.appendChild(popup)
  wireAnnotation(
    el('annToggle'), el('annStrip'), el('annCount'), el('annList'), el('annSend'),
    popup, popupChild('popupLbl', popup), popupChild('popupIn', popup, 'textarea') as HTMLTextAreaElement,
    popupChild('popupOk', popup), popupChild('popupCx', popup),
    () => {},
    async (text: string) => { sent.push(text) },
  )
})

// annotation.ts's document-level capture click listener is never torn down (no
// unwire API) and accumulates one instance per wireAnnotation() call, sharing
// module-scope `_annToggle`/`annotateTarget` state. Leaving annotate mode
// "active" here would let a stale listener from this file swallow clicks in
// OTHER test files that run afterward in the same process (e.g. it stole the
// pin-indicator click in channel-pin.test.ts before this fix).
afterEach(() => { doSetAnnotate(false) })

function annotateViaClick(target: HTMLElement, note: string) {
  doSetAnnotate(true)
  target.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, clientX: 10, clientY: 10 }))
  const popupIn = document.getElementById('popupIn') as HTMLTextAreaElement
  popupIn.value = note
  document.getElementById('popupOk')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
}

test('confirmAnnotation carries the nearest data-bead ancestor id (task-mkns)', () => {
  appendHtml(`<div data-bead="task-mkns"><button id="save">Save</button></div>`)
  const target = document.getElementById('save') as HTMLElement
  annotateViaClick(target, 'looks off')

  expect(annotations.length).toBe(1)
  expect(annotations[0].bead).toBe('task-mkns')
})

test('confirmAnnotation leaves bead undefined when no data-bead ancestor exists', () => {
  appendHtml(`<button id="plain">Plain</button>`)
  const target = document.getElementById('plain') as HTMLElement
  annotateViaClick(target, 'no bead here')

  expect(annotations.length).toBe(1)
  expect(annotations[0].bead).toBeUndefined()
})

test('sendAnnotations prefixes the bead id ahead of the element text when present', async () => {
  appendHtml(`<div data-bead="task-mkns"><button id="save2">Save</button></div>`)
  annotateViaClick(document.getElementById('save2') as HTMLElement, 'note text')

  await sendAnnotations()
  expect(sent.length).toBe(1)
  expect(sent[0]).toContain('task-mkns | Save')
})

test('sendAnnotations omits the bead prefix when the annotation has none', async () => {
  appendHtml(`<button id="plain2">PlainTwo</button>`)
  annotateViaClick(document.getElementById('plain2') as HTMLElement, 'note text')

  await sendAnnotations()
  expect(sent.length).toBe(1)
  expect(sent[0]).toContain('[PlainTwo]')
  expect(sent[0]).not.toContain('|')
})
