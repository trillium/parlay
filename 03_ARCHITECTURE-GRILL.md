# 03 — Architecture grill: Round 2 (agent → captain)

**How to answer:** write `04_ARCHITECTURE-GRILL.md`. Bare answers are fine
(`Q3a: client-side, because…`). `PUNT` defers a question.

**What this round is.** Round 1 (files 01/02) settled the big shape: the Go server
replaces the Bun server entirely (Q1), the relay daemon folds into it (Q3), history
gets a searchable JSONL archive (Q4), beads is the health/activity layer (Q5), the
spawn layer splits into a small public verb and the private power tool (Q10). This
round does two things: (1) follow-ups — each Round 1 decision opened one or two
smaller decisions that need answers before anyone can build; (2) the four questions
I held back in Round 1 because they depended on those verdicts. Two Round 1 answers
(Q6 auth, Q7 audit) were brief enough that the recorded verdict includes detail you
never actually confirmed — Q6a and Q7a restate those from zero so you can confirm
or override with full information.

Every question below is written to be answerable from this file alone — no code
reading, no internet. Where I explain a mechanism, I verified it in the tree first.

---

## Follow-ups to Round 1 decisions

### Q3a. When an agent reconnects to its channel, who remembers where it left off — the server or the agent?

**Where this stands.** In Q3 you decided to kill the relay daemon. Today's delivery
chain is: server → relay daemon (one process on the box) → a text "spool" file per
agent in `$TMPDIR/parlay/` → a `tail -F` process per agent reading that file. The
replacement: each agent's `parlay listen` talks HTTP directly to the Go server and
the whole spool/tail layer disappears.

**Background you need.** Messages already survive fine — they're in the server's
history file. The only real job the spool file did was _bookmarking_: when an
agent's listener dies (crash, laptop sleep, context reset) and later reconnects,
something must know which messages it already saw, so it neither misses the ones
sent during the gap nor gets them delivered twice. That bookmark is called a
**cursor** — literally just the ID of the last message this listener delivered.
Every message in history has an ID; "resume from cursor" means the reconnecting
listener asks the server "give me everything on my channel after ID X."

The lesson already learned the hard way (and worth keeping): when a listener has
been gone a long time, replaying the _entire_ backlog into an agent floods its
context window and destroys the session — the current code caps replay at 50
newest lines and _says out loud_ that it skipped the rest. Whatever we pick keeps
that cap.

**The question.** Where does the cursor live?

- **(a) Client-side.** Each `parlay listen` process writes its own last-seen ID to
  a small file under `~/.parlay/` after each delivered message, and sends it back
  as `?after=X` on reconnect. The server stays completely ignorant of readers.
  _Consequences:_ server code stays simple and stateless (nothing new to persist,
  nothing to garbage-collect when an agent is deleted); if an agent's state dir is
  wiped, its cursor is gone and it starts from "now" (which is the safe default —
  new listeners already deliberately start at end-of-history, not at the
  beginning); two listeners on one channel (a bug we've had) can't fight over one
  server-side bookmark.
- **(b) Server-side.** The server keeps a per-agent "delivered up to X" record and
  each reconnect just says "give me what I haven't seen."
  _Consequences:_ the client is dumber (nice for third-party clients using
  `parlay-input`); but the server now owns reader state — it must persist it,
  clean it up when agents are unregistered, and decide what "the" cursor means
  when two listeners share a channel (there is no right answer to that one).

**My recommendation: (a) client-side.** The per-reader nature of the bookmark is
the deciding fact — a cursor fundamentally belongs to a _reader_, not a channel,
and the server can't know how many readers exist. This also matches how the
current spool cursor works (a file next to the spool), so the migration is a
straight port of known-good semantics.

**Your answer:**

Client can give a var to say where they are in the file, otherwise the server will handle the level of message the client shoudl receive.

---

### Q4a. Does the message archive grow forever, or get pruned — and does the old Bun server's rotation history migrate into it?

**Where this stands.** In Q4 you decided: the live history file stays a bounded
ring (what the panel loads on connect), and instead of _deleting_ old messages at
compaction time, they get appended to monthly archive files
(`archive/2026-08.jsonl` etc.), which the new `GET /api/chat/search` route scans.

**Background you need — real numbers from your own box.** Your live fleet
currently produces about **5MB of chat per 4–8 days** (`~/exchange/` holds five
rotated 5MB history files spanning July 22 → Aug 14, plus the 2.8MB live file).
Call it 20–40MB/month, so roughly **¼–½GB per year**. Scanning half a gigabyte of
text line-by-line takes well under a second on your hardware — that's why Q4's
brute-scan verdict is safe for years. Disk cost is irrelevant at this scale; the
only real costs of keeping everything are (1) search slowly getting slower and
(2) an ever-growing pile of chat content sitting on disk in plaintext, which is a
privacy surface if the box is ever shared or backed up somewhere careless.

Also relevant: the Bun server _already_ rotates history into dated files (those
`chat-history.2026-*.jsonl` files). When the Go server takes over (Q1), those
existing files are the first year of your archive — if we migrate them.

**The question.** Three small decisions in one:

1. **Retention:** (a) unbounded — keep everything forever; (b) a configurable
   cap (e.g. "keep N months," default unlimited); (c) prune only by explicit
   command (`parlay archive prune --before 2026-01`), never automatically.
2. **Migration:** when the Go server takes over, do the existing rotated Bun
   files get folded into the new archive so search covers them? (yes/no)
3. **Does search cover the archive by default,** or only the live ring unless you
   pass `--all`? (Default-everything is more useful; default-live is faster and
   avoids surprising hits from months ago.)

**My recommendation.** 1(c) — unbounded by default, prune as an explicit command
only. Automatic deletion of chat history is exactly the class of destructive
surprise this repo's safety principles exist to prevent, and a manual prune verb
can go through trash like every other deletion here. 2: yes, migrate — it's a
one-time file copy+rename and it makes search actually cover "what did that agent
do in July." 3: search everything by default; the result set is capped anyway.

**Your answer:**

unbounded by default, some sort of doctor comamnd that keeps a running knowledge of how much info is there, file size, and since when so the user can dump it if they want to. This might actually become huge if we run many many agents and they depend on parlay.

---

### Q5a. Beads as the health layer: who writes the beads, who reads them, and does the _public_ parlay depend on beads at all?

**Where this stands.** Your Q5 answer: beads is the layer that tells the system
about activity and health — spawn claims a bead, struggle files a robots bead,
finish closes the bead; the system reads bead states to know what's going on,
plus aliveness checks. And in Q10 you decided the spawn layer splits: a minimal
public `parlay spawn`, with `bin/parlay-spawn` (treehouse, beads-required,
mechanic dispatch) staying the private power tool.

**Background you need.** "Beads" concretely: `bd` is a standalone binary; a beads
_store_ is just a directory it manages, selected by an environment variable — no
PAI needed (this was the H-A research finding in `VISION-answers.md`). Today
**nothing in the server or relay touches beads at all**. The beads coupling lives
entirely in the private spawn/dispatch tooling, and it talks to _your_ stores
(`robots`, `task`, …) through _your_ wrappers. VISION commits to "parlay's own
beads store at a parlay-controlled path" — a store parlay itself owns, e.g.
`~/.parlay/agents.beads` — which does not exist yet. The tension Q5 didn't
resolve: your answer describes the _fleet's_ lifecycle (claimed → robots →
closed), which runs through private stores, while VISION describes a store the
public product owns.

**The question.** Which of these is the design?

- **(a) Public parlay owns a beads store.** `parlay spawn` / `status` / teardown
  write lifecycle beads into `~/.parlay/agents.beads`; the server (or CLI) reads
  it to answer "what's alive, what's stuck." `bd` becomes a _soft_ dependency of
  public parlay: present → rich lifecycle; absent → parlay still works, just
  without the bead layer. Your private fleet's robots/task stores remain separate
  and private.
- **(b) Beads stays entirely on the private side of the Q10 split.** The public
  product's health surface is what exists today (registry + status lines +
  presence + aliveness checks); the claimed/robots/closed lifecycle is the
  private power tool's business. The VISION line about "parlay's own beads
  store" gets softened or dropped.
- **(c) Beads is the source of truth even for public parlay** — status reads and
  writes go through `bd` directly; no beads binary, no working status. (Listed
  for completeness; it makes a hard dependency out of a binary that has no public
  release story yet.)

**My recommendation: (a).** It honors both of your answers at once: the public
product gets a real, parlay-owned bead lifecycle (VISION's promise), your fleet's
private stores stay private (Q10's split), and a fresh-clone user without `bd`
loses nothing they ever had. The one rule that keeps (a) honest: the server never
_requires_ a bead read to answer a request — beads enrich, registry + status
remain the fallback truth.

**Your answer:**

Yes beads is the necessary layer of how this will work, it will make creation of tasks easier on behalf of the client. Beads makes it easier for tooling to know what the state of a task is, and should be depended upon to get that info.

---

### Q6a. Auth, restated from zero: what actually protects the port today, what a token would add, and whether you want one.

**Where this stands.** Round 1's Q6 asked whether to add an optional access token.
Your answer — "Tailscale is the security layer at the moment, whatever that means
here" — reads as _unsure what was being asked_, and the recorded verdict
("Tailscale + optional token on `PARLAY_TOKEN`") contains detail you never
actually said. So here it is from zero, no jargon assumed. This decides whether a
small auth feature gets built at all.

**Background you need.** The parlay server has **no login of any kind, on
purpose** — that's what lets the CLI, hooks, and plain `curl` talk to it with
zero setup. Two separate mechanisms currently stand between the internet and your
fleet:

1. **Reachability (Tailscale).** The server listens on your machine; only devices
   that can reach that machine's port can talk to it at all. Your tailnet is a
   private network: your phone, your laptop, any device you've enrolled. Nothing
   on the public internet can even connect. This is what "Tailscale is the
   security layer" means, and it is genuinely doing that job.
2. **The origin guard (already built, both servers).** This stops a _malicious
   web page_ open in a browser on one of your own devices from silently using
   that device's network access to post into your fleet. Browsers attach an
   "Origin" header saying which website sent a request; the guard refuses
   mutating requests from origins it doesn't recognize. It only constrains
   browsers — command-line tools don't send an Origin and pass freely (by
   design).

**The gap neither one covers:** any _person or program_ on an enrolled device —
a houseguest on your wifi if you ever share the LAN path, another user account on
a shared machine, any process running on any tailnet device — can read your
entire chat history and send messages _as you_ to any agent, with `curl`. Today
"can reach the port" equals "is the captain."

**What the proposed token is.** One shared secret string. You'd set it once in the
server's environment (`PARLAY_TOKEN=whatever`) and once wherever the CLI/panel
run; every request carries it in a header; the guard rejects requests without it.
**Off by default** — if you never set it, nothing changes, `curl` stays
frictionless. It is _not_ logins, users, or sessions; compromising the one token
still means game over (rotation = pick a new string, update both ends). Cost:
~30 lines in the guard, one env var plumbed through CLI/panel/webview, a query
parameter fallback for the browser's live-event stream (browser SSE connections
can't set custom headers).

**The question.** (i) Build the opt-in token, yes or no? (ii) If yes: one shared
secret for everything, or per-caller tokens (one for the phone, one per agent
box…)? Per-caller buys the ability to revoke one device without re-keying
everything, at the price of real key-management busywork for a one-person fleet.

**My recommendation.** (i) Yes — it's the cheapest possible second layer, and the
threat it closes ("every tailnet device is a captain") is real even if currently
acceptable. (ii) One shared secret; per-caller tokens are multi-user
infrastructure and VISION explicitly refuses user roles beyond captain and crew.

**Your answer:**

Sure, same asnwer as other doc

---

### Q7a. Confirming the audit log design: one local file per machine, full detail, nothing new sent over the network.

**Where this stands.** VISION promises a durable audit log of every parlay command
— verb, agent, **flag values, positionals** (i.e. full detail, message bodies
included), exit code, timing. But the server's existing live-commands registry
deliberately refuses to accept flag _values_ or message text over the wire,
because the reporting endpoints have no auth (see Q6a) — anything on the network
could read or spoof them, and command lines routinely contain message bodies and
secrets. Round 1's Q7 asked which principle wins; your whole answer was
_"Fidelity presently wins."_ That settles the priority but not the mechanism, and
the recorded verdict filled in a mechanism for you — so, confirming it explicitly.

**The design being confirmed (option a from Round 1).** Every `parlay <verb>`
invocation appends one line — full argv, exit code, duration, timestamp, agent id
— to a local file, `~/.parlay/audit.jsonl`, on the machine where the command ran.
The plumbing already exists: every Go CLI command is already wrapped by the
command-reporting layer, so this is one extra write in a place that already sees
start and end of every verb. Nothing new crosses the network; the server's
redaction rule stays exactly as is; the wire registry remains the "what's running
right now" view while the local file is the "what happened, exactly" record.
_The one limitation:_ with several machines, each keeps its own log — there's no
merged fleet-wide audit view unless something collects the files later. (The
alternative was server-side collection, which would require Q6a's token first
and would put message bodies in yet another central plaintext file.)

**The question.** Confirm local-per-machine full-fidelity audit (a), or override:
(b) central server ingest (requires token auth), (c) local full + redacted copy
shipped centrally.

**My recommendation.** Confirm (a). Revisit (c) only if a real "grep the whole
fleet's history from one place" need shows up — and note Q4's search plus Q8's
webhooks already cover most of what a central log would be used for.

**Your answer:**

refer to other doc for this decision

---

### Q9a. The deploy script: should installing the server on a box require Bun/Node, or only Go?

**Where this stands.** Q9 settled that the Go server serves both browser apps —
the chat panel at `/` and the fleet dashboard at `/fleet/` — and that the missing
piece is deploy plumbing: nothing today builds the two frontends and puts their
output where the server's `-assets-dir` flag points. The fix lands in the
existing installer (`packages/go-server/deploy/install.sh`, the script that
builds the server binary and sets it up as an always-on macOS service).

**Background you need.** The two frontends are TypeScript/React; turning their
source into the static `dist/` bundles a browser can load requires **Bun** (the
JS toolchain) on the building machine. The Go server itself needs only Go. So
there's a real fork in what "install" means:

- **(a) install.sh builds everything.** Run it on any checkout and it does bun
  builds for both frontends, then copies the results into the assets dir.
  _Consequence:_ deploying the server now requires Bun installed on the target
  box, and the installer inherits the frontends' build quirks — notably the chat
  panel's build script fires a "reload" ping at your live production server on
  every build (a known footgun documented in the repo), which an _installer_
  triggering is extra surprising.
- **(b) install.sh only copies pre-built `dist/` dirs** and fails with a clear
  message ("run `bun run build` in packages/client and packages/webview first")
  when they're missing. _Consequence:_ deploy target needs only Go; building
  frontends stays a developer action (or a CI artifact) with its footguns in
  developer context; but "fresh box to running panel" is now two documented steps
  instead of one.
- **(c) (b) plus a separate opt-in `--build` flag** that does the bun builds when
  asked.

**My recommendation: (c).** Copy-only by default keeps the deploy dependency
surface at "Go, period" and keeps the reload-ping footgun out of the install
path; the `--build` flag preserves the one-command path for the common case
(your own box, Bun present). Either way the panel build must learn to skip its
reload ping when invoked from the installer.

**Your answer:**

prod go only, dev accepts bun, tinkerers will bring in dev tooling

---

## Held-back questions, now unlocked

### Q11. The TUI: VISION promises one — when, and built on what?

**Where this stands / correcting the record.** File 03's earlier draft called the
TUI "a potential future interface" — that understates your own VISION, which
commits to it flatly: _"The relay exposes fleet-visibility surfaces: a TUI and a
standalone web app… These surfaces live in this repository and work without Pulse
or PAI."_ The web half now exists (`packages/webview`). The terminal half doesn't
— zero TUI code in the repo.

**Background you need.** A TUI (terminal UI) is a full-screen interactive
terminal app — `htop`/`lazygit`-style: agent list down the side, thread view,
live updates, keyboard navigation. Everything it needs to _show_ already exists
as API: the agents list, history, presence, and the live event stream the web
dashboard already consumes. The real decision is dependency policy: every Go
module in this repo is deliberately **pure standard library** (no third-party
packages at all — it keeps builds instant and CI simple). The standard way to
build a Go TUI is the Bubble Tea framework — excellent, but a third-party
dependency tree. Hand-rolling full-screen terminal handling in pure stdlib is
weeks of fiddly work that reimplements a solved problem badly. A middle path
exists because each Go module here has its _own_ dependency file: a new
`tools/tui` module could take the Bubble Tea dependency without touching the
purity of the server/CLI modules.

**The question.** (i) Priority: is the TUI wanted _soon_ (next few work cycles),
or after the Round-1 port/fold work lands? (ii) Dependency ruling: may a
dedicated TUI module use Bubble Tea, or is pure-stdlib a repo-wide law (which
realistically shelves the TUI)? (iii) Minimum viable scope — my proposal: agent
list with live status + tail of the selected channel + send-to-channel. Read
_and_ one write (send), or read-only like the web dashboard?

**My recommendation.** (i) After the port — the TUI reads APIs the port is about
to stabilize. (ii) Allow Bubble Tea in an isolated `tools/tui` module; the
pure-stdlib rule's value is in the server/CLI, and "no TUI" is a worse outcome
than "one leaf module with deps." (iii) Include send — a fleet surface you can't
_answer_ from sends you back to your phone mid-glance; but adopt the same guard
rules as every other writer.

**Your answer:**

take rec

---

### Q12. Retiring the TypeScript CLI: what has to be true before `packages/cli` is deleted, and what dies with it?

**Where this stands.** `bin/parlay` — the entry point everything uses — already
runs the Go CLI for every verb **except one**: `lavish-import`, which still
routes to the TS CLI because its Go port was never written. So the TS CLI is
retired in practice but not in fact: it's still in the tree, still has tests in
CI, and still gets one live call.

**Background you need.** Three things are coupled to its existence:

1. **The parity harness** (`tools/cli/parity/run.sh`) — a script that runs the
   same commands through both CLIs and diffs the output. It exists to prove the
   Go port faithful _while both exist_; with the TS side gone it has nothing to
   diff and dies too. It has a standing, documented 4-case failure (help-text
   diffs from Go-only verbs) — the harness's own docs say "pass=39 fail=4 is the
   standing state, not green."
2. **CI time/scope** — the bun job tests `packages/cli` on every PR.
3. **VISION's own line** — "parity is established and maintained by a diff
   harness." Deleting the TS CLI means rewriting that sentence: parity stops
   being a maintained property and becomes a historical fact.

**The question.** (i) Gate: port `lavish-import` to Go first, or decide it's
fleet-private (it imports data for the author's page host) and move it out of the
public CLI's scope entirely? (ii) Once ungated: delete `packages/cli` + the
parity harness in one PR (history stays in git), or keep the harness script as
reference? (iii) Timing: EOL immediately after the gate clears, or after the Go
CLI ships one tagged release with no faithfulness bugs?

**My recommendation.** (i) Port it — it's one file, and "the public CLI has one
verb that silently needs Bun" is exactly the kind of hidden dependency this whole
grill is deleting. (ii) Delete both in one PR; a diff harness without a second
side is dead weight, and git history is the reference. (iii) Immediately — the Go
CLI has been the only path anyone actually runs for weeks (every fleet agent goes
through `bin/parlay`); that _is_ the release test. Also rewrite the VISION parity
sentence in the same PR so the vision doesn't promise a harness that no longer
exists.

**Your answer:**

sure, take rec

---

### Q13. The known identifier leak on the old server: fix it, or let it die with the Bun server — and that depends on how long the Bun server has left.

**Where this stands / correcting the record.** File 03's earlier draft described
this wrong (it claimed both servers leak; they don't). The accurate picture:

**Background you need.** There is one _known, accepted_ information leak, and it
exists **only on the Bun server** — the one Q1 just sentenced to deletion. What
it leaks: any web page open in a browser on any device that can reach the port
(see Q6a for who that is) can quietly read (1) the list of every registered agent
ID, and (2) a device identifier that the panel's speech system attaches to its
events. It cannot _send_ anything with these — every route that could aim at an
agent or the panel is guarded — but identifiers make any future hole more
aimable, which is why the leak was documented and tracked rather than shrugged
off when it was found. The Go server already doesn't have this problem: it sends
browsers no cross-origin read permissions at all on those routes, deliberately
stricter than the old server. So the leak's lifespan equals the Bun server's
remaining lifespan — which Round 1 didn't put a number on.

**The question.** Two parts, really one: (i) Roughly when do you want the Q1
replacement _done_ — is the Bun server's remaining life weeks, or quarters? (ii)
Given that horizon: patch the leak in the Bun server now (a small, targeted
change: stop reflecting the wildcard permission on those two reads, mirroring
what the Go side already does), or accept it until retirement? A patch is wasted
motion if retirement is close; "accepted residue" quietly becomes "permanent
residue" if retirement drifts — which is the honest risk, since the port list in
Q2 is real work.

**My recommendation.** Set the horizon explicitly (my suggestion: Bun server off
within two release cycles of the Q2 port list landing), and _if_ you commit to
that, accept the leak until then — it's read-only, tailnet-only, and every
aimable route is guarded. If you don't want to commit to a horizon, patch it now
instead; unbounded-lifetime known leaks are how "accepted" rots.

**Your answer:**

take rec

---

### Q14. One docs housekeeping call: where does `docs/live-commands.md` belong?

**Background you need.** `docs/README.md` sorts every doc into one of two tables:
"Generally useful" (for anyone running parlay) and "Internal — integration with
the author's agent fleet" (real design reasoning, but assumes private
infrastructure). One doc sits in neither table: `docs/live-commands.md`, which
specifies the live-command registry — the "what verbs are running right now"
feature. The feature itself is public product (both servers implement it, the
panel and webview render it); the doc's _content_ is mostly design rationale
about redaction and self-reporting, written for maintainers.

**The question.** (a) List it under "Generally useful," (b) list it under
"Internal," or (c) leave it unlisted. (Genuinely small — deciding it here beats
it staying nowhere forever.)

**My recommendation. (a) Generally useful.** The registry's wire format and
redaction rules are exactly what someone building against parlay needs, and the
"internal" table is defined by _private-fleet dependence_, which this doc doesn't
have. One-line change to `docs/README.md`.

**Your answer:**

take rec, might revisit

---

### Q15. Sequencing: what order does all of this land in?

**Where this stands.** Round 1 + this round produce a real backlog: the route
ports (Q2), the relay fold (Q3/Q3a), archive + search (Q4/Q4a), the parlay-owned
beads layer (Q5a), maybe a token (Q6a), the audit file (Q7a), webhooks (Q8), the
deploy pipeline (Q9a), the public spawn verb (Q10), TUI (Q11), TS-CLI deletion
(Q12). These contend for the same attention, and several have real ordering
constraints — which is why this is a decision and not scheduling trivia.

**Background you need — the constraints I can see.** The route ports (Q2) gate
Bun retirement, and Bun retirement gates Q13's leak answer and simplifies
everything after (one server to change instead of two). The relay fold (Q3) is
the biggest single de-risking — it deletes the repo's worst bug factory — but
touches every live agent's delivery path, so it wants to happen while you can
babysit it, not concurrently with five other changes. Webhooks (Q8) and search
(Q4) are pure additions with no dependents. The deploy pipeline (Q9a) is small
and unblocks "fresh box runs the panel," which is the README's headline gap.
Q12 (TS-CLI deletion) is nearly free and shrinks CI immediately.

**The question.** Rank the work. My proposed order, for you to reorder or bless:

1. **Deploy pipeline (Q9a)** — small, makes the Go server demonstrably the real
   host, everything after ships through it.
2. **Route ports (Q2)** — the Bun-retirement gate; do them as one focused block.
3. **Bun server off** (Q1 completes; Q13 resolves itself).
4. **Relay fold (Q3/Q3a)** — done as its own quiet window, nothing else moving.
5. **TS-CLI deletion (Q12)** — anytime after 2; listed here so it isn't forgotten.
6. **Search + archive (Q4/Q4a)**, then **webhooks (Q8)** — the visible new
   features, on a stable single-server base.
7. **Beads layer (Q5a) + public spawn (Q10)** together — they share the lifecycle
   design.
8. **Token (Q6a), audit file (Q7a)** — small, slot in anywhere.
9. **TUI (Q11)** — last, reading stabilized APIs.

**Your answer:**

take rec, doesnt matter, is ai built, will all be built in a short window

---

_End of Round 2. Write `04_ARCHITECTURE-GRILL.md`. Round 3 turns the consensus
register into an ordered implementation-ticket plan (the C-series continuation
for go-server plus the standalone tickets), which is the "shared understanding"
gate before any of it gets built._
