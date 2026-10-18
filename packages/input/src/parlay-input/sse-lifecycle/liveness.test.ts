import { test, expect, describe, beforeEach } from 'bun:test'
import { parlayInput } from '../../index'
import { FakeEventSource, sleep, STREAM, resetOwnedSse } from './index'

// ── One live connection: reconnect must never stack or leak ────────────────────
//
// Both of these are the same defect wearing two hats. sse.ts keeps ONE
// module-level `es`, and every handler is bound to whatever instance it was
// created for. A handler that acts on `es` instead of its own instance tears
// down whatever is CURRENT — so one late `error` from a superseded connection
// closes the live stream. And each `error` scheduled its own timer without
// checking for a pending one, so two errors in the same tick opened two
// replacements and orphaned the first.

describe('exactly one live EventSource at a time', () => {
  beforeEach(resetOwnedSse)

  test('a second error before the first reconnect fires does NOT stack a connection', async () => {
    const el = document.createElement('input')
    const unsub = parlayInput(el, {
      server: 'http://localhost:4242',
      device: 'd-double',
      reconnect: { initialMs: 10, maxMs: 10 },
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    try {
      FakeEventSource.instances[0].fireError()
      FakeEventSource.instances[0].fireError()   // second error, first timer pending
      await sleep(40)
      // ONE replacement, not two. A third would be a duplicate down-channel
      // applying every input_action twice.
      expect(FakeEventSource.instances).toHaveLength(2)
      expect(FakeEventSource.instances[0].closed).toBe(true)
      expect(FakeEventSource.instances[1].closed).toBe(false)
    } finally {
      unsub()
    }
  })

  test("a superseded connection's late error does NOT close the live stream", async () => {
    const el = document.createElement('input')
    const unsub = parlayInput(el, {
      server: 'http://localhost:4242',
      device: 'd-stale',
      reconnect: { initialMs: 5, maxMs: 5 },
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    try {
      FakeEventSource.instances[0].fireError()
      await sleep(25)
      expect(FakeEventSource.instances).toHaveLength(2)
      const live = FakeEventSource.instances[1]

      // The dead connection's handler fires again (a browser can deliver a
      // queued error after close). It owns nothing and must be inert.
      FakeEventSource.instances[0].fireError()
      expect(live.closed).toBe(false)
      await sleep(25)
      expect(FakeEventSource.instances).toHaveLength(2)  // no reconnect either

      // And the live stream still delivers.
      live.emit('input_action', {
        v: 1, streamId: STREAM, seq: 0, baseVersion: 1,
        actions: [{ verb: 'setText', args: { text: 'still live' } }],
      })
      expect(el.value).toBe('still live')
    } finally {
      unsub()
    }
  })

  test('a superseded connection cannot apply actions or move the resume cursor', async () => {
    const el = document.createElement('input')
    const unsub = parlayInput(el, {
      server: 'http://localhost:4242',
      device: 'd-zombie',
      reconnect: { initialMs: 5, maxMs: 5 },
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    try {
      const dead = FakeEventSource.instances[0]
      dead.emit('message', { id: 'm-from-the-dead' })
      dead.fireError()
      await sleep(25)
      expect(FakeEventSource.instances).toHaveLength(2)

      // Both a message and an input_action from the closed connection are
      // ignored: no cursor movement on the next reconnect, no buffer mutation.
      dead.emit('message', { id: 'm-zombie' })
      dead.emit('input_action', {
        v: 1, streamId: STREAM, seq: 0, baseVersion: 1,
        actions: [{ verb: 'setText', args: { text: 'ZOMBIE' } }],
      })
      expect(el.value).toBe('')

      FakeEventSource.instances[1].fireError()
      await sleep(25)
      expect(FakeEventSource.instances[2].url).toContain('after=m-from-the-dead')
    } finally {
      unsub()
    }
  })

  test('unsubscribe closes the live connection and detaches its handlers', async () => {
    const el = document.createElement('input')
    const unsub = parlayInput(el, {
      server: 'http://localhost:4242',
      device: 'd-unsub',
      reconnect: { initialMs: 5, maxMs: 5 },
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    const live = FakeEventSource.instances[0]
    unsub()
    expect(live.closed).toBe(true)

    // A queued error after teardown must not resurrect the connection.
    live.fireError()
    await sleep(25)
    expect(FakeEventSource.instances).toHaveLength(1)
  })
})