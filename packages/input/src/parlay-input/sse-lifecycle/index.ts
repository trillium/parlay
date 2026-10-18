// ── Owned-SSE lifecycle suites — barrel ───────────────────────────────────────
//
// The owned EventSource (`sse.ts`) has three separable failure surfaces, and
// each grew its own suite as bugs appeared:
//
//   reconnect.test.ts  the happy path + resume cursor + exponential backoff
//   liveness.test.ts   exactly ONE live EventSource (no stacking, no zombie)
//   lifecycle.test.ts  subscribe/unsubscribe + device identity
//
// They share one reset because they all drive the same module-level fake: a
// stale `FakeEventSource.instances` from a previous test is what makes a
// liveness assertion vacuous, and a persisted localStorage id makes the
// identity assertion compare a value to itself.

export { FakeEventSource, harness, sleep, STREAM } from '../harness'

import { FakeEventSource } from '../harness'

/** Reset every piece of state the owned-SSE suites share. */
export function resetOwnedSse(): void {
  try { localStorage.clear() } catch { /* no localStorage in this runtime */ }
  FakeEventSource.instances = []
}
