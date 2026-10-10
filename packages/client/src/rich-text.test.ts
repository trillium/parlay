// richText() renders agent/tool turn text into the thread with innerHTML
// (thread.ts), and that text routinely carries bytes the agent did not author:
// WebFetch output, a file it Read, a build log. So the escaping boundary it
// relies on is load-bearing, and the code-fence info string is the sharpest
// edge in it — it is the one user-controlled value interpolated into an
// ATTRIBUTE.

import { test, expect, describe } from 'bun:test'
import { richText } from './rich-text'

function parse(html: string): HTMLElement {
  const host = document.createElement('div')
  host.innerHTML = html
  return host
}

// Any element carrying an event-handler attribute means an interpolation broke
// out of the attribute it was written into.
function eventHandlers(html: string): string[] {
  const host = parse(html)
  const out: string[] = []
  for (const el of Array.from(host.querySelectorAll('*'))) {
    for (const attr of Array.from(el.attributes)) if (/^on/i.test(attr.name)) out.push(attr.name)
  }
  return out
}

describe('fenced blocks', () => {
  test('renders a complete fence as a highlighted <pre><code>', () => {
    const host = parse(richText('```js\nconst a = 1\n```'))
    expect(host.querySelector('pre.pa-code')).not.toBeNull()
    expect(host.querySelector('code')!.textContent).toContain('const a = 1')
  })

  test('records the info string as data-lang', () => {
    const host = parse(richText('```ts\nlet x\n```'))
    expect(host.querySelector('pre')!.getAttribute('data-lang')).toBe('ts')
  })

  test('an unterminated fence falls through as plain escaped text', () => {
    const html = richText('```js\nconst a = 1')
    expect(html).not.toContain('<pre')
    expect(html).toContain('const a = 1')
  })

  test('a fence info string cannot inject an event handler', () => {
    const hostile = '```js" onmouseover="alert(1)\nconst a = 1\n```'
    expect(eventHandlers(richText(hostile))).toEqual([])
  })

  test('a fence info string cannot close the attribute and open a tag', () => {
    const hostile = '```"><img src=x onerror=alert(1)>\ncode\n```'
    expect(parse(richText(hostile)).querySelector('img')).toBeNull()
  })

  test('the escaped info string still survives as readable text', () => {
    const host = parse(richText('```js" onx="1\ncode\n```'))
    expect(host.querySelector('pre')!.getAttribute('data-lang')).toContain('onx')
  })
})

describe('non-fenced text', () => {
  test('renders inline code in a <code> element', () => {
    const host = parse(richText('run `bd list` now'))
    expect(host.querySelector('code.pa-code-inline')!.textContent).toBe('bd list')
  })

  test('escapes raw HTML in message prose', () => {
    const host = parse(richText('hello <img src=x onerror=alert(1)> world'))
    expect(host.querySelector('img')).toBeNull()
    expect(host.textContent).toContain('<img src=x onerror=alert(1)>')
  })

  test('linkifies a URL outside a fence', () => {
    const host = parse(richText('see https://example.com/x'))
    expect(host.querySelector('a')!.getAttribute('href')).toBe('https://example.com/x')
  })

  test('does not linkify a URL inside a fence', () => {
    const host = parse(richText('```\nhttps://example.com/x\n```'))
    expect(host.querySelector('a')).toBeNull()
  })

  test('a hostile URL in prose cannot inject an event handler', () => {
    expect(eventHandlers(richText('https://x.example/" onmouseover="alert(1)'))).toEqual([])
  })

  test('tolerates a null/undefined body', () => {
    expect(richText(undefined as unknown as string)).toBe('')
    expect(richText(null as unknown as string)).toBe('')
  })
})