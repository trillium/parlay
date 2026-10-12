import { describe, expect, test } from 'bun:test'
import { renderDevices } from './devices-panel'
import { deviceLabel } from './roll-call'

const now = Date.parse('2026-10-10T12:00:30Z')
describe('devices panel', () => {
  test('lists each device, marks this one and stale ones', () => {
    const lines = renderDevices([
      { clientId: 'a', stale: false, lastSeen: '2026-10-10T12:00:20Z', state: { device: 'me', deviceLabel: 'Mac · Chrome', version: '1.2' } },
      { clientId: 'b', stale: true, lastSeen: '2026-10-10T11:00:30Z', state: { device: 'x', deviceLabel: 'iPhone · Safari' } },
    ], 'me', now)
    expect(lines[0]).toBe('Mac · Chrome · (this device) · live · seen 10s ago · v1.2')
    expect(lines[1]).toBe('iPhone · Safari · STALE · seen 1h ago')
  })
  test('empty registry says so', () => {
    expect(renderDevices([], 'me')).toEqual(['no device has announced itself'])
  })
  test('labels iPhone Safari and Mac Chrome', () => {
    expect(deviceLabel('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605 Version/17 Mobile/15E148 Safari/604.1')).toBe('iPhone · Safari')
    expect(deviceLabel('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537 Chrome/120 Safari/537')).toBe('Mac · Chrome')
  })
})
