# monitor — thinnest per-agent channel reader

`parlay-monitor.sh --agent <id>` is the N-side of the 1-relay + N-monitor split.
Its entire job: copy its agent's relay-fed spool to stdout so a harness Monitor
tool wakes the agent. It is a bash wrapper that **enrolls then execs `tail -F`**,
so the running process footprint is `tail` alone (~1.2MB) — not a ~40MB bun poller.

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
2. **Stream** — `exec tail -n0 -F <spool>`.
   - `-n0` starts at end-of-file — no replay of already-consumed lines.
   - `-F` follows by name and **re-opens on truncate/rotate/recreate**. This is
     the "channel re-open after relay restart" guarantee: restart the relay, the
     spool is recreated, `tail -F` reattaches without restarting the monitor.
   - With `--notify-safe`, `tail -F` is piped through an `awk` that caps each
     over-budget line (`PARLAY_NOTIFY_BUDGET`, default 400 chars) and appends a
     "fetch full text" pointer, so `exec` can't be used (a pipeline can't
     replace the shell) — killing the monitor's process group still reaps both.
     Harness Monitor tools truncate long single-event lines mid-word for
     display; this makes that recoverable instead of silent. Off by default so
     raw programmatic consumers keep getting complete, unmodified lines.

## Upstream-server scoping (robots-buu8)

A relay process is a per-runtime-dir **singleton bound to one upstream server**,
chosen when it starts. So the relay you enroll on — not your `$PARLAY_SERVER` —
decides whose registry you land in. Before this was handled, a sandbox running
`PARLAY_SERVER=http://127.0.0.1:<scratch> parlay listen --agent verify-agent`
enrolled through the shared `$TMPDIR/parlay` relay (bound to production `:31337`)
and the agent appeared in the captain's **live** registry, with no error anywhere.

Two mechanisms, both needed:

1. **Scoped runtime dir.** The canonical dir is reserved for the default server
   (`http://localhost:31337`). Any other `$PARLAY_SERVER` resolves to
   `<canonical>/srv-<hash>` and therefore gets its own relay
   (`parlay_relay_scoped_runtime_dir` in `../relay/deploy/lib.sh`). The monitor
   exports the result so `ensure-up.sh` and the relay launcher agree, announces
   the scoping on stderr, and drops a `server` marker file in the dir naming the
   upstream. An explicit `$PARLAY_RELAY_RUNTIME` still wins.
   - The dir name is a bare hash, not a readable slug, because a Unix socket path
     is capped at 104 bytes (`sun_path`) and `$TMPDIR` already eats ~53 of them.
     A readable slug overflowed it and the relay died with `bind: invalid
     argument`, which names neither the limit nor the path.
   - Scoped relays are unsupervised (no launchd) and log to
     `<runtime>/relay.{out,err}.log`, keeping production's crash trail clean.
2. **Pre-enroll refusal.** Whatever socket is resolved, the monitor reads the
   relay's own `GET /agents` → `server` and **exits 1 before `POST /register`**
   if it disagrees with `$PARLAY_SERVER`. This is the guard that holds when
   scoping is bypassed — explicit `$PARLAY_RELAY_RUNTIME`/`$PARLAY_RELAY_SOCK`, a
   hand-started relay, or a checkout without `lib.sh`. A relay that reports
   nothing is "unknown", not a mismatch, so old relays still work.

Regression harness: `parlay-monitor.test.sh` (stub relay on a unix socket; the
cross-server case asserts `/register` is *never* reached).

## Env

| Var | Default | Meaning |
|-----|---------|---------|
| `PARLAY_SERVER` | `http://localhost:31337` | upstream server; a non-default value gets its own scoped runtime dir + relay |
| `PARLAY_RELAY_RUNTIME` | `$TMPDIR/parlay`, or `$TMPDIR/parlay/srv-<hash>` when `PARLAY_SERVER` is non-default | runtime dir with `relay.sock` + `<agent>.chan` |
| `PARLAY_RELAY_SOCK`    | `<runtime>/relay.sock` | explicit control-socket path |
| `PARLAY_NOTIFY_BUDGET` | `400` | `--notify-safe` per-line char budget before truncating |

## Failure modes

- Relay socket missing → exits 1 with "start the relay first" (never silently
  streams a stale spool with no live upstream).
- Relay bound to a different upstream server than `$PARLAY_SERVER` → exits 1
  *before* enrolling, rather than registering in the wrong server's registry.
- Control-socket path over the 103-byte `sun_path` limit → exits 1 naming the
  length and the path, instead of letting the relay fail with `bind: invalid
  argument`.
- Relay rejects the enroll (bad id, shutting down) → exits 1 with the relay's
  error echoed.
- Bad `--agent` (not a kebab-slug) → exits 2.
