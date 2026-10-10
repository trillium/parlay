// esc()/linkify() are the client panel's only text→HTML boundary: every message
// body, agent label, code-fence info string and url pill goes through them, so a
// gap here is a live injection, not a cosmetic bug. esc() is attribute-safe
// because three call sites interpolate it straight into an attribute.

import { test, expect, describe } from 'bun:test'
import { esc, linkify, panelCapsQuery, cssColor, byAttr, firstByAttr } from './config'

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
// cssColor() guards the OTHER boundary esc() cannot: agent colours are
// interpolated into `style="..."` attributes and handed to the CSSOM, so a
// value must be a colour TOKEN, not merely an escaped string — `url(…)` is a
// valid `background` shorthand and would make the panel fetch an attacker's URL.
describe('cssColor', () => {
  const FB = 'var(--pa-green)'
  test('passes real colour tokens through untouched', () => {
    for (const v of ['#fff', '#ff8800', '#ff8800cc', 'rebeccapurple', 'transparent',
                     'var(--pa-blue)', 'rgb(1, 2, 3)', 'rgba(1,2,3,.5)', 'hsl(210 50% 40%)']) {
      expect(cssColor(v, FB)).toBe(v)
    }
  })

  test('trims surrounding whitespace rather than rejecting it', () => {
    expect(cssColor('  #ff8800 ', FB)).toBe('#ff8800')
  })

  test('rejects anything that could break out of a style attribute', () => {
    for (const v of ['#000" onmouseover="alert(1)', "#000' onx='y", 'red; background:url(//evil/x)',
                     'url(https://evil.example/track.png)', 'red}</style><script>x', 'expression(alert(1))']) {
      expect(cssColor(v, FB)).toBe(FB)
    }
  })

  test('a url() in the function position cannot smuggle a fetch through', () => {
    expect(cssColor('rgb(url(https://evil.example/x))', FB)).toBe(FB)
  })

  test('non-strings and blanks fall back instead of throwing', () => {
    for (const v of [undefined, null, 42, {}, [], '', '   ']) {
      expect(cssColor(v, FB)).toBe(FB)
    }
  })
})
// byAttr()/firstByAttr() replace `[data-pa-id="${id}"]` selector interpolation.
// A quote or bracket in the value used to either rewrite the selector (matching
// a DIFFERENT message) or make it unparseable (a DOMException thrown out of the
// SSE message handler). Message ids reach the client off a JSONL log that only
// mostly holds server-shaped ids, so the value is compared, never spliced.
describe('byAttr / firstByAttr', () => {
  // The classic selector-injection payload: spliced into
  // `[data-pa-id="…"]` this becomes two attribute selectors and matches VICTIM.
  const HOSTILE = 'x"] , [data-pa-id="victim'

  function fixture() {
    document.body.innerHTML = ''
    for (const id of ['victim', HOSTILE, 'm1']) {
      const el = document.createElement('div')
      el.setAttribute('data-pa-id', id)
      document.body.appendChild(el)
    }
  }

  test('finds an element by exact value, metacharacters and all', () => {
    fixture()
    const found = byAttr(document, 'data-pa-id', HOSTILE)
    expect(found.length).toBe(1)
    expect(found[0].getAttribute('data-pa-id')).toBe(HOSTILE)
  })

  test('a selector-shaped value cannot match a different element', () => {
    fixture()
    expect(byAttr(document, 'data-pa-id', HOSTILE).map(e => e.getAttribute('data-pa-id')))
      .toEqual([HOSTILE])
  })

  test('an unparseable value returns nothing instead of throwing', () => {
    fixture()
    for (const bad of ['a" *', 'a] b', 'a\\', 'a\n*', '"', 'm1"']) {
      expect(byAttr(document, 'data-pa-id', bad)).toEqual([])
    }
  })

  test('firstByAttr returns the first match in document order, else null', () => {
    fixture()
    expect(firstByAttr(document, 'data-pa-id', 'm1')!.textContent).toBe('')
    expect(firstByAttr(document, 'data-pa-id', 'nope')).toBeNull()
  })

  test('scopes to the given root and stringifies non-string values', () => {
    fixture()
    const scope = document.querySelectorAll('div')[0]!
    expect(byAttr(scope, 'data-pa-id', 42)).toEqual([])
    const el = document.createElement('div')
    el.setAttribute('data-n', '7')
    scope.appendChild(el)
    expect(byAttr(scope, 'data-n', 7).map(e => e.getAttribute('data-n'))).toEqual(['7'])
  })
})
