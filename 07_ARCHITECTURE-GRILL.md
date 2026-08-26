# 07 — Architecture grill, Round 4 (agent)

**Status recap.** You answered Rounds 1 and 2 **inline** inside `01` and `03`
(the `02`/`04` files are deleted; where your inline words differ from the
delegated answers that had been recorded, your words win). `00_ARCHITECTURE-GRILL-META.md`
now carries the full consensus register with `(01-inline)`/`(03-inline)`
citations. Q15's "take rec" plus inline answers on every gating question is the
shared-understanding confirmation the grilling protocol was waiting for, so
**Phase 1 has been executed**: PR #106 (go-server deploy ships both frontends,
opt-in `--build`) and PR #107 (webview fail-soft API helpers) both passed
`parlay merge-gate` READY and are merged **and live** on `origin/main`.

Six of your inline answers **overrode** the delegated ones. Restating them so
nothing is lost: plugins and pages are **vital** (not dying); beads is a **hard**
dependency (the soft-dep veto was exercised); the audit log is **server-side
ingest**, not client-side; `parlay spawn` is a **core product verb** with
gascity as the launch entry point (not a public/private split); the Bun SSE
identifier leak is **accepted** until Bun retires; and the hybrid cursor
(client may say where it is, server decides otherwise).

This round: five follow-ups you invited (two of which I answered myself under
the delegation rule), your standing gascity directive answered with a real
capability map, and the deltas those answers force onto the `05` ticket plan.

---

## Answered without you (delegation rule: procedural, or "okay vs good")

### Q5b · What happens when `bd` is absent — RESOLVED

**Decision: refuse loudly, never degrade silently.** Since beads is now a hard
dependency ("bd is still the required backend of this thing"), a verb that needs
it and cannot find it prints a named error naming the missing binary and the
install pointer, and exits non-zero. It does **not** fall back to a degraded
mode. Reasoning: a silent degrade is the exact failure shape this codebase has
been burned by repeatedly (`registered-but-deaf` in robots-dcag, the swallowed
`_ = cmd.Run()` in robots-6xq7) — a dependency that is *required* but *silently
optional* produces a fleet that looks healthy and does nothing. `bd` is
documented as an install prerequisite in the README/install path. No version
pinning yet — added the first time a version-specific behavior actually bites.

### Q7b · Server-side audit vs the "no free-form text" redaction rule — RESOLVED

These looked contradictory: `docs/live-commands.md` mandates that the command
registry stores **no free-form text** (verb, agent id, pid, flag *names* only —
never argv, values, paths, or error strings, because a parlay command line
routinely carries message bodies and tokens), yet you chose server-side
full-fidelity audit ingest.

**Decision: they are two different surfaces and both stay.** The redacted
live-commands registry remains the *unauthenticated* surface, unchanged. The
audit ingest route is a **separate, token-gated** endpoint: full fidelity, but
it requires the bearer token from Q6a — or, when no token is configured,
loopback-only. This makes the Q6a token a hard prerequisite *inside* the audit
ticket rather than a nice-to-have beside it. Nothing that is world-readable
today gains a single new byte of free-form text.

---

## Q2b · What is a plugin, concretely?

**Context.** You said (01-inline): *"must have an ability to have plugins, they
will expand the capabilities of the commands that can be run and executed."*
That's a clear "yes"; what it doesn't yet pin down is *where a plugin lives and
what it is allowed to touch*. Today there is exactly one thing in the repo
called a plugin: `POST /api/chat/plugin/cursorless/rpc`, whose only caller is an
out-of-repo Talon script — i.e. an ad hoc route hardcoded into the server, not a
plugin system. The distinction matters because parlay has three places a
"capability" could plug into, with very different blast radii:

- **(a) Server-side command extensions.** A plugin registers a new *verb* with
  the Go command server: a name, a declared flag set, and an executable (a
  binary or script on disk, discovered from a plugin directory + manifest). The
  server runs it and the result flows back through the existing command/response
  machinery. This is the only option that literally matches your words "expand
  the capabilities of the commands that can be run and executed."
- **(b) Client/panel plugins.** JS modules the panel loads to render new UI.
  This is what most chat apps mean by "plugin", but under your Q2 answer the
  panel is just *a subscriber* to a generic command protocol — so panel
  extensions become a subscriber-side concern, not parlay's.
- **(c) In-process Go plugins.** Go's `plugin` package. Ruled out on facts: it
  is unsupported on the platforms you care about in practice, requires exact
  toolchain matching, and would break the pure-stdlib, single-binary property
  the whole Go rewrite is built on.

**Recommendation: (a), with (b) explicitly delegated to subscribers.** A plugin
is a manifest + executable in a plugin dir; the server discovers it at startup
(and on a `parlay plugins reload`), validates the manifest, exposes the verb,
and shells out. Sandboxing is *not* attempted — a plugin runs with the server's
privileges, and that is stated plainly in the docs rather than pretended
otherwise, exactly as the chat API's "no authentication" is stated today.

> **Your answer:**

---

## Q2c · Pages as a generated-on-install sidecar — what generates them, from what?

**Context.** You said pages are vital but *"not part of the stack that is in
git, but thing bucket gets generated on install and build."* Right now the Bun
server has page routes and the pages themselves are Pulse/PAI-era artifacts; the
Go server has none. "Generated on install" implies three things this repo does
not yet have: a generator, a source of truth for the content, and a destination
directory that install/uninstall owns.

The precedent already in the repo is `packages/go-server/deploy/install.sh`,
which after PR #106 copies pre-built frontend `dist/` into
`~/Library/Application Support/parlay/` and — importantly — **trashes rather
than `rm`s** on uninstall, and resolves the real installed state dir out of the
live plist rather than assuming a default (that rule exists because a `--purge`
once permanently deleted the live `~/.parlay`).

The open question is what the *source* is:

- **(a) Templates in-repo, content out-of-repo.** The generator is a Go
  subcommand (`parlay pages generate`); templates ship in the binary
  (`embed.FS`); the per-user content comes from the state dir / settings. Pages
  land in the install's own directory, tracked by install and trashed by
  uninstall.
- **(b) Fully derived at install from live data** (agents, beads, channels) with
  no template layer — pages are a rendered snapshot, regenerated on demand.
- **(c) External generator** (a separate tool the user brings), parlay just
  serves whatever is in the pages dir.

**Recommendation: (a), with the pages dir served read-only by the Go server
under the existing world-readable wildcard.** It keeps the pages out of git as
you asked, keeps the generator versioned with the binary (so an upgrade
regenerates coherently), and lets uninstall own the artifacts it created. (b)
sounds appealing but makes every page render a live query against beads/registry
— that is a *dashboard*, which the panel already is. (c) is the escape hatch and
costs nothing to also allow.

> **Your answer:**

---

## Q2d · The generic UI-command protocol

**Context.** This is the largest architectural consequence of your Q2 answer:

> *"in a command and response format... use the command server to drive the UI
> of some project that is a subscriber as a core part of parlay."*

Today the "drive the UI" capability is five bespoke, hardcoded routes/events —
`navigate`, `reload`, `device_cmd`, `input_action`, `draft` — each with its own
handler, its own event name, and its own client-side branch. They are also
precisely the events the Go server's ingress allowlist **refuses** from
out-of-process producers, because the guard allows a missing Origin by design,
so any local process could otherwise reload or navigate every connected panel or
overwrite your draft.

Generalizing them means answering who is allowed to issue a UI command:

- **(a) Typed command envelope, capability-declared by the subscriber.** A
  subscriber declares on connect which UI commands it accepts
  (`{"accepts": ["navigate", "prompt", "toast"]}`); the server routes a
  `{id, command, args}` envelope to it and correlates a
  `{id, ok, result}` response. Unknown commands are refused at the edge. Issuing
  a UI command requires the token (Q6a) or loopback — same gate as the audit
  route, for the same reason.
- **(b) Freeform pass-through** — any name, any payload, the subscriber decides.
  Maximum flexibility, and it recreates the exact "push any frame to every
  panel" primitive the ingress allowlist was written to prevent.
- **(c) Keep the five bespoke routes** and add new ones as needed.

**Recommendation: (a).** The correlation id is the part that earns its keep — it
turns "drive the UI" from fire-and-forget broadcasting into a request/response
you can actually build on (ask the panel a question, get an answer back), which
is what "command and response format" reads as. The capability declaration is
what lets the server refuse safely instead of trusting every producer, and it
subsumes all five existing events as ordinary command names.

**One consequence worth your explicit sign-off:** under (a), the five current
events become deprecated aliases and eventually go away — a breaking change for
any out-of-repo caller you have wired to them (Talon scripts, hooks). I'd keep
them as aliases for one release cycle rather than cutting them at the switch.

> **Your answer:**

---

## Q6b · What does "tailscale as the connection layer" mean concretely?

**Context.** You said (01-inline): *"I explicitly want an option to use
tailscale as the connection layer to make this screamingly easy."* Agreed on the
goal — phone-to-laptop with no port forwarding, no reverse proxy, no
certificate. There are two genuinely different implementations, and the choice
turns on a dependency question this project has otherwise been strict about
(every Go module here is **pure stdlib** — no `go.sum`, no external requires):

- **(a) `tailscale serve` integration in the deploy tooling.** The server keeps
  binding loopback; `install.sh` optionally runs
  `tailscale serve --bg 4242`, which publishes it on your tailnet with a real
  HTTPS cert and a stable `*.ts.net` name. **Zero code dependency** — it is a
  deploy-script feature. Requires the Tailscale app installed and logged in
  (which, if you're asking for this, it is). Tailscale's own identity headers
  can then be read for "who is this" without parlay implementing auth.
- **(b) Embed `tsnet`.** The Go binary *becomes* a tailnet node: it gets its own
  device name and IP, listens on the tailnet directly, no host Tailscale needed.
  Genuinely "screamingly easy" for the user. Cost: `tailscale.com/tsnet` is a
  large dependency tree, and it ends the pure-stdlib property of
  `packages/go-server` — the single most defensible constraint the Go rewrite
  currently has (it is why the whole Go suite runs in seconds with a build cache
  and no download cache).

**Recommendation: (a) first, (b) later behind a build tag.** (a) ships in a day,
costs zero dependencies, and delivers most of the ergonomics. If (b) is
warranted after living with (a), it goes in as `tools/tailnet` or a
`//go:build tailscale` variant so the default binary stays stdlib-only — the
same isolation pattern you already approved for Bubble Tea in Q11.

**This is a genuine human call**, not an okay-vs-good one: (b) is more
convenient for a user who has never installed Tailscale, and only you know
whether parlay's audience is "already a tailnet user" or "needs to be handed
one."

> **Your answer:**

---

## Q16 · Gascity leverage map (your standing directive)

**Your directive, verbatim (01-inline, closing note):**

> *"We are specifically trying to find scenarios where gascity can be leveraged
> to take over hard parts of this tool. What does gascity do well, where we have
> to maintain less while gascity trucks along."*

I ran a full read-only survey of `~/code/gascity` (module
`github.com/gastownhall/gascity`). Findings first, then the map.

### The structural fact that constrains every option

**Essentially all of gascity is under `internal/`.** The only exported package
in the whole module is `pkg/eventexport`. Go's `internal/` visibility rule is
absolute — no `replace` directive, no vendoring trick, no fork-free workaround
makes those packages importable from parlay. So "leverage gascity" can only mean
one of four things, and it's worth naming them because they have wildly
different costs:

1. **Shell out to the `gc` CLI** (~250 subcommands).
2. **Call its HTTP API** (huma/OpenAPI; `internal/api/openapi.json` ships a spec,
   and there's a generated client).
3. **Port source into parlay** (read the design, write our own).
4. **Import `pkg/eventexport`** — the one thing we can actually `go get`.

**Anti-recommendation, stated plainly: do not `go get github.com/gastownhall/gascity`.**
Beyond the `internal/` wall blocking ~99% of it, its dependency graph is dolt +
go-mysql-server + client-go + cloud SDKs. That would dominate parlay's build and
end the pure-stdlib property outright. The 237MB binary is the tell.

### Ranked leverage opportunities

**1. Worktree teardown safety — PORT THE DESIGN (highest value).** This is
parlay's most dangerous surface and gascity's most mature one.
`internal/git/git.go` is 519 lines of exactly the checks
`checkWorktreeGitSafety` reaches for: `HasUncommittedWork`,
`HasUnpushedCommits`, `HasUnreachableCommits`, `HasStashes`, `AheadBehind`, plus
git-env hardening. More valuable than the code is
`cmd/gc/bead_worktree_reaper.go`'s **six-gate ordering**: never-remove-named-homes
→ owning-bead-closed → freshness quarantine → borrow-veto → liveness (where
*indeterminate reaps nothing*) → git state judged by **reachability, not push
state**. That last one is the fix for the exact bug this repo hit in robots-ceon
— `isContentLanded` failing closed for every input because two-arg `git
merge-tree` isn't a predicate. Parlay's `sweep`/`teardown` guards were each added
reactively after something got destroyed; gascity's are one designed ordering.

**2. Process stop / liveness semantics — PORT THE DESIGN.**
`process_control.go` (SIGTERM → 5s grace → SIGKILL → 3s *confirmed-death* reap
grace) and `internal/pidutil` (pid-reuse guards via process start-time **and**
cmdline, not pid alone). Most valuable single idea: `liveness.go`'s **three-way**
return — `Running` / `Alive` / `ErrRuntimeUnavailable` — encoding
"observation failed" as distinct from "nothing is running", with destructive
actions deferring on the former. That is the same principle parlay arrived at
independently and piecemeal (`[ghost]` classification failing open when `ps` is
unreadable; `stale`'s `unknown` failing open). Gascity has it as one type.

**3. Subprocess provider pattern — PORT (one file, ~immediately useful).**
`internal/runtime/subprocess/subprocess.go` uses a per-session unix socket as
*both* liveness proof and control channel. Directly relevant to the
`gascity-spawn` path already in `tools/parlay-bin` — which deliberately used a
PID file instead precisely because a socket needs a living creator process, and
that note in the file is still the right call for one-shot CLI invocations. But
the control-channel half is worth having if parlay ever gains a supervisor.

**4. Event spool + cursors — IMPORT (`pkg/eventexport`) + port the design.**
The only genuinely importable package, and it happens to solve a problem you
just made decisions about: cursor persistence that **fails closed on
corruption**, projection/redaction before export, batched HTTP delivery. Pair it
with `internal/events`' design — seq-ordered JSONL, gzip rotation with atomic
rename, archive backfill on read, `ReadFrom(offset)`. That is *precisely* Q4a
(unbounded retention + archive) and Q3a (hybrid cursor) already built and
running. Strongest candidate for real code reuse.

**5. Run gascity as a service — THE ONLY WHOLESALE OPTION.** Via the OpenAPI
client or the `gc` CLI, parlay gets session lifecycle + registry + presence +
mail + SSE without maintaining any of it. The cost is a `city.toml`, a
controller dependency, tmux/git/jq/pgrep/lsof on PATH, and a `gc start` — i.e.
parlay stops being a single binary you can hand someone. **My read: this is the
one that contradicts everything else you've decided** (single Go binary, prod
Go-only, install by copy). Recommend **no** — unless your intent is that parlay
becomes a gascity front end, which is a product decision only you can make.

**6. `internal/extmsg` as a design template — READ IT.** External-conversation↔session
binding, a binding reaper, default routing, handoff, a transcript service. That
is the chat-relay problem parlay solves, solved once more by someone else. Copy
the model, not the code.

**7. Auth patterns — PORT SELECTIVELY when Q6a lands.** Two are directly
applicable: the clientauth exec-credential contract (secret passed via
`GC_EXEC_INFO` **env, never argv**, inherited `GC_*_INFO` stripped, cached to
expiry with 30s skew), and citywriteauth's request-bound single-use ed25519
grants (digest binds method+path+query+body; jti replay guard). The first is
cheap and immediately relevant to the bearer token. The second is over-engineered
for parlay today.

**8. Skip outright:** commandcensus (telemetry codegen — parlay's live-commands
registry already does this better *for parlay*, with a stricter redaction rule),
`internal/beads` (dolt-entangled; use the `bd` CLI, or the `GC_BEADS=file` shape
which needs no dolt at all), and the k8s provider.

### Two facts that limit the map

- **Worktree *creation* is not in gascity's Go at all** — it's pack shell
  scripts. So parlay's treehouse-lease integration has no gascity counterpart to
  adopt; that stays ours.
- **Registry/presence is bead-coupled by design** (sessions *are* beads;
  heartbeat is `held_until`; adoption-on-restart). Given Q5's "beads is the
  required backend," this is convergent evolution rather than a blocker — but it
  means adopting gascity's registry means adopting its bead schema.

### The question for you

**Q16: which of these do you want taken?** My recommendation, in order:
**(4)** import `pkg/eventexport` and port the `internal/events` design into the
archive/cursor ticket — it is real code reuse on a problem you just specified;
**(1)+(2)** port the worktree-safety gate ordering and the three-way liveness
type into `teardown`/`sweep`/`stale`, since those are the places parlay has
already destroyed work; **(7)** the exec-credential contract when the token
lands; and **decline (5)** so parlay stays a single binary.

That answers the directive's actual shape: gascity's leverage here is
**designs and one importable package**, not a service to hand work off to. The
`internal/` wall means "gascity trucks along while we maintain less" is only
available via option (5), and option (5) costs the single-binary property. I'd
rather take the four cheap wins than the one expensive one — but the call on (5)
is yours, because it's a product-identity question, not an engineering one.

> **Your answer:**

---

## Plan deltas — amendments to `05`

Your inline answers change the ticket plan. Recording them here rather than
editing `05` (the numbered files are append-only history).

| Ticket | Delta | Driver |
|---|---|---|
| T-01 · Bun SSE leak patch | **DOWNGRADED** — leak accepted until Bun retires; keep only if trivially cheap | Q13 "take rec" |
| T-02 · Frontend deploy | **DONE** — PR #106, merged and live | Q9/Q9a |
| T-04 · TTS pluggable | stands; now also the first consumer of the Q2b plugin system | Q2 |
| T-11 · Beads + spawn | **REWRITE** — beads is a hard dep (no degraded no-bd mode; refuse loudly per Q5b); `parlay spawn` is a core verb; bash `parlay-spawn` deprecated; gascity is the launch entry point | Q5/Q5a/Q10 |
| T-12 · Token + audit | **REWRITE** — audit is server-side ingest, so the Q6a token is a hard prerequisite *inside* this ticket; add the Tailscale connection layer as sibling scope | Q6/Q6a/Q7/Q7b |
| **T-15 (new)** | Plugin system — manifest + discovery + verb registration | Q2/Q2b |
| **T-16 (new)** | Pages as generated-on-install sidecar | Q2/Q2c |
| **T-17 (new)** | Generic UI-command protocol; deprecate the five bespoke panel-aiming routes | Q2/Q2d |
| **T-18 (new)** | Gascity adoption — `pkg/eventexport` + ported worktree-safety/liveness designs | Q16 |

---

## What I need from you in Round 5

Four open questions: **Q2b** (plugin surface), **Q2c** (pages generator),
**Q2d** (UI-command protocol, including the deprecation-window sign-off), and
**Q6b** (tailscale shape) — plus **Q16**'s pick-list. Q5b and Q7b are answered
and need no reply unless you disagree.

Answer inline under each "Your answer:" heading, as you did in `01`/`03` — that
works well and I'll keep folding them into `00`'s register.
