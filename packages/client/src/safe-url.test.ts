import { describe, expect, test } from 'bun:test'
import { isSafeUrl } from './safe-url'

describe('isSafeUrl', () => {
  test('accepts http(s) and relative paths', () => {
    for (const u of ['http://a.test/x', 'https://a.test/', '/page/', 'page.html', '?q=1'])
      expect(isSafeUrl(u)).toBe(true)
  })
  test('rejects executable and content schemes', () => {
    for (const u of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', ' javascript:alert(1)', 'data:text/html,<b>x</b>', 'vbscript:x', 'file:///etc/passwd', 'blob:http://a.test/1'])
      expect(isSafeUrl(u)).toBe(false)
  })
  test('rejects non-strings and empty', () => {
    for (const u of [undefined, null, 5, {}, ''])
      expect(isSafeUrl(u)).toBe(false)
  })
})
