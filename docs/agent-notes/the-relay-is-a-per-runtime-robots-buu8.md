# The relay is a per-user singleton on the canonical runtime dir (robots-buu8)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`parlay listen` shells out to `tools/monitor/parlay-monitor.sh`, which enrolls
over the relay's canonical Unix control socket. The relay is a single
per-user process supervised by `com.parlay.relay`; hermetic tests may provide an
explicit `$PARLAY_RELAY_RUNTIME`. `PARLAY_SERVER` selects the upstream for the
CLI/server request, not a second relay runtime.

**Unix socket paths are capped at 104 bytes (`sun_path`).** The canonical
`$TMPDIR/parlay/relay.sock` path stays within that limit; any new path under the
relay runtime must budget against it using `parlay_relay_sock_path_ok` in
`lib.sh`.

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
