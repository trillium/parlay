// ── Connection hold times, and the server idle timeout that must outlive them ─
//
// WHY THIS FILE EXISTS (task-2nwlw). `Bun.serve()` applies a DEFAULT
// `idleTimeout` of 10 seconds: any connection that transfers no bytes for that
// long is closed by the runtime, which answers the client HTTP 200 with an
// EMPTY body. This server deliberately holds two kinds of connection open for
// longer than that, so both were being killed mid-flight in production:
//
//   GET /api/chat/poll    parks a waiter for POLL_WAIT_MS (30s) before it
//                         answers `{"timeout":true}` — Bun closed it at 10s,
//                         so every agent long-poll returned 0 bytes and the
//                         relay saw a server that accepts and never replies.
//   GET /api/chat/events  streams SSE and only emits its first keepalive after
//                         SSE_KEEPALIVE_MS (25s) — Bun closed the connection
//                         ~10s after the initial history flush, which is BEFORE
//                         the keepalive could ever fire even once. The panel
//                         reconnected in a loop, and the runtime tore down
//                         stream controllers that JS timers still held, which
//                         is the teardown path each `panic(main thread):
//                         Segmentation fault at address 0x0` was logged
//                         immediately after.
//
// The invariant is therefore: the server's idleTimeout must exceed EVERY
// intentional hold above. It is derived here rather than written down as a
// literal so that lengthening a hold cannot silently re-break it, and
// timeouts.test.ts fails if the margin is ever lost.

// How long GET /api/chat/poll parks a waiter before answering `{timeout:true}`.
export const POLL_WAIT_MS = 30_000

// How often an open SSE stream emits a `: ka` comment frame. Must stay BELOW
// the derived idle timeout, or the runtime closes the stream before the
// keepalive that exists to prevent exactly that can run.
export const SSE_KEEPALIVE_MS = 25_000

// The longest a healthy connection may legitimately go without bytes.
const LONGEST_HOLD_MS = Math.max(POLL_WAIT_MS, SSE_KEEPALIVE_MS)

// Headroom over that hold. A hold answers at its deadline, so the timeout has
// to sit far enough past it to absorb scheduling jitter and the time spent
// writing the response, without letting a genuinely dead peer linger.
export const IDLE_HEADROOM_MS = 15_000

// `Bun.serve` takes idleTimeout in SECONDS and rejects anything above 255
// (verified against Bun 1.3.11: 255 accepted, 256 throws ERR_INVALID_ARG_TYPE).
export const BUN_MAX_IDLE_TIMEOUT_SEC = 255

// What Bun applies when `idleTimeout` is omitted. Recorded so the tests can
// assert it is too small for this server's holds — that gap is the whole
// reason the option must be passed explicitly rather than left to default.
// (Measured on Bun 1.3.11: a held response is destroyed ~12s in, the nominal
// 10s plus the timer's coarse granularity.)
export const BUN_DEFAULT_IDLE_TIMEOUT_SEC = 10

export const SERVER_IDLE_TIMEOUT_SEC = Math.min(
  BUN_MAX_IDLE_TIMEOUT_SEC,
  Math.ceil((LONGEST_HOLD_MS + IDLE_HEADROOM_MS) / 1000),
)
