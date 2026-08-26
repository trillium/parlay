# The relay is a per-runtime-dir singleton bound to ONE server — `PARLAY_SERVER` alone does not scope it (robots-buu8)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Setting `PARLAY_SERVER` does **not** by itself keep a sandbox off production.
`parlay listen` shells out to `tools/monitor/parlay-monitor.sh`, which enrolls
over a relay's Unix control socket — and a relay process is a singleton per
runtime dir, bound to whatever upstream server it was started with. On the
captain's box the shared `$TMPDIR/parlay` relay is bound to production `:31337`,
so enrolling there registered the agent in the captain's **live** registry no
matter what `PARLAY_SERVER` said. Nothing hardcodes `31337` in the monitor or
CLI; the leak was entirely via the shared daemon.

The fix (`tools/relay/deploy/lib.sh`, `ensure-up.sh`, `parlay-monitor.sh`):
the canonical runtime dir is reserved for the default server, any other
`PARLAY_SERVER` gets `<canonical>/srv-<hash>` and its own relay, and the monitor
reads the relay's own `GET /agents` → `server` and refuses to `POST /register`
on a mismatch. Anything else in this repo that talks to the relay must reason
about *which relay*, not just which `PARLAY_SERVER` — see
`tools/monitor/NOTES.md` § Upstream-server scoping, and
`tools/monitor/parlay-monitor.test.sh` for the reproduction.

**Unix socket paths are capped at 104 bytes (`sun_path`), and macOS's `$TMPDIR`
eats ~53 of them.** `/var/folders/xx/<28 chars>/T/parlay/relay.sock` leaves very
little room — this is why scoped runtime dirs are named `srv-<10 hex>` and not
something readable. Over the cap, `bind()` fails with `invalid argument`, which
names neither the limit nor the path. Any new path under the relay runtime dir
must budget against this; `parlay_relay_sock_path_ok` in `lib.sh` is the check.


ensure-up.sh used to `launchctl kickstart -k` on any failed health read. `-k`
**kills** a running job, so it killed relays that were alive but mid-startup —
the relay used to bind its control socket only *after* replaying every spooled
agent (~7s for 206 spools), leaving `/health` unanswerable that whole time — and
then reported them dead at its 10s bound. Agents came out looking enrolled
(`parlay claim` is a plain POST to Pulse) with a dead listen loop.

Two rules this leaves behind, both worth applying to any supervised daemon here:

- **Probe for a live pid before starting anything.** `parlay_relay_launchd_pid`
  (in `tools/relay/deploy/lib.sh`) reads it from `launchctl print`; a pid means
  the process exists and only needs waiting on. `-k` is reserved for an explicit
  `ensure-up.sh --force-restart` and for `install.sh` (which is replacing the
  binary and genuinely must restart).
- **Never bound a startup wait with a fixed timeout that scales with the
  fleet.** Use `parlay_relay_wait_health`: a base budget
  (`$PARLAY_RELAY_HEALTH_WAIT`, default 45s) re-granted whenever the daemon's
  log grows, capped by `$PARLAY_RELAY_HEALTH_MAX_WAIT` — waits out real work,
  still fails fast on a wedged, quiet process.

The relay side now binds and serves before `resumeFromSpools`, so `/health`
answers in milliseconds at any fleet size. `TestControlSocketBindsBeforeSpoolResume`
(`tools/relay/startup_test.go`) pins that ordering against the process's own log;
`tools/relay/deploy/ensure-up.test.sh` pins the start/wait policy with stubbed
`launchctl`/`curl`. Anything reordering relay startup must keep the bind first.
