import { describe, expect, test } from 'bun:test'
import { relTime, fmtDuration } from './format'

describe('relTime', () => {
  test('buckets: seconds, minutes, hours', () => {
    const now = Date.now()
    expect(relTime(new Date(now - 5_000).toISOString())).toBe('5s ago')
    expect(relTime(new Date(now - 90_000).toISOString())).toBe('1m ago')
    expect(relTime(new Date(now - 2 * 3_600_000).toISOString())).toBe('2h ago')
  })
})

describe('fmtDuration', () => {
  test('buckets: s, m+s, h+m', () => {
    expect(fmtDuration(4_000)).toBe('4s')
    expect(fmtDuration(90_000)).toBe('1m 30s')
    expect(fmtDuration(3_720_000)).toBe('1h 2m')
  })
})
