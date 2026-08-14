/**
 * parlay-input — a thin, framework-agnostic DOM input wrapper for parlay's
 * real input protocol: REST up-channel + a single shared Server-Sent Events
 * down-channel, with client-owned version/seq staleness handling.
 *
 * Dependency-free and framework-agnostic: it operates on a plain `Element` and
 * takes injectable `fetch`/`EventSource`/`subscribe` so a host can share its
 * existing SSE connection (or a test can drive a fake one). See the package
 * README for the full protocol writeup; the implementation lives under
 * `./parlay-input/` (`core.ts`, `sse.ts`, `dom.ts`, `types.ts`).
 */

export {
  PROTOCOL_V,
  ACTION_TTL_MS,
  type ParlayAction,
  type ActionEnvelope,
  type ApplyResult,
  type Tab,
  type Unsubscribe,
  type ActionContext,
  type ParlayInputOptions,
} from './parlay-input/types'
export { getDeviceId } from './parlay-input/dom'
export { parlayInput } from './parlay-input/core'
