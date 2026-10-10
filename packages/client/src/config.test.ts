// esc()/linkify() are the client panel's only text→HTML boundary: every message
// body, agent label, code-fence info string and url pill goes through them, so a
// gap here is a live injection, not a cosmetic bug. esc() is attribute-safe
// because three call sites interpolate it straight into an attribute.

import { test, expect, describe } from 'bun:test'
import { esc, linkify, panelCapsQuery } from './config'

// Parse a fragment and report whether any element carries an event handler or a
// script-ish attribute — i.e. whether the escaping actually held.
function injectedAttrs(html: string): string[] {
  const host = document.createElement('div')
  host.innerHTML = html
  const out: string[] = []
  for (const el of Array.from(host.querySelectorAll('*'))) {
    for (const attr of Array.from(el.attributes)) {
      if (/^on/i.test(attr.name) || attr.name === 'srcset' || /javascript:/i.test(attr.value)) {
        out.push(`${el.tagName.toLowerCase()}[${attr.name}=${attr.value}]`)
      }
    }
  }
  return out
}

describe('esc — text context', () => {
  test('escapes the HTML-significant characters', () => {
    expect(esc('<b>&</b>')).toBe('&lt;b&gt;&amp;&lt;/b&gt;')
  })

  test('coerces non-strings instead of throwing', () => {
    expect(esc(undefined)).toBe('undefined')
    expect(esc(null)).toBe('null')
    expect(esc(42)).toBe('42')
  })

  test('does not double-escape an already-escaped entity', () => {
    // & must lead, or &amp; becomes &amp;amp; on every render pass.
    expect(esc('&amp;')).toBe('&amp;amp;')
  })
})

describe('esc — attribute context', () => {
  test('escapes double quotes', () => {
    expect(esc('say "hi"')).toBe('say &quot;hi&quot;')
  })

  test('escapes single quotes', () => {
    expect(esc("it's")).toBe('it&#39;s')
  })

  test('a quote-bearing value cannot break out of a double-quoted attribute', () => {
    const hostile = 'x" onmouseover="alert(1)'
    expect(injectedAttrs(`<span title="${esc(hostile)}">t</span>`)).toEqual([])
  })

  test('a quote-bearing value cannot break out of a single-quoted attribute', () => {
    const hostile = "x' onmouseover='alert(1)"
    expect(injectedAttrs(`<span title='${esc(hostile)}'>t</span>`)).toEqual([])
  })

  test('angle brackets in an attribute stay inside it', () => {
    const hostile = '"><img src=x onerror=alert(1)>'
    expect(injectedAttrs(`<a href="${esc(hostile)}">t</a>`)).toEqual([])
  })
})

describe('linkify', () => {
  test('wraps a bare http(s) URL', () => {
    expect(linkify('see https://example.com/a')).toBe(
      'see <a href="https://example.com/a" target="_blank" rel="noopener noreferrer">https://example.com/a</a>',
    )
  })

  test('wraps a markdown link and keeps its label', () => {
    expect(linkify('[docs](https://example.com)')).toContain('>docs</a>')
  })

  test('does not linkify a non-http scheme', () => {
    expect(linkify('javascript:alert(1)')).toBe('javascript:alert(1)')
  })

  test('a quote in a URL cannot escape the href attribute', () => {
    const html = linkify(esc('https://example.com/" onmouseover="alert(1)'))
    expect(html).not.toContain('onmouseover="alert')
    expect(injectedAttrs(html)).toEqual([])
  })

  test('a URL carrying an apostrophe is still linked whole', () => {
    // esc() encodes ' as &#39;, so the bare-URL matcher no longer truncates.
    expect(linkify(esc("https://example.com/it's"))).toContain("href=\"https://example.com/it&#39;s\"")
  })
})

describe('panelCapsQuery', () => {
  test('carries an encoded declaration with the documented schema', () => {
    const q = panelCapsQuery('inst-1')
    expect(q.startsWith('&caps=')).toBe(true)
    const decl = JSON.parse(decodeURIComponent(q.slice('&caps='.length)))
    expect(decl.schema).toBe('1.0.0')
    expect(decl.surface).toEqual({ kind: 'panel', instance: 'inst-1' })
    expect(Object.keys(decl.accepts).sort()).toEqual(['device_cmd', 'draft', 'input_action', 'navigate', 'reload'])
  })

  test('percent-encodes the instance so the query param stays one param', () => {
    expect(panelCapsQuery('a b&c=d')).not.toContain('&c=d')
  })
})