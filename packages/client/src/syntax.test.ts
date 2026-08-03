import { test, expect } from 'bun:test'
import { highlight } from './syntax'
import { richText } from './rich-text'

test('highlight: ts keywords, strings, comments, numbers, function calls', () => {
  const out = highlight(`const x = 42; // comment\nfoo("bar");`, 'ts')
  expect(out).toContain('<span class="pa-t-kw">const</span>')
  expect(out).toContain('<span class="pa-t-num">42</span>')
  expect(out).toContain('<span class="pa-t-cmt">// comment</span>')
  expect(out).toContain('<span class="pa-t-str">"bar"</span>')
  expect(out).toContain('<span class="pa-t-fn">foo</span>')
})

test('highlight: python triple-quoted string and keywords', () => {
  const out = highlight(`def f():\n    """doc"""\n    return None`, 'python')
  expect(out).toContain('<span class="pa-t-kw">def</span>')
  expect(out).toContain('<span class="pa-t-kw">return</span>')
  expect(out).toContain('<span class="pa-t-kw">None</span>')
  expect(out).toContain('<span class="pa-t-str">"""doc"""</span>')
})

test('highlight: bash line comment and keywords', () => {
  const out = highlight(`if true; then\n  echo hi # note\nfi`, 'bash')
  expect(out).toContain('<span class="pa-t-kw">if</span>')
  expect(out).toContain('<span class="pa-t-kw">then</span>')
  expect(out).toContain('<span class="pa-t-cmt"># note</span>')
})

test('highlight: json null/true/false as keywords', () => {
  const out = highlight(`{"a": true, "b": null}`, 'json')
  expect(out).toContain('<span class="pa-t-kw">true</span>')
  expect(out).toContain('<span class="pa-t-kw">null</span>')
})

test('highlight: diff is line-based (+/-/@@) not token-based', () => {
  const out = highlight(`+++ b/file\n--- a/file\n@@ -1 +1 @@\n+added\n-removed\n context`, 'diff')
  const lines = out.split('\n')
  expect(lines[0]).toBe('<span class="pa-t-cmt">+++ b/file</span>')
  expect(lines[1]).toBe('<span class="pa-t-cmt">--- a/file</span>')
  expect(lines[2]).toBe('<span class="pa-t-fn">@@ -1 +1 @@</span>')
  expect(lines[3]).toBe('<span class="pa-t-str">+added</span>')
  expect(lines[4]).toBe('<span class="pa-t-kw">-removed</span>')
  expect(lines[5]).toBe(' context')
})

test('highlight: unknown language falls back to plain HTML-escaped text', () => {
  const out = highlight(`<script>alert(1)</script>`, 'weirdlang')
  expect(out).toBe('&lt;script&gt;alert(1)&lt;/script&gt;')
})

test('highlight: HTML-unsafe code is always escaped, even inside tokens', () => {
  const out = highlight(`const s = "<img src=x onerror=alert(1)>";`, 'ts')
  expect(out).not.toContain('<img')
  expect(out).toContain('&lt;img')
})

test('richText: fenced code block renders as highlighted <pre><code> with data-lang', () => {
  const raw = '```ts\nconst x = 1;\n```'
  const out = richText(raw)
  expect(out).toContain('<pre class="pa-code" data-lang="ts"><code>')
  expect(out).toContain('<span class="pa-t-kw">const</span>')
  expect(out).toContain('<span class="pa-t-num">1</span>')
})

test('richText: inline code and plain text still escape and linkify as before', () => {
  const out = richText('see `x < y` at https://example.com')
  expect(out).toContain('<code class="pa-code-inline">x &lt; y</code>')
  expect(out).toContain('<a ')
})
