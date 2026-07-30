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

## Env

| Var | Default | Meaning |
|-----|---------|---------|
| `PARLAY_RELAY_RUNTIME` | `$TMPDIR/parlay` (or `/tmp/parlay`) | runtime dir with `relay.sock` + `<agent>.chan` |
| `PARLAY_RELAY_SOCK`    | `<runtime>/relay.sock` | explicit control-socket path |
| `PARLAY_NOTIFY_BUDGET` | `400` | `--notify-safe` per-line char budget before truncating |

## Failure modes

- Relay socket missing → exits 1 with "start the relay first" (never silently
  streams a stale spool with no live upstream).
- Relay rejects the enroll (bad id, shutting down) → exits 1 with the relay's
  error echoed.
- Bad `--agent` (not a kebab-slug) → exits 2.
