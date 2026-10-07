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
  (`POST /register`, `POST /unregister`, `GET /agents`, `GET /health`,
  `GET /audit`, `GET /delivery`), where `runtime-dir` defaults to
  `$TMPDIR/parlay`. `GET /health` answers
  `{"ok":true,"server":…,"runtime":…}`: the `server` field names the upstream
  this relay is bound to, which is what lets an enrolling monitor notice that
  the canonical relay is somebody else's (below).
- **Delivery ledger** — `{runtime-dir}/delivery.log`, one JSON line per
  delivery event (see below).

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

## What the relay delivered, and when

The relay keeps **two durable trails**, and the split is the point:

- `{runtime-dir}/audit.log` — the **control plane**: who enrolled or retired a
  channel and when (`register`, `unregister`, and the denials), one line per
  enroll, with the caller's owner-token fingerprint and never the raw token.
  Shape: `{"ts","actor","action","agent"}`. Read it with `GET /audit?limit=N`.
  It is also the evidence of *which delivery path* a channel had: `parlay
  timeline` reads these lines as claim intervals, so a message on a channel the
  relay never claimed (a `--legacy-poll` agent) is never reported as lost — see
  [`timeline.md`](timeline.md).
- `{runtime-dir}/delivery.log` — the **data plane**: what this relay actually
  spooled, for whom, when, and how each channel's delivery ended. One line per
  message. Read it with `GET /delivery?limit=N&agent=<id>`.

```json
{"ts":"2026-10-07T08:23:12Z","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}
{"ts":"2026-10-07T08:23:19Z","event":"spool-failed","agent":"crew-1","msg":"m-2"}
{"ts":"2026-10-07T08:24:02Z","event":"delivery-ended","agent":"crew-1","reason":"channel-gone","spoolLines":1}
{"ts":"2026-10-07T08:24:02Z","event":"rotated","reason":"size-cap"}
{"ts":"2026-10-07T08:31:00Z","event":"started"}
{"ts":"2026-10-07T08:31:00Z","event":"resumed","agent":"crew-1"}
```

Six events, each a fact only the relay witnesses:

| event | says |
|---|---|
| `spooled` | the message is on disk in that agent's spool — the relay's delivery boundary |
| `spool-failed` | the append failed, so the message did **not** reach the agent |
| `delivery-ended` | the channel stopped being polled, with `reason` (`channel-gone` \| `unregister` \| `shutdown`) and `spoolLines` |
| `rotated` | the active file hit 8 MiB; `delivery.log.1` holds the previous generation |
| `started` | THIS relay process took its control socket and began serving (one line per boot, fleet-wide, written after the bind) |
| `resumed` | this boot brought a poll loop up for that channel from the spool it found on disk |

The last two exist because a restart is the one event that explains a gap in
deliveries, and nothing recorded it: the spool replay that re-registers every
channel was invisible, so a restart looked exactly like a quiet fleet and the
2026-07-17 shape (19 agents left deaf until hand re-enrolled) left no durable
trace. Per channel rather than a count on `started`, so it can be diffed against
the `delivery-ended reason=shutdown` lines to see *which* channel did not come
back.

Four honest limits, stated rather than papered over:

- **`spooled` is not "read".** Nothing in the fleet acknowledges consumption, so
  the ledger records the boundary it can see. It never claims an agent saw
  anything. `resumed` is the same posture in the other direction: it proves the
  relay was polling that channel again from that instant, never that the agent
  was listening or that a queued line was consumed.
- **`spoolLines` is a count, not a diagnosis.** It is the number of messages in
  the spool when the channel stopped, taken *before* the spool is tombstoned. It
  counts what was spooled and unproven-consumed — never what the agent missed,
  which is not knowable here.
- **Rotation is lossy and says so.** A trail that simply begins mid-history is
  indistinguishable from a quiet fleet, so the new file's first line is a
  `rotated` marker.
- **Silence has more than one cause.** A channel with a spool that failed to
  re-register at boot (an error logged to the relay's own stderr) records no
  `resumed` line; so does a ledger written by a relay that predates these two
  events. Absence of a `resumed` line is therefore a question to follow up, not
  a verdict — the trail explains what it saw and says nothing about what it
  could not see.

Nothing free-form is stored: no message body, no path, no error string — the
same posture as `audit.log`. Writes are best-effort exactly like the audit
log's and can never slow or fail a delivery; `PARLAY_RELAY_DELIVERY_LOG=0`
turns recording off, and `GET /delivery` then reports `enabled:false` while
`exists:false` tells a never-written ledger apart from an empty one.

Both trails are local reads on the control socket, they are read-only, and the
chat API's guard is untouched by them. `parlay explain <agent-id>` is the
operator-facing reader of all of the above — relay health, relay enrollment,
this agent's ledger entries and its spool — rendered next to the server-side
facts in [`explain.md`](explain.md). It asks the socket first (the only source
that can say whether recording is switched off right now) and falls back to the
ledger FILE when the relay does not answer, naming the substitution on the line.
`parlay timeline` reads the two trails as
FILES rather than over the socket, which is what lets it answer while the relay
is down; see [`timeline.md`](timeline.md). See `tools/relay/NOTES.md` for the
route table and `tools/relay/relay_delivery.go` for the writer.
