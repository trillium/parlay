# monitor — thinnest per-agent channel reader

`parlay-monitor.sh --agent <id>` is the N-side of the 1-relay + N-monitor split.
Its entire job: copy its agent's relay-fed spool to stdout so a harness Monitor
tool wakes the agent. It is a bash wrapper that **enrolls then runs `tail -F`**,
so the running footprint is `tail` plus a supervising shell (~1.2MB of real
reader) — not a ~40MB bun poller.

The shell stays instead of `exec`ing away because something has to outlive the
enroll and *end* the reader: a bare `exec tail -F` has no way to notice its
launcher died, and that is exactly how 168 readers piled up (robots-3pvi below).

## Why a wrapper, not a compiled binary

A hand-written reader cannot beat `tail -F` at pure byte-copying (measured:
`tail -F` ≈ 1200KB RSS vs a Rust reader ≈ 1776KB). You can't out-optimize a
system binary at copying bytes. So the monitor *is* `tail`; the only custom code
is the few lines of enroll + spool-path resolution that correctness demands.

## Run

```sh
./parlay-monitor.sh --agent main-agent [--notify-safe]
```

Or through the CLI (the default `parlay monitor` path now routes here):

```sh
parlay monitor --agent main-agent [--notify-safe]
```

The harness enrolls it exactly as before:

```
Monitor({ command: "parlay monitor --agent <id>", persistent: true })
```

## What it does

1. **Enroll** — `POST /register {"agent":"<id>"}` to the relay's Unix control
   socket. Idempotent; the relay creates the spool and starts (or reuses) the
   upstream poll loop for this channel. This is the one-call enroll: the single
   startup action both registers the agent and begins streaming.
2. **Stream** — `tail -n0 -F <spool>`, supervised (not `exec`ed).
   - `-n0` starts at end-of-file — no replay of already-consumed lines.
   - `-F` follows by name and **re-opens on truncate/rotate/recreate**. This is
     the "channel re-open after relay restart" guarantee: restart the relay, the
     spool is recreated, `tail -F` reattaches without restarting the monitor.
   - With `--notify-safe`, `tail -F` is piped through an `awk` that caps each
     over-budget line (`PARLAY_NOTIFY_BUDGET`, default 400 chars) and appends a
     "fetch full text" pointer — killing the monitor's process group reaps both
     halves of that pipeline.
     Harness Monitor tools truncate long single-event lines mid-word for
     display; this makes that recoverable instead of silent. Off by default so
     raw programmatic consumers keep getting complete, unmodified lines.

## Canonical relay

There is one relay runtime per user: `$TMPDIR/parlay` (or the explicit
`$PARLAY_RELAY_RUNTIME` used by hermetic tests). The CLI resolves the upstream
server before starting a monitor, and all monitors enroll through this single
relay. The relay is supervised by `com.parlay.relay`; `ensure-up.sh` waits for
that relay or starts the same binary when developing from a checkout.

Regression coverage: `parlay-monitor.test.sh` uses a unix-socket stub to prove
canonical enrollment and verify-only preflight without touching live state.

## Reader lifetime + one reader per channel (robots-3pvi)

Nothing ever ended a `tail -F`. A harness kills only the shell it spawned;
everything below reparents to init and keeps running, and a reader on a quiet
channel never writes, so it never even earns a `SIGPIPE`. Measured on the
captain's box when this was found: **168 live readers, 142 with no launcher left
anywhere in their ancestry**, oldest 3 days, 101 distinct channels, 13 channels
with more than one reader, one (`shape.chan`) with **20**. The spool is
append-only and never truncated, so every extra reader re-delivers every
directive — a channel with 20 readers wakes 20 sessions, 19 of them dead.

Three mechanisms, one per layer, because the leak had two roots:

1. **Reader dies with its launcher** (`parlay-monitor.sh`). The script keeps the
   reader as a child and runs a watchdog that ends it when either the script's
   supervisor dies or the script's own `PPID` changes (reparenting = the launcher
   is gone). `TERM`/`INT`/`HUP` are trapped and tear the reader down too — which
   requires `wait`, not a foreground pipeline: bash defers a trap until the
   current foreground command finishes, and `tail -F` never finishes.
2. **CLI dies with *its* launcher** (`internal/monitor/monitor.go`). 73 of the
   168 stranded chains were rooted at an orphaned `parlay-cli`, which the script's
   watchdog cannot see — from down there its own launcher is alive and healthy.
   The CLI puts the script in its own process group (`Setpgid`), forwards signals
   to that group, and polls `os.Getppid()` for its own orphaning.
3. **One reader per channel.** Enrolling evicts any existing reader of the same
   spool (announced on stderr, never silent), so a re-armed Monitor replaces its
   predecessor instead of doubling up on it.

`PARLAY_MONITOR_NO_ORPHAN_EXIT=1` opts out of 1 and 2 for a deliberate
daemonization. Readers are matched as a **whole command line**, never a
`pgrep -f` regex — a metacharacter in a spool path must not be able to widen a
kill.

### Cleaning up what already leaked

```sh
parlay monitor --reap            # dry run: what would be killed, and why
parlay monitor --reap --apply    # TERM, then KILL what survives
```

A reader is an ORPHAN when climbing its ancestry reaches init without passing
through any process that isn't part of a monitor chain; a working monitor always
has a foreign root (tmux/harness shell, herdr, bun), so a live reader is never
mistaken for garbage. The sweep is **scoped to the runtime dir**
(`$PARLAY_RELAY_RUNTIME`, default `$TMPDIR/parlay`) and everything nested under
it — an unscoped host-wide kill is not something a test or a second server's
relay may be allowed to run.

Regression coverage lives in section D of `parlay-monitor.test.sh`: launcher
killed → reader dies; second monitor evicts the first; `--reap` lists an orphan,
spares a live reader, kills nothing without `--apply`, and never reaches outside
its runtime dir. Go-side: `internal/monitor/monitor_test.go`.

## Env

| Var | Default | Meaning |
|-----|---------|---------|
| `PARLAY_SERVER` | `http://localhost:4242` | upstream server resolved by the CLI |
| `PARLAY_RELAY_RUNTIME` | `$TMPDIR/parlay` | runtime dir with `relay.sock` + `<agent>.chan` |
| `PARLAY_RELAY_SOCK`    | `<runtime>/relay.sock` | explicit control-socket path |
| `PARLAY_NOTIFY_BUDGET` | `400` | `--notify-safe` per-line char budget before truncating |
| `PARLAY_MONITOR_WATCH_INTERVAL` | `15` | seconds between orphan checks (shared by the script's watchdog and the CLI's) |
| `PARLAY_MONITOR_NO_ORPHAN_EXIT` | unset | `1` = keep streaming after the launcher dies (deliberate daemonization) |

## Failure modes

- Relay socket missing → exits 1 with "start the relay first" (never silently
  streams a stale spool with no live upstream).
- Control-socket path over the 103-byte `sun_path` limit → exits 1 naming the
  length and the path, instead of letting the relay fail with `bind: invalid
  argument`.
- Relay rejects the enroll (bad id, shutting down) → exits 1 with the relay's
  error echoed.
- Bad `--agent` (not a kebab-slug) → exits 2.
- Launcher dies → the reader is stopped within `PARLAY_MONITOR_WATCH_INTERVAL`
  and the monitor exits, instead of tailing the channel forever as an init child.
- Channel already has a reader → the old one is evicted (with a stderr notice)
  rather than left racing the new one for the same append-only spool.
