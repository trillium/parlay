# Remote debug log + on-screen mobile console (phone-only bug triage)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`packages/client/src/debug-log.ts` captures `window.onerror`,
`unhandledrejection`, `console.error`/`warn`, and explicit `logTrace()` calls
(used in `thread-scroll.ts` and the `#pa-jump` handler in `jump-button.ts`), batches
them, and POSTs to `${CHAT_BASE}/debug-log` (i.e. `/api/chat/debug-log`).
Disable with `?paDebug=0` or `localStorage['pa-debug-log']='0'`. The server
handler (`packages/server/src/debug-log.ts`, not yet wired — see the section
above) appends formatted lines to `$PARLAY_STATE_HOME/debug.log` (default
`~/.parlay/debug.log`); once wired, read it with `tail -f ~/.parlay/debug.log`.
Disable server-side with `PARLAY_DEBUG_LOG=0`.

`packages/client/src/mobile-console.ts` lazy-loads `eruda` from a CDN for an
on-screen console on the phone itself. Toggle via `?paConsole=1` in the URL
(sticky, persists to localStorage) or a ~600ms long-press on the drawer
trigger button. Default off, zero bundle cost when unused.
