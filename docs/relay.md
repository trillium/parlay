# Relay

**Code:** [`tools/relay`](../tools/relay) — its own Go module, built by `tools/relay/build.sh`.

The relay is the single central fan-out for agent channels, described in its
own header comment (`tools/relay/main.go`, verified 2026-09-03): instead of
one independent Bun long-poll loop per agent (~40MB per process), **one**
relay process holds one upstream `GET /api/chat/poll` long-poll loop per
registered agent against the chat server, and appends each inbound message to
that agent's private spool file as a `CHAT_MSG` line. Each agent's
`parlay monitor`/`listen` then just tails its own ~1.2MB spool file instead of
polling the server itself — see [`docs/monitor.md`](monitor.md) for the
consumer side.

Interface, all local to the machine:

- **Spool file** — `{runtime-dir}/<agent>.chan`, plain text lines
  (`CHAT_MSG|<id>|<role>|<text>[|from:<sender>]`).
- **Control socket** — a Unix domain socket at `{runtime-dir}/relay.sock`
  (`POST /register`, `POST /unregister`, `GET /agents`, `GET /health`), where
  `runtime-dir` defaults to `$TMPDIR/parlay`.

Per the root `CLAUDE.md`: the relay is a **per-runtime-dir singleton bound to
one server** — `PARLAY_SERVER` alone does not scope which relay a given
runtime dir's socket belongs to, and Unix socket paths cap at 104 bytes, which
constrains how deep a runtime dir can nest. The canonical runtime dir is
reserved specifically so a wrong-server relay bound there can't become a
fleet-wide outage; never let an ambient env var reconfigure an installed
singleton relay. A relay that isn't answering `/health` is not necessarily
down — never force-restart it on that basis alone (see
`docs/agent-notes/not-answering-health-not-running-never-robots-mpr3.md`).

The relay is **not built by `bun install` or `bin/parlay`** — it's gitignored
and must be built explicitly (`tools/relay/build.sh`) before `monitor`/`listen`
can use the non-legacy path; without it, those verbs exit 1 with
`relay is not up and could not be started`, which is why the root README's
Quickstart leans on `--legacy-poll` for a fresh clone.
