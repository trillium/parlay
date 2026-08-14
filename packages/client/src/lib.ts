// ── parlay-client public library entry ──────────────────────────────────────
// For EXTERNAL host apps (e.g. herdr-web) that want voice-command input
// handling backed by a running parlay server, without embedding the full
// in-page panel (init.ts). Re-exports just the server-eval dispatcher:
//
//   up:   scheduleEval() POSTs the (debounced) buffer to the parlay server's
//         /api/chat/eval, which relays to the compiled Go phrase-matching
//         engine (packages/eval-engine) and returns/broadcasts an action
//         envelope over the same server's /api/chat/events SSE stream.
//   down: applyEnvelope() drives those actions (setText/clear/submitNow/…)
//         against a host-supplied CommandContext — the ONLY surface a host
//         needs to implement (see types.ts).
//
// See packages/server/src/eval-relay.ts for the wire protocol this depends on,
// and web/src/ParlayMobileInput.tsx (herdr-web) for a reference host.
export type { CommandContext, Command, CommandMatch, MatchMode } from './commands/types'
export type { Action, ActionEnvelope, ApplyResult, EvalCtx, PickerChannel, PickerSender } from './commands/dispatcher/types'
export { PROTOCOL_V, ACTION_TTL_MS } from './commands/dispatcher/types'
export { bumpInputVersion, currentInputVersion, scheduleEval, setEvalServerBaseUrl } from './commands/dispatcher/up'
export { setDispatcherContext, applyEnvelope } from './commands/dispatcher/apply'
export { telemetry } from './commands/dispatcher/telemetry'
export { PA_VERSION } from './version'
export type { ParlaySettings } from './settings-modal/types'
export { DEFAULTS as PARLAY_SETTINGS_DEFAULTS } from './settings-modal/types'
