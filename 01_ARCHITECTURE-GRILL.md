# 01 — Architecture grill: Round 1 questions (agent → captain)

**Protocol** (full ledger in `00_ARCHITECTURE-GRILL-META.md`): iterations are
numbered files, `{NN}_ARCHITECTURE-GRILL.md`. Odd NN = agent questions, even NN =
captain answers — answer this file by writing `02_ARCHITECTURE-GRILL.md` (copy the
questions or just reference them as Q1…Q10; inline answers under each is easiest).
Question IDs are global and never reused across rounds. Settled decisions get a
one-line verdict in the meta file's consensus register, `VISION-answers.md`-style.

Questions are ordered by dependency: Q1 is upstream of almost everything else.
Facts below were verified against the tree as of `82bf9f0` (2026-08-26); I only ask
about _decisions_.

---

## Round 1

### Q1. What is the endgame for the Bun server (`packages/server`)?

**Context.** `VISION.md` says two things that are now in tension:

- "Go is the language of parlay's server and CLI" (§Go is the target language)
- "`packages/server` is a publishable relay library" (§Scope)

Meanwhile the actual state: `packages/go-server` implements messaging, registry,
poll, SSE, drafts/uploads/settings, live-commands, **and now serves the panel HTML
itself** (`internal/static/static.go`, `-assets-dir`). The Bun server uniquely owns:
the eval relay (`eval`/`eval-push`), all of TTS, `debug-log`, the PAI tailers
(hook/tool → hub-ingress), the cursorless plugin RPC, pages/`parlay-ui`,
`navigate`/`reload`/`device-cmd`/`clear`/`system`/`declare-channel`, and `version`.
Today they are _co-deployed_: Bun is a producer pushing into the Go hub over HTTP.

**The question.** Pick one:

- (a) go-server is the canonical server; every product route gets ported; Bun server
  is deleted when the port completes.
- (b) go-server is canonical; the Bun server shrinks _permanently_ into a
  PAI-integration sidecar (tailers, TTS, plugins — the Pulse-shaped parts) that is
  explicitly not part of the public product.
- (c) Both stay peers indefinitely; the Bun server is what gets published as the
  "relay library."

**My recommendation: (b).** The tailers read `$PAI_DIR` JSONL and TTS writes into
PAI's memory tree — porting those to Go just moves the PAI coupling into the thing
you're trying to publish. The hub-ingress seam (`POST /api/chat/events` allowlist)
already _is_ the sidecar architecture; name it that. And rewrite the VISION §Scope
line: the publishable server artifact is **go-server**, not `packages/server` —
option (c)'s "publishable relay library" claim contradicts the Go-target principle
and, practically, nobody should `import` a `bun serve()` app with side effects
(AGENTS.md already concluded packages/server isn't publishable).

**Your answer:**

The point here is that PAI was another app, we do not want to participate in PAI any longer, we got what we needed and the concept is turning into the comamnd server, chat relay, event stuff, but in Go. PAI did a good job of showing off what is possible, but we should move that into Go because PAI was too heavy

---

### Q2. Which of the unported routes are _product_, and which are Pulse/PAI residue?

**Context.** Depends on Q1. The routes only Bun serves today, grouped by my read of
their nature:

| Group         | Routes                                                | My read                                                |
| ------------- | ----------------------------------------------------- | ------------------------------------------------------ |
| Panel product | `version`, `clear`, `system`, `declare-channel`       | product — the standalone panel needs them              |
| Panel-aiming  | `navigate`, `reload`, `device-cmd`                    | product — voice-first control of the panel is the demo |
| Voice         | `eval`, `eval-push` (proxy to eval-engine on `:4343`) | product — the voice layer is a headline feature        |
| Debug         | `debug-log`                                           | product, trivial                                       |
| Speech        | `tts*` (5 routes, PAI cache paths)                    | sidecar/residue                                        |
| Plugins       | `plugins`, `plugin/cursorless/rpc\|response`          | sidecar/residue (Talon-only caller)                    |
| Pages         | `pages`, `parlay-ui`                                  | Pulse residue                                          |

**The question.** Confirm or redraw the line. Specifically: (i) is the eval relay a
go-server port target (it's just a guarded HTTP proxy — cheap in Go)? (ii) does TTS
have a future in the public product (it's a big chunk of the voice-first story —
"agent replies read back aloud" is in the README's pitch) or is speech synthesis the
host's problem?

**My recommendation.** Port groups 1–4 to go-server (all small, all in
`api-contract.md`). TTS is the hard call: I'd say **TTS playback stays product, TTS
_synthesis/cache_ becomes pluggable** — go-server gets a `tts` route that proxies to
a configurable synth backend, and the PAI pronunciation-report/cache logic stays in
the sidecar. Plugins and pages stay Bun-side and die or live with Pulse.

**Your answer:**

Plugins - VITAL, must have an ability to have plugins, they will expand the capabilities of the commands that can be run and executed.

Pages - also vital, but as a portion of the app that is sidecar, not part of the stack that is in git, but thing bucket gets generated on install and build, and adaptions that improve the functionality of this sector are part of of Parlay

TTS - Yes, may drop

Vocie - voice is vital, but specifically it is about evaluating strings, letting some other tool handle voice-to-text

panel-aiming - vital, but in a command and response format, this isnt necessarily inbuilt into parlay, but we should be able to use the command server to drive the UI of some project that is a subscriber as a core part of parlay

panel product - the panel that gives a view into what the system is doing, this is vital as well.

If you read these and have more questions, please update.

---

### Q3. Does `tools/relay` survive, or does its job fold into go-server + Go CLI?

**Context.** VISION's "the relay is the only product" uses _relay_ to mean the whole
channel product; `tools/relay` is a specific 710-line daemon: one long-poll
goroutine per agent, fan-out via `$TMPDIR/parlay/<agent>.chan` spool files consumed
by `tail -F`, control over a Unix socket. That transport has generated the single
largest bug class in the repo's history — orphaned readers (robots-3pvi, 168 live /
142 orphaned), registered-but-deaf (robots-dcag, robots-gv6t), duplicate listeners
(robots-fgyz, 12 loops on one channel), spool replay/cursor semantics (robots-jkwc),
singleton-per-runtime-dir server binding (robots-buu8/93xu), and the 104-byte
`sun_path` cap. Each is fixed, but the fix inventory _is_ the evidence about the
architecture.

**The question.** Long-term transport for agent-side delivery — pick one:

- (a) Keep the relay daemon as-is; it's now battle-hardened.
- (b) Fold it into go-server: server keeps per-agent durable cursors, `parlay
listen` becomes a direct long-poll/SSE client with resume-from-cursor, no spool
  files, no tail, no Unix socket, no launchd relay job.
- (c) Keep a relay process but replace spool+tail with something structured.

**My recommendation: (b).** The relay exists because N bun pollers cost ~40MB each;
a single Go CLI long-polling directly costs nothing like that, and go-server already
holds `/api/chat/poll` open natively. The spool file's one real virtue — messages
survive while no listener is attached — moves server-side: history is already
durable, so `listen` resuming from a per-agent acked cursor gives the same guarantee
without a second durability layer in `$TMPDIR`. This deletes the entire
orphan/reaper/singleton/cursor machinery rather than maintaining it. Big migration,
so: freeze relay feature work now, build cursor-resume into go-server, cut over
per-agent. (Sub-decision if you pick (b): does the server store per-agent read
cursors, or does the client persist its own `after=` id? I'd put it client-side in
`$PARLAY_STATE_HOME` — server stays stateless about readers.)

**Your answer:**

B is a good choice

---

### Q4. Storage: what backs "queryable history, cross-channel search, durable audit log"?

**Context.** Today: `messages.jsonl` ring buffer with byte-cap compaction;
presence + live-commands **in-memory by design**; no search surface at all
(`GET /history?limit&channel` is everything). All five Go modules are deliberately
pure-stdlib (no `go.sum` — AGENTS.md calls this out as making CI fast). VISION
promises cross-channel search and a durable audit log.

**The question.** Two decisions:

1. Is **pure-stdlib** a hard constraint for go-server, or a nice-to-have that a real
   feature may break? (SQLite means cgo or a big pure-Go dep like `modernc.org`.)
2. Given that: search backend = (a) brute-scan the JSONL per query, (b) SQLite,
   (c) beads/dolt, (d) something else?

**My recommendation.** Keep pure-stdlib and pick **(a) brute-scan**, but stop
compacting away what you want to search: split _live ring_ (what the panel loads)
from an _archive_ — `messages.jsonl` compaction appends the evicted lines to
`archive/<yyyy-mm>.jsonl` instead of dropping them, and `GET /api/chat/search?q=&channel=&since=`
scans archive+ring. A one-captain fleet produces megabytes, not gigabytes; a linear
scan is milliseconds for years of history, and you can graduate to SQLite behind the
same route if it ever measures slow. The audit log (see Q7) is append-only JSONL by
the same logic.

**Your answer:**

I am okay with keeping this part light and configurable with a sensible smart default choice, and then another one can be swapped in at a later point.

---

### Q5. Beads-backed crew status: what does "backed by parlay's own beads store" mean mechanically?

**Context.** VISION (via H-A research): `bd` is a standalone binary, a store is just
`BEADS_DIR`, no PAI needed — status store at `$PARLAY_STATE_HOME/agents.beads`.
Today, nothing in the relay/server touches beads; status is `parlay status` lines in
per-agent files plus in-memory presence, and the beads coupling that _does_ exist
(spawn `--bead` required-mode, mechanic-dispatch, robots-emit) goes through the
private fleet's wrappers. `api-contract.md` has a proposed-not-built
`POST /api/events/bead-status`.

**The question.** Pick the integration shape:

- (a) go-server shells out to `bd` (soft dependency: degrade gracefully when absent).
- (b) Status stays native (files/registry as today); a bridge process mirrors it into
  a beads store — beads is an _export target_, not the source of truth.
- (c) Beads is the source of truth: crew-state/status verbs read and write
  `$PARLAY_STATE_HOME/agents.beads` directly.

**My recommendation: (b).** A public project whose _server_ won't start honestly
without a private-ish binary on PATH is the coupling VISION spends three sections
removing; and (c) makes every status read shell out. Native status stays the
contract; a `parlay beads-sync` (or the bead-status ingress endpoint) mirrors
transitions into the beads store for the fleet that wants it. If `bd` ever gets a
public release with a Go library API, revisit (c).

**Your answer:**

bd is still the required backend of this thing, we must use beads, it is a superior tool.

---

### Q6. Auth: still strictly network-delegated, now that the server serves the panel, a fleet dashboard, and (soon) outbound webhooks?

**Context.** H-2 settled auth as environment-delegated, JWT a stretch goal. Since
then: go-server serves the panel at `/` and the fleet dashboard at `/fleet/`
(read-only but shows every agent, channel, command), and VISION added outbound
webhooks carrying **full message bodies**. The origin guard stops cross-origin
browsers; it does nothing about any process/person on the LAN/tailnet curling the
port — which today reads all history and posts into any agent's turn.

**The question.** Is an **opt-in static bearer token** (one shared secret, checked
in the guard alongside origin, off by default so CLI/curl ergonomics are unchanged)
in vision — or does auth stay 100% the network's job until/unless JWT ever happens?

**My recommendation.** Add the opt-in token. It's ~30 lines in `internal/guard`, a
`PARLAY_TOKEN` env on CLI/relay/panel (query param or header for `EventSource`),
zero cost when unset, and it converts "anything that can reach the port owns the
fleet" into "anything that can reach the port _and_ holds the token." A tailnet is a
good moat, but every phone/laptop on it is currently a captain. This is not JWT and
adds no user roles, so it doesn't violate the captain-and-crew-only principle.

**Your answer:**

Sure, but I explcitily want an option to use tailscale as the connetion layer to make this screamingly easy

---

### Q7. The audit log's fidelity contradicts the live-commands redaction policy — which wins, and where does the log live?

**Context.** VISION: audit log "includes verb, agent, **flag values, positionals**,
exit code, and timing." The live-commands registry stores _none_ of that by explicit
policy (`internal/store/commands.go`: flag names only, no argv/values — because the
report endpoints are unauthenticated and a parlay command line routinely carries
message bodies and tokens). Both can't govern the same channel.

**The question.** Where is the audit log written?

- (a) Client-side: each CLI invocation appends locally to
  `$PARLAY_STATE_HOME/audit.jsonl` — full fidelity, never crosses the wire, wire
  redaction policy untouched.
- (b) Server-side: a new authenticated/guarded ingest route accepts full argv and
  persists it centrally (implies Q6's token, minimum).
- (c) Hybrid: client-side full log; a redacted durable summary ships to the server.

**My recommendation: (a)**, upgrading to (c) only if a real cross-machine need
appears. The audit question ("what did commands do on this box") is answered where
commands run; `commandreport.Begin` already wraps every dispatch, so the append is
one more sink. Central aggregation without auth would mean either weakening
redaction or building auth _for this feature_ — wrong order.

**Your answer:**

B, becasue the client apps will not have an easy UI for this likely, and should not.

---

### Q8. Outbound webhooks: delivery contract?

**Context.** Unbuilt; VISION commits to "publishes outbound webhooks carrying full
message bodies when messages arrive, so the captain can receive notifications
without polling." The hub-ingress code already established the house pattern for
HTTP-push-that-must-never-wedge (per-route chaining, 5s abort, bounded queue,
stall-based shed, rate-limited failure logs).

**The question.** Three sub-decisions:

1. Config surface: static (`settings.json` / env: URL + event filter) vs a CRUD
   subscriptions API?
2. Guarantee: fire-and-forget with bounded retry, or at-least-once with a durable
   outbox?
3. Event set: captain-bound messages only, or also agent status transitions
   (`done`/`failed`/`needs-decision` — arguably the notifications you actually want
   on your phone)?

**My recommendation.** Static config (a CRUD API is a new unauthenticated write
surface — see Q6); fire-and-forget with the hub-ingress queue pattern (a missed
notification is recoverable — the panel has the truth; a durable outbox is a second
message store to keep consistent); and **include status transitions** — "agent went
`needs-decision`" is worth more on a phone lock-screen than most message bodies.
Full bodies stay in per H-C, with the note that this makes Q6's token matter more
(the webhook receiver URL is now a place chat content flows to).

**Your answer:**

your rec is good

---

### Q9. Two front ends: is `packages/client` (chat panel) + `packages/webview` (fleet dashboard) the permanent shape?

**Context.** `client` is the vanilla-TS chat panel, now servable by go-server at `/`
(PR #101 line of work) — which quietly closes the README's stated "main gap" (no
turnkey panel host without Pulse). `webview` is a new React/Vite read-only fleet
dashboard at `/fleet/` (tabs: thread/eval/commands/events). They overlap: both
render the thread and consume the same SSE.

**The question.** (i) Confirm the split: client = the _write_ surface (chat,
voice, drafts), webview = the _read-only_ fleet surface — two apps, two stacks, no
convergence. (ii) Is go-server-serves-the-panel now the blessed public deployment
(README rewritten accordingly, Pulse demoted to "the author's host"), and is the
missing deploy step (nothing builds/copies `webview/dist` or `client/dist` into
`-assets-dir` — the mount exists, the pipeline doesn't) a C6-deploy-script job?

**My recommendation.** Yes to both. Keep the stacks separate — a read-only React
dashboard and a voice-first vanilla panel have different change velocities and merge
pressure would couple them for no user benefit. Add the build+copy to
`packages/go-server/deploy/install.sh` (it's already the artifact-owning script),
and rewrite the README Quickstart to the one-binary story: `parlay-server
-assets-dir` serving panel + fleet + API on `:4242`.

**Your answer:**

sure, will revisit this later, but for now lets take the rec

---

### Q10. Where is the public/private line in the spawn layer?

**Context.** `bin/parlay-spawn` (bash, large), launcher backends herdr _and_
gascity, treehouse pool guards, beads-required mode, PII-aware model routing,
mechanic-dispatch — this is the fastest-moving surface in the repo (most commits in
the last 10 days) and the most entangled with the private fleet (herdr is public;
treehouse/beads stores/mechanic zones are not). VISION §Scope: "not an orchestration
engine," yet README sells "Spawn + supervise" as a headline feature.

**The question.** Pick the boundary:

- (a) Spawn is product: define a documented launcher interface (herdr/gascity as the
  two in-repo backends), keep beads-required as opt-in config, and hold
  `parlay-spawn` to the same public bar as the server.
- (b) Spawn is fleet-private tooling that happens to live here: mark it internal
  (docs/README's second table), never promise it works off the author's box, and
  keep the public product line at _enroll + channel + supervise a
  process someone else started_.
- (c) Split: a minimal public `parlay spawn` (register + launch a command with
  identity env, nothing else) with the fleet's full bash layer as the private
  superset.

**My recommendation: (c).** The README already promises spawn, so (b) breaks the
pitch; but (a) commits you to publicly supporting treehouse/beads semantics that
VISION explicitly walks away from. A Go `parlay spawn` doing register→launch→enroll
against the documented launcher seam (the gascity path is already the
dependency-free shape of this) is small and honest; `bin/parlay-spawn` stays the
power tool, documented as internal.

**Your answer:**

parlay-spawn is deprecated, `parlay spawn` in integral, core part of the product, gascity as the entry point for agent launching.

---

_End of Round 1. Write your answers as `02_ARCHITECTURE-GRILL.md`; mark anything
you want to defer `PUNT`. My next file (`03_…`) will chase follow-ups and the
held-back questions (Q3 cursor semantics, Q4 archive retention, TUI stack, TS-CLI/
parity-harness EOL — listed in `00*ARCHITECTURE-GRILL-META.md`), which hinge on
these.*

Other things to consider. We are specifically trying to find scenarios where gascity can be leveraged tom take over hard parts of this tool. What does gascity do well, where we have to maintain less while gascity trucks along.
