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
  `runtime-dir` defaults to `$TMPDIR/parlay`. `GET /health` answers
  `{"ok":true,"server":…,"runtime":…}`: the `server` field names the upstream
  this relay is bound to, which is what lets an enrolling monitor notice that
  the canonical relay is somebody else's (below).

## The relay binds ONE server, and every instance on the host shares it

`-server` is read once at startup and never changes. Two parlay instances on
one machine — `parlay-dev`, a `-state-dir` server, a `parlay remote set`
target — therefore share the single canonical relay, and only the server *that
relay* polls ever reaches an enrolled monitor. Before this was checked, a
second instance's `parlay listen --agent X` enrolled successfully, appeared as a
live tab, and then streamed a spool the relay never writes to: registered,
present, and permanently deaf, with no error on any stream.

The pre-enrollment preflight (`tools/monitor/parlay-monitor.sh`, run by
`listen`, `monitor` and `claim` before anything is registered) now reads the
`server` field off `/health` and compares it with the server the CLI resolved
(`PARLAY_SERVER`, or the persisted remote). On a mismatch it exits 1 naming
both URLs and offering three ways out — `--legacy-poll`; a private relay
(`PARLAY_RELAY_RUNTIME=<dir>` plus one started with `-server $PARLAY_SERVER`);
or point `PARLAY_SERVER` at the server the existing relay already polls. The
comparison normalizes `localhost`≡`127.0.0.1` and drops the scheme's default
port, so two spellings of one server are not a mismatch, and a relay that does
not report `server` at all (any build predating the field) is **not** treated
as a mismatch — an unanswerable question must not become a refusal.

`GET /health`'s shape stays additive on purpose: `ensure-up` decides liveness
with `curl … | grep -q '"ok":true'` (`tools/relay/deploy/lib.sh`), so `ok` must
keep being the first key emitted. `TestHealthKeepsOkFirstForTheEnsureUpGrep`
holds that.

Per the root [`AGENTS.md`](../AGENTS.md) (tracked as the symlink `CLAUDE.md`):
the relay is a **per-runtime-dir singleton bound to one server** — `PARLAY_SERVER`
alone does not scope which relay a given runtime dir's socket belongs to, and
Unix socket paths cap at 104 bytes, which constrains how deep a runtime dir
can nest. The canonical runtime dir is reserved specifically so a wrong-server
relay bound there can't become a fleet-wide outage; never let an ambient env var
reconfigure an installed singleton relay. A relay that isn't answering `/health`
is not necessarily down — never force-restart it on that basis alone (see
`docs/agent-notes/not-answering-health-not-running-never-robots-mpr3.md`).

The relay is **not built by `bun install` or `bin/parlay`** — it's gitignored
and must be built explicitly (`tools/relay/build.sh`) before `monitor`/`listen`
can use the non-legacy path; without it, those verbs exit 1 with
`relay is not up and could not be started`, which is why the root README's
Quickstart leans on `--legacy-poll` for a fresh clone. That exit is a clean
refusal rather than a trap: `monitor`/`listen`/`claim` each preflight the relay
before enrolling, so nothing is registered when it fires — see
[`monitor.md`](monitor.md).
