# Project agent memory

This file is internal operating memory for AI agents working in this repository, not user documentation — it is written for whoever (or whatever) is editing the code next. See [`README.md`](README.md) if you are looking for how to run parlay.

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- Add durable project-specific notes here as they are discovered through real work.

## `packages/server` is a standalone Bun server for the Parlay chat API

`packages/server/src/*` are real files: a self-contained `bun serve()`
application that owns `/api/chat/*`. Entrypoint `src/index.ts` binds
`PARLAY_PORT` (default 4242) and routes every request through
`handleChatRequest`. Run it standalone — `bun run start` (or `bun run dev`
for watch); config is `PARLAY_PORT` and `PARLAY_DATA_DIR` (history/draft dir,
default `~/exchange`). See `packages/server/README.md` for the full config
surface and data-file locations. Live chat history is `~/exchange/chat-history.jsonl`
— do not move or clobber it.

Historical sharp edge (fixed here): `src/*` were once committed symlinks into
`~/.claude/PAI/PULSE/modules/chat`, which was itself a symlink back to this
directory — a self-referential loop that made every `import` throw `ELOOP`, so
Pulse's in-process chat module never loaded and `/api/chat/*` returned 404 to
the CLI, relay, and panel. The real source was recovered from git and the loop
symlinks replaced with real files. The Pulse side must stop importing
`modules/chat` in-process and instead reverse-proxy `/api/chat/*` to this
standalone server; that rewire and removing the `~/.claude` loop symlink are
production changes made outside this repo (never edit `~/.claude` from here).

### Adding a mutating `/api/chat` route? Add it to `GUARDED_CHAT_PATHS`

`packages/server/src/guard/` is the one security boundary for the chat API
(which still has **no authentication**) — `guard/paths.ts` is the route set,
`guard/origin.ts` the origin/content-type predicates, `guard/index.ts` the
policy that applies them. `handleChatRequest` in `router.ts` runs it before
dispatch: paths listed in `GUARDED_CHAT_PATHS` require an
allowed `Origin` (missing Origin = allowed, which is how the CLI/hooks/curl
keep working) and `Content-Type: application/json` (415 otherwise — this is
what stops a cross-origin CORS *simple request* from reaching a handler
without a preflight, and preflight on those paths is refused). Their
responses are re-headered by `withGuardedCors` so the wildcard `CORS` the
handlers spread never reaches the wire. The rest — history, agents, events,
version, pages, and `GET /api/chat/uploads/<name>`, which an `<img src>` must
load — keeps the old wildcard and stays world-readable.

**The rule, on both servers: inside the mutating and identifier-aiming
surface, a route is guarded by what its handler DOES, REGARDLESS OF HTTP
METHOD.** The verb is not evidence — `GET /subscribers` is guarded because it
hands out identifiers, and `GET /poll` because it registers the channel it is
polling for. Classify by reading the handler.

That is the boundary that exists, and it is narrower than "everything that
writes or discloses". Two TS routes are known, accepted, deliberately
unguarded residue — accepted meaning somebody looked and decided, not that
nothing is exposed. `GET /api/chat/events` writes `sseClients` from an
attacker-supplied `?device=` (`router-events.ts`), and the `tts_event` frames
it streams carry that device uuid to every connected client
(`router-tts-events.ts` broadcasts `{ …, device, ...body }` unfiltered), so a
cross-origin `EventSource` can read it; `GET /api/chat/agents`
(`router-messages.ts`) hands any origin every registered agent id under the
wildcard `CORS` — the same class of disclosure `/subscribers` was guarded for.
Both are tracked as `identifier-disclosure-remains-on-sse`, ruled out of that
change's scope rather than overlooked. What keeps the residue from chaining is
that every route that AIMS anything (eval, draft, device-cmd, navigate,
reload, poll, upload, subscribers) is guarded. The Go server's unguarded
routes send no ACAO at all, so a foreign page cannot read their bodies.

A new route that injects into an agent turn, mutates the registry, or drives a
device is unguarded until you add it to that set — and if its callers do not
send a JSON content type, adding it breaks them. Test with
`packages/server/src/guard/{paths,origin,allow,reject}.test.ts` (pure, no side
effects — `guard/origin.ts` and `guard/paths.ts` deliberately import nothing)
and `guard/integration-{attack,callers,origin-branches}.test.ts` (each spawns
a real server via `guard/scratch-server.ts` on a port reserved by binding
`:0`, with `HOME`/`PARLAY_DATA_DIR`/`PAI_DIR`/`PARLAY_STATE_HOME` redirected
to a temp dir).

**The route SET is the part that rots, not the mechanism.** An end-to-end
verification (task-6ai1, defects D9/D7) found the guard working exactly as
documented while `/eval`, `PUT /draft`, `/upload`, `/parlay/settings`, the tts
family, the cursorless plugin RPC and `/api/debug/input-timing` all sat
outside it — and `GET /subscribers` handed any origin the connected device
uuid plus every registered agent id, which is what made the rest aimable
(read the device id → `input_action` into the panel → set the captain's draft
→ submit attacker text as the captain). All of them are guarded now.
`/subscribers` was **guarded rather than redacted**: its only panel caller is
same-origin (`packages/client/src/tab-online.ts`, a relative `fetch` for the
per-tab online check), and every caller outside the panel (`parlay
doctor`/`subscribers`/crew-state, the Go CLI, `tools/split-test`) is a
no-Origin HTTP client, so guarding costs them nothing. `GET /poll` was added
for the same reason a round later — on the TS side it auto-registers an
unknown `channel` in the agent registry, broadcasts `agent_register` and calls
`persistAgents()`, so a cross-origin CORS-simple GET could create an agent and
write it to disk; the Go handler only takes a Presence poller slot, and is
guarded on that (see its package comment — the Go set is derived from Go
handlers, never copied from TS). Every poller in this repo is a no-Origin HTTP
client and nothing in `packages/client` polls at all. Two structural notes:
`/api/debug/*` is dispatched in `index.ts`
*ahead of* `handleChatRequest`, so it runs the guard itself — anything else
added there must too; and `JSON_EXEMPT_PATHS` is how a route keeps the origin
check without the JSON content-type gate. It is a CLOSED three-member list,
decided by one sweep of the whole guarded set against a three-part test — the
handler parses the body regardless of Content-Type, the contract is JSON by
*semantics* rather than by header, and no in-repo caller depends on the strict
header — rather than a queue that grows one route per bug report. The members:
`/api/chat/upload` (multipart by contract); `POST
/api/chat/plugin/cursorless/rpc` (its handler is `await req.json()`, which
parses the body whatever the header says, and its only caller is an
out-of-repo Talon script — so its contract has always been a JSON *body*,
never a JSON content type); and `POST /api/chat/tts/validate-splits` (same
shape — `await req.json()` in `tts-validate.ts`, a header-documented hand-run
contract stating no content type, and zero callers anywhere in this repo, so
the only callers are hand-typed `curl -d`, whose default is
`application/x-www-form-urlencoded`). Caller evidence, not the handler shape,
is what decides membership: most other guarded routes have an in-repo caller
already sending `Content-Type: application/json`, so the gate costs them
nothing. The same sweep escalated five routes and deliberately left them
unexempt — `/api/chat/tts`, `/api/chat/system`, `/api/chat/clear`,
`/api/chat/navigate`, `/api/chat/device-cmd` — so do not re-litigate them one
gate at a time; the per-route reasons are in `guard/paths.ts` next to
`JSON_EXEMPT_PATHS`. All three exempt routes stay inside the guarded set; the
exemption drops one layer, not the boundary. `packages/go-server` has no
`/api/chat/tts*` route at all, so only `/api/chat/upload` is exempt there.

`packages/go-server` now has its own guard: `internal/guard`, wrapped once
around the whole mux in `cmd/parlay-server/main.go` so a route registered
later cannot land outside it. Same semantics — missing Origin allowed,
same-origin/loopback/LAN/`PARLAY_ALLOWED_ORIGINS` allowed, 403 with no CORS
headers otherwise, 415 on non-JSON POST/PUT, reflected ACAO plus `Vary:
Origin`. Its package comment is the authoritative statement of the two
deliberate divergences (its unguarded routes send no ACAO at all, where the
TS side still wildcards; no blanket OPTIONS answer) — both stricter, because
this server has never sent CORS headers and matching TS exactly would newly
*open* read access.

`packages/server/src/debug-log.ts` was written during the loop outage as a
standalone, not-yet-wired handler (Write could still create files whose names
did not already exist in the symlink farm). Now that `src/` holds real files
again it can be wired into `router.ts`/`index.ts` normally; see its header
comment for the intended wiring.

**`packages/client`'s `build.ts` has a live side effect**: every successful
build POSTs to `http://127.0.0.1:31337/api/chat/reload`, and that port is the
captain's real, live local Pulse server — not sandboxed per worktree. Running
`bun run build` / `bun build.ts` in `packages/client` from *any* environment
that shares the host's network namespace (including disposable pool
worktrees) force-reloads the captain's actual connected clients. The built
bundle itself lands harmlessly in the invoking worktree's own `dist/` (and
gitignored `pulse-agent.js`/`plugins/`), so this doesn't ship broken code —
but it does interrupt whatever the captain is doing. Prefer `bun test` or a
scoped `bun build src/<file>.ts --outdir=<tmp>` (no `build.ts`) to validate
client changes without triggering the reload beacon.

`packages/cli` talks to whatever server is running over HTTP. Target resolution
(`serverUrl()` in `packages/cli/src/config.ts`): `PARLAY_SERVER` env var >
persisted `$PARLAY_STATE_HOME/config.json` (default `~/.parlay/config.json`,
`"server"` key, managed via `parlay remote set/clear`) > coded default
`http://localhost:4242`. `parlay doctor` reports which source is active. The
CLI does not import `packages/server` as code, so its functionality is
independent of how the server is deployed.

### `PARLAY_DATA_DIR` isolates what the server WRITES, not what it READS

`PARLAY_DATA_DIR` redirects every persisted file that goes through `paths.ts`, so
nothing lands under `~/exchange`. It is **not** a full sandbox, and not only on
the read side. `packages/server/src/tts.ts` never touches `paths.ts`: it resolves
its own `PAI_DIR` (`process.env.PAI_DIR ?? ~/.claude/PAI`) and then **writes**
`$PAI_DIR/MEMORY/OBSERVABILITY/tts-pronunciation-reports.jsonl` (`appendFileSync`,
from both `/api/chat/tts-report` and the substitution handler), **creates**
`$PAI_DIR/MEMORY/STATE/tts-cache/`, and **`unlinkSync`-deletes** clips out of that
cache every time it passes `DISK_CACHE_MAX` (100). `PARLAY_DATA_DIR` redirects
none of it. On the read side, `startHookFiringTailer()` and
`startToolEventTailer()` still watch `$PAI_DIR` (default `~/.claude/PAI`), which
`PARLAY_DATA_DIR` does not cover, and every event they see is injected into the
instance's chat history as a `channel:"system"`, `source:"turn"` message. A
scratch instance booted on a host that has a populated `$PAI_DIR` will therefore
fill with real, live agent turns from the agents running on that host — read-only,
but confusing (and a quiet information leak) for whoever is reading the sandbox.
Set `PAI_DIR` to an empty scratch dir alongside `PARLAY_DATA_DIR` whenever you
boot a test instance, or the run writes into and deletes out of the real one;
`guard.integration.test.ts` already redirects both, and the
public Quickstart in `README.md` says the same for anyone who happens to have a
`~/.claude/PAI`.

### Never `pkill -f 'src/index.ts'` — `com.parlay.chat-server` matches it

On a development host the production chat server runs as a launchd job
(`com.parlay.chat-server`) executing `~/code/parlay/packages/server/src/index.ts`,
so any broad process match on `src/index.ts`, `bun`, or `parlay` kills **that**
server, not just your sandbox's. It is launchd-supervised and respawns in about a
second, so the blast radius is a brief interruption rather than lost data — but it
is a live server other agents are talking to, and nothing in the pattern tells you
which instance you matched. Tear a test server down by the thing that is unique to
it: the port (`PARLAY_PORT=<45xxx>` in the match, or the listening pid) or its
scratch `PARLAY_DATA_DIR` path. The same trap applies to the relay: `$TMPDIR/parlay/`
is the host-wide shared runtime dir and a scoped test relay lives in a
`srv-<hash>` subdirectory of it (see the robots-buu8 section below) — match the
subdirectory, never the parent.

## Remote debug log + on-screen mobile console (phone-only bug triage)

`packages/client/src/debug-log.ts` captures `window.onerror`,
`unhandledrejection`, `console.error`/`warn`, and explicit `logTrace()` calls
(used in `thread-scroll.ts` and the `#pa-jump` handler in `jump-button.ts`), batches
them, and POSTs to `${CHAT_BASE}/debug-log` (i.e. `/api/chat/debug-log`).
Disable with `?paDebug=0` or `localStorage['pa-debug-log']='0'`. The server
handler (`packages/server/src/debug-log.ts`, not yet wired — see the section
above) appends formatted lines to `$PARLAY_STATE_HOME/debug.log` (default
`~/.parlay/debug.log`); once wired, read it with `tail -f ~/.parlay/debug.log`.
Disable server-side with `PARLAY_DEBUG_LOG=0`.

`packages/client/src/mobile-console.ts` lazy-loads `eruda` from a CDN for an
on-screen console on the phone itself. Toggle via `?paConsole=1` in the URL
(sticky, persists to localStorage) or a ~600ms long-press on the drawer
trigger button. Default off, zero bundle cost when unused.

## Go CLI (`tools/cli`, the `packages/cli` rewrite) cites two docs that don't exist in this checkout

Comments across `tools/cli/**/*.go` (config, args, identity, monitor, …)
repeatedly cite `docs/scope-go-cli.md` and `docs/plan-go-migration-tickets.md`
as authoritative for naming/behavior decisions (env var names, exit codes,
FNV color-hash parity, etc.) — neither file exists under `docs/` as of this
writing (`docs/api-contract.md`, also cited from `internal/httpc`, does
exist and is accurate). Treat the `scope-go-cli.md`/`plan-go-migration-tickets.md`
rationale as historical/aspirational: verify against the actual TS source
(`packages/cli/src/*.ts`, not symlinked, safe to read directly) and the
landed Go code's own tests instead of trying to open those paths.
`tools/cli/internal/monitor` (ticket B2: `monitor`/`listen`) is
a straight shell-out port — the relay path execs
`tools/monitor/parlay-monitor.sh`, resolved via `exec.LookPath` first (so a
future PATH install is picked up) and falling back to a repo-relative path
computed from the Go source file's own location, the closest Go equivalent
of the TS original's `import.meta.url`-relative resolution.

## Go rewrite of `packages/server`: use `docs/api-contract.md`, not `docs/scope-go-server.md`

`packages/go-server` (module `parlay/go-server`) is the Go rewrite of Pulse's
HTTP/SSE chat server, built ticket-by-ticket (C0: HTTP skeleton + storage
layer in `internal/store`; C1: messaging/registry/legacy-poll handlers in
`internal/handlers`; C2: the SSE hub behind `GET /api/chat/events`, also in
`internal/handlers` — see `events.go`'s package-level doc comment for exactly
which of the 17 event names documented in `docs/api-contract.md` have a live
producer today (message, message_received, agent_register, plus the
connect-time burst of connected/history/agents/presence_map) versus which are
wire-ready but unproduced pending a future ticket (drafts, device-cmd,
tool/session events, etc.); C3: drafts/uploads/settings, also in
`internal/handlers` — see the dedicated section below; C4+ still open —
eval-relay/debug-log, parity harness, deploy tooling). Ticket briefs for this
workstream point at `docs/scope-go-server.md` as the authoritative spec —
**that file has never existed anywhere in this repo's git history** (checked
with `git log --all --diff-filter=A -- '*scope-go-server*'`, no hits, as of
this note). Use `docs/api-contract.md` instead: a ~600-line HTTP contract for
every `/api/chat/*` route, reconstructed from `packages/client`/
`packages/cli` call sites (since the real handler source is the broken
symlink farm described above), already referenced by name in C0's own store
doc comments. It landed on `origin/main` for real via PR #27 — if a future
worktree is missing it, that's a stale/pre-#27 base, not a sign the doc
doesn't exist; rebase onto current `origin/main` rather than re-deriving or
re-cherry-picking it. (History note: before #27 merged, C1 found it early by
checking `git log --all` for every local ref, not just `origin/main` — it was
sitting on a diverged, not-yet-pushed local `main` commit. That workaround is
now obsolete but is the reason to always check `git log --all` before
concluding a referenced doc "doesn't exist" in future tickets.)

## Go CLI ticket B5: `status`/`crew-state`/`supervise`/`unattended-queue`/`context-check`

Ticket B5 (`status` verb, `crew-state`, `supervise`, `unattended-queue`,
`context-check`) fixed one confirmed TS bug during the port —
`crewStateForAgent(agentId)` (`commands-crew-state.ts`) resolved its status
file via the caller's own `PARLAY_AGENT_ID`/`PARLAY_STATUS_FILE` instead of
the passed `agentId`; the Go port (`internal/commands/status_verb.go`'s
`statusFileForAgent`) resolves the target agent's file directly. Two sibling
defects were found but deliberately left bug-for-bug faithful in B5 (only the
crew-state fix was in scope there); a same-branch follow-up ticket then
extended the identical fix to both, each pinned by a regression test:
- `commands-supervise.ts`'s `cmdSupervise` had the identical
  caller-identity-instead-of-argument bug — only mattered when supervise was
  invoked by something other than the target agent itself. Fixed in
  `internal/commands/supervise.go`'s `Supervise` by resolving the status file
  via `statusFileForAgent(agentID)` instead of `statusSink()`.
- The shared status-line regex
  (`/^(\w+)(?:\s*\[key=...\])?\s*:\s*(.*)$/`, ported to
  `statusLineRe` in `internal/commands/crew_state.go`) used `\w+` for the
  verb, which couldn't match hyphenated verbs (`needs-decision`,
  `captain-held`) despite both being in the code's own declared verb
  vocabulary — such lines silently failed to parse and read back as
  "unknown / no status recorded" in both crew-state and supervise. Fixed by
  widening the verb class to `[\w-]+`.

`tools/cli/internal/commands/{guard,teardown,variant}.go` (ticket B4) port
`commands-guard.ts`/`commands-teardown.ts`/`commands-variant.ts` verbatim,
including three TS-source quirks worth knowing before touching this code:
(1) all three TS files hardcode `AGENTS_DIR`/`WKTREES_DIR` to
`homedir()/.parlay/{agents,worktrees}` and never honor `$PARLAY_AGENT_HOME`
or `$PARLAY_STATE_HOME` — unlike `internal/identity.AgentsRoot()` (honors
`$PARLAY_AGENT_HOME`) or `commands-guard.ts`'s own beacon path (honors
`$PARLAY_STATE_HOME`); the Go port preserves this split via non-env-aware
`parlayAgentsDir()`/`parlayWktreesDir()` helpers in `guard.go`, deliberately
distinct from `internal/identity`/`internal/config`'s env-aware equivalents.
(2) `cmdVariantTeardown`'s `try { await postJSON(...) } catch {}` around its
unregister call looks best-effort but isn't: `die()`'s `process.exit()`
is not a catchable JS exception, so an unreachable server there genuinely
aborts teardown before the final cleanup+success message — verified
empirically against the Go port. Contrast `commands-teardown.ts`'s raw
`fetch(...).catch(() => {})`, which IS genuinely best-effort (no status
check, network errors swallowed). The Go port matches both real behaviors:
`variant.go`'s teardown calls `httpc.PostJSON` unwrapped (dies loud, matching
reality over the misleading comment); `teardown.go` has its own
`bestEffortUnregister` that truly swallows every error.
(3) `commands-teardown.ts`/`commands-variant.ts` each define their own local
`parseFm`, distinct from `commands-identity/store.ts`'s `readFrontmatter`
(what `internal/identity.ReadFrontmatter` mirrors) — the local `parseFm`'s
per-line regex requires the whole `key: "value"` shape to match, silently
dropping a line whose value contains an embedded quote rather than keeping
it mangled. `guard.go`'s `readLocalFrontmatter`/`localFrontmatter` replicate
that local parity for `teardown.go`/`variant.go`'s frontmatter reads; see the
doc comment above `localFrontmatterBlockRe` in `guard.go` for the full
rationale. `identity.go`'s register/launch/rename/reap-ephemeral verbs are
unaffected — they keep using `internal/identity.ReadFrontmatter`, matching
their own TS source.

## Go CLI ticket B8: `resolve-handoff`/`say-guard` — the port was already done, only the dispatch wire was missing

Ticket B8 targeted porting `packages/cli/src/resolve-handoff.ts` and
`say-guard.ts` (the create->submit death-window fix: `identity
--submit`/`--park`/`--handoff` resolve a missing id from the newest open
handoff bead; `parlay say`/`reply` warns loudly-but-non-blocking on stderr
when sent inside that unsubmitted window, with a separate gentle warning for
a handoff *inherited* from a prior session on the same agent id). By the
time this ticket ran, B1 (`internal/resolvehandoff`, `internal/sayguard`,
and `internal/identity/mem.go`'s `--handoff`/`--submit`/`--park` id
auto-resolution) had already landed the entire port faithfully, including
`internal/identity/say.go`'s `CmdSay` calling
`sayguard.WarnIfUnsubmittedHandoff` — B1 just deliberately left `say`/`reply`
out of `main.go`'s dispatch switch (see B1's note, previously in `say.go`'s
header) because that ticket's DoD only covered `identity`/`scratchpad`. B8's
actual remaining work was: add the `case "say", "reply":` arm to `main.go`,
plus the test coverage that gap had also left missing (`internal/sayguard`
had zero tests; `internal/identity/say_test.go` — CmdSay's own dispatch —
didn't exist, mirroring the TS side where `say.ts` similarly has no
`say.test.ts`, only `say-guard.test.ts`/`resolve-handoff.test.ts`). Lesson
for future tickets in this workstream: a "port X" ticket brief can already
be substantially or fully done by an earlier ticket's broader scope — grep
for the target package/behavior across `internal/` before assuming a blank
slate.

## Go server ticket C3: drafts/uploads/settings (`internal/handlers`)

C3 built on C0's already-landed `internal/store` (which already had
`DraftStore`/`SettingsStore` — only `UploadStore`, in
`internal/store/uploads.go`, was new) and is registered via its own
`handlers.RegisterData(mux, st)`, called from `main.go` alongside C1's
`handlers.Register(mux, st)` — deliberately not folded into C1's `Register`,
so this ticket never had to touch or depend on C1's broker or a later C2's
SSE hub. Two things `docs/api-contract.md` doesn't pin down, decided here:

- **`GET`/`PUT` share one mux registration per path** (`handleDraft`,
  `handleSettings`, each switching on `r.Method` internally) rather than two
  separate `handleFunc` calls on the same pattern — `net/http.ServeMux`
  panics on registering the exact same pattern twice, and C1's handlers
  never hit this because each of their routes is single-method.
- **Uploads need a serving route the contract doesn't document.** `POST
  /api/chat/upload` returns `{ok, url}`, and `packages/client/src/
  attachments.ts` renders that `url` directly as an `<img src>` — so
  something on this server has to answer that URL. `UploadStore` saves each
  file under `<state-dir>/uploads/<random-hex><ext>` (never the
  client-supplied filename, which is discarded except for its sanitized
  extension) and `handleServeUpload` is mounted at the same
  `/api/chat/uploads/` prefix the upload response's `url` is rooted at, so
  the returned URL is always directly `GET`-able. Image type is verified
  server-side via `http.DetectContentType` sniffing on the actual bytes
  (not the client-supplied `Content-Type` header), capped at 10MB per the
  contract's documented client-side UI copy, now also enforced server-side.
  `handleServeUpload` re-sniffs those same bytes at serve time to set the
  response `Content-Type` (`http.ServeContent`, not `http.ServeFile`'s
  extension-based `mime.TypeByExtension`), and `UploadStore.Save`'s kept
  extension is allow-listed to image extensions only
  (`png|jpg|jpeg|gif|webp|bmp`) — together these mean a served upload's
  declared type is always derived from its real bytes, never from a
  client-supplied filename or extension.

## Go CLI ticket B7: `doctor`/`health`

`tools/cli/internal/commands/doctor.go` ports both `cmdDoctor` and `cmdHealth`
from `commands-doctor.ts` (they share one TS file, so they share one Go
file). One deliberate quirk carried over: the `identity.md` frontmatter check
inside `Doctor` uses its own ad hoc regex pair (`doctorFrontmatterRe`,
`doctorIDRe`) instead of `internal/identity.ReadFrontmatter` — the TS
original does the same (a local `txt.match(/^---\n([\s\S]*?)\n---/)` plus a
separate `id:` extraction), and its block regex has no required trailing
newline after the closing `---`, unlike `ReadFrontmatter`'s stricter one.
Matching this exactly means `doctor`'s launch-spec-presence check can behave
differently from `identity`'s own frontmatter parsing on a malformed file —
intentional fidelity to the TS source, not an oversight. `commands-doctor.ts`
has no dedicated TS test file to mirror; `doctor_test.go`'s cases were
derived directly from reading the implementation.

## Go CLI ticket B6: `robots-watch`/`robots-tail` — a TS command-*folder* becomes its own Go package

`internal/robotswatch` ports `packages/cli/src/commands-robots-watch/{index,
detect,handlers,cursor,tail}.ts` (the panic-isolated event poll-daemon plus
its byte-offset tailer). Confirms the layout convention `internal/identity`
already established: a TS command implemented as a *folder* of files (not a
single `commands-X.ts`) gets its own Go package under `internal/`, not a file
inside `internal/commands` — `internal/commands` is reserved for the
single-file `commands-*.ts` ports (ticket B3/B4 style).

Two things worth knowing before touching this code:
- **Panic isolation is `defer`/`recover` at the same two boundaries as the TS
  try/catch**, not smeared into every I/O helper. `watch.go`'s `runPollOnce`
  recovers a whole bad pass (mirrors `index.ts`'s outer try/catch);
  `handleRoutedEvent` recovers one failing handler without losing the rest of
  that pass's diff (mirrors the per-event try/catch inside `pollOnce`).
  `tail.go`'s `tickIsolated` is the same pattern for the tailer's loop. To
  make this work, `cursor.go`'s `writeCursor` and `tail.go`'s `writeOffset`/
  `readNewLines` deliberately `panic()` on unexpected fs errors instead of
  swallowing them — matching an unguarded `mkdirSync`/`writeFileSync` throw in
  the TS source bubbling up to that same outer catch. Don't add local
  error-swallowing to those helpers; the isolation boundary belongs at the
  call sites named above, not inside the low-level I/O.
- **`detectEvents`' event order is bead-id sorted, not TS's Object.entries
  insertion order** — Go map iteration has no ordering guarantee (unlike a JS
  object's insertion-order iteration), and no test or caller depends on
  event order, so this is a deliberate, faithful-in-substance divergence, not
  a bug.

`cursor.go`'s `stateDir()` (shared by the poll cursor and the tailer's
offset file) reuses `internal/config.StateHome()` rather than reimplementing
the `PARLAY_STATE_HOME` fallback — unlike `guard.go`'s deliberately-duplicated
`guardStateHome()`, there is no TS-side inconsistency to preserve here:
`cursor.ts`'s `stateDir()` and `config.ts`'s `serverUrl()`-adjacent state-home
logic already agree.

**`commands-robots-watch/subscribe.ts` was never ported — correctly, it turns
out.** Ticket B10's TS-vs-Go test-coverage audit flagged
`subscribe.test.ts` (covering `isGuardBead`/`originatingAgent`/
`subscribeLabel`/`subscribeOnCreate`) as having no Go counterpart in
`internal/robotswatch`. Tracing it further: `subscribe.ts`'s exports are not
imported anywhere in `packages/cli/src` — not `index.ts`, not `handlers.ts`
(which implements the DELIVER/close-time half using `detect.ts`'s
`notifyChannels`, ported faithfully above) — nor anywhere else in this repo
(`grep -rl subscribeOnCreate` outside its own file/test comes up empty). It's
dead code in the TS CLI itself: pure SUBSCRIBE-on-CREATE helpers documented
as part of decision-4zr/robots-3q7n, written for some bead-creation call site
that was never wired up (in this CLI or elsewhere in the repo as of this
writing). Since it corresponds to no reachable dispatch path in either CLI,
there is nothing for `tools/cli` to port parity-wise — porting unreachable
logic just to satisfy a test-file-name checklist would be scope creep with no
behavioral referent. Left unported deliberately; if `subscribe.ts` ever gets
wired to a real call site, port it then and this note becomes obsolete.

## Go CLI ticket B9: `launch`/`drawdown`/`idle`

`tools/cli/internal/commands/{launch,drawdown,idle}.go` port `cmdLaunch`/
`cmdDrawdown`/`cmdIdle` from `packages/cli/src/commands/{launch,drawdown,
idle}.ts` (that source has since been split from the older single
`commands.ts` file some ticket briefs still cite — read the actual
`packages/cli/src/commands/*.ts` files directly, not `commands.ts`). Two
things worth knowing before touching this code:

- **`launch` resolves its spawner at runtime — never hardcode one binary
  name (robots-v81b)**. Ticket A1 renamed `bin/parlay-spawn` (bash) to
  `tools/parlay-bin` (Go, `spawn`/`reset` subcommands), and `launch.ts`
  followed the new name while `commands-variant.ts` kept the old one. That
  divergence was documented here as harmless; it was not. **`parlay-bin` is
  built by no install path in this repo** — `bin/parlay` builds `tools/cli`
  only — so on the captain's box, where `~/.local/bin` carries the
  `bin/parlay-spawn` symlink and nothing named `parlay-bin`, every `parlay
  launch <id>` exec'd a nonexistent binary. Compounding it, both CLIs
  discarded the spawn result (`_ = cmd.Run()` / an unchecked
  `Bun.spawnSync`), so ENOENT was indistinguishable from success: an
  announcement, exit 0, and no agent. Both now walk `parlay-bin` (with the
  `spawn` subcommand) then `parlay-spawn` (bare positionals), take the first
  on PATH, die loudly when neither resolves, and treat a non-zero spawner
  exit as a failed launch. The two names are still both live on purpose —
  the resolution order is the contract, not either binary. `variant.go`
  still calls `parlay-spawn` directly; it is the one that always existed.
- **`launch.go`'s `knownAgents()` reuses `guard.go`'s
  `readLocalFrontmatter`/`parlayAgentsDir`/`parlayHomeDir`** instead of a
  fourth copy of the local frontmatter parser — `launch.ts` defines its own
  `parseFrontmatter` with the identical regex pair
  (`^---\n([\s\S]*?)\n---` block, `^(\w+):\s*"?([^"]*)"?\s*$` KV) already
  duplicated by `commands-teardown.ts`/`commands-variant.ts` and already
  consolidated once in `guard.go` for the Go port (see the B4 section
  above) — same hardcoded non-env-aware `~/.parlay/agents` resolution, so
  the existing helpers apply directly.

## `bun test` only works from inside a package directory

There is no root `bunfig.toml`, so running `bun test` from the repo root
does not preload `@happy-dom/global-registrator` for packages whose tests
touch `document`/`window` (e.g. `packages/client`, `packages/input`) — those
tests fail with `ReferenceError: document is not defined` at the root even
though they pass cleanly run from their own directory (`cd packages/X && bun
test`). This predates any one package; always run a package's tests from
inside that package when validating.

## Publishable packages use flat, unscoped `parlay-<part>` names — the `@parlay` scope is never published

This section covers npm packages under `packages/` (those with a
`package.json`); Go modules like `packages/go-server` (no `package.json`)
are outside this private/publishable dichotomy entirely. The naming rule is
fixed: anything meant for npm uses a **flat, unscoped** `parlay-<part>` name,
and the `@parlay` scope is **never** published (the scope is unclaimed and
nothing should depend on claiming it — a real publish attempt under it returned
404). Every npm package under `packages/`/`tools/` now uses that flat scheme:
`parlay-cli` (`packages/cli`), `parlay-client` (`packages/client`),
`parlay-server` (`packages/server`), and `parlay-split` (`tools/split-test`,
whose package name now matches its `parlay-split` bin). Those four stay
`private: true` — the rename is repo-wide naming hygiene, not a decision to
publish them; publishing remains the captain's separate call. The `@parlay`
scope no longer appears in any manifest, import, config, or changeset.

The single publishable package today is `packages/input`, named
`parlay-input`. It is self-contained — a real client for parlay's REST + shared-SSE
input protocol, built directly from its own `src/index.ts`, declaring no
runtime dependencies — and configured for npm publishing (`publishConfig:
{ access: "public" }`, `.changeset/config.json` `access: "public"`, MIT
`LICENSE` on disk). The earlier two-package split (a private `@parlay/input`
plus a `packages/parlay-input` shim re-exporting it) is **gone**: `@parlay/input`
cannot be published (unclaimed scope), so it was collapsed into this one
package and the shim deleted. `parlay-input@0.1.0` was the broken shim; the
self-contained rewrite is `0.2.0`. Every other npm package under `packages/`
is `private: true`. Neither `bun build` nor
`tsc` alone produces a complete publishable output for a TS package here:
`bun build.ts` emits the JS bundle to `dist/`, and a package-local
`tsc --emitDeclarationOnly` (via a package-local `tsconfig.json`) emits the
`.d.ts` — `bun run build` runs both. `packages/server` was evaluated and is
not reasonably publishable: it's a `bun serve()` application with side
effects on import (opens a port, touches disk), not an importable library
(see the standalone-server section above) — it remains `private: true`.

`packages/client` also gained `main`/`module`/`exports`/`types` fields and a
`dist/index.js` build target (`src/lib.ts`), but stays `private: true` — this
is a same-monorepo/workspace library entry point for external host apps (e.g.
herdr-web) importing the server-eval dispatcher, not npm publishing prep.

## Go CLI ticket B10: coverage/parity close-out — `bin/parlay` now execs the Go binary

B10 was workstream B's closing ticket, with three parts: close Go test-
coverage gaps against `packages/cli/src/*.test.ts`, build a TS-vs-Go parity
harness and fix whatever it finds, and wire `bin/parlay` to the Go binary.

`bin/parlay` (the one shared entry point every crewmate and the captain use)
now builds and execs `tools/cli`'s Go binary instead of `bun
packages/cli/src/index.ts` — see the script itself for the symlink-safe
`$REPO` resolution and the build-if-stale check (`find tools/cli -name
'*.go' -newer "$GO_BIN"`). One exception: `parlay lavish-import` still
routes to the TS CLI — `packages/cli/src/index.ts` has a real `case
"lavish-import"` (`cmdLavishImport`, in `lavish-import.ts`) and the Go help
text in `internal/help/help.go` already documents the subcommand, but
`tools/cli/main.go`'s dispatch switch never grew a matching `case` in any
prior ticket. `bin/parlay` special-cases that one verb back to `bun
packages/cli/src/index.ts` rather than silently 404ing; porting
`lavish-import.ts` to Go is open follow-up work, not done in B10.

An original TS-vs-Go parity harness (no `packages/go-server` C5 harness
existed yet to base this on) lives at `tools/cli/parity/run.sh`: it builds a
disposable `packages/go-server` fixture instance plus the Go CLI, redirects
`$HOME` to a scratch dir for both CLIs (which also safely scopes the
hardcoded, non-`$PARLAY_STATE_HOME`-aware `~/.parlay/agents|worktrees` paths
used by guard/teardown/variant/launch — see the B4/B9 notes above), runs the
full representative command surface through both `bun
packages/cli/src/index.ts` and the built Go binary, and diffs normalized
stdout/stderr/exit code. Run it with `tools/cli/parity/run.sh [-v]`; on any
FAIL it copies the raw diffs to `tools/cli/parity/last-diffs.log`
(gitignored scratch output, not the source of truth — re-run the harness for
current results). Three real Go-side bugs were caught and fixed this way:

- `internal/httpc`'s `GetJSON`/`PostJSON` die-message duplicated the HTTP
  status code (Go's `resp.Status` already contains it, unlike TS's
  `res.status`/`res.statusText` pair printed separately).
- `main.go`'s `cmdHelp` had grown a `parlay help <cmd>` per-subcommand
  lookup with no TS equivalent (TS's `case "help"` always prints the full
  `USAGE` regardless of trailing args) and was reverted to match.
- `internal/commands/doctor.go`'s presence-status fallback printed `presence:
  ` (empty) instead of `presence: unknown`: `packages/go-server`'s
  `/api/chat/subscribers` never sends a `status` key on presence entries at
  all (see `internal/handlers/registry.go`'s `subscribersPresenceEntry` —
  only `channel`/`lastSeen`), so Go's `pres.Status` zero-values to `""`
  where TS's `pres?.status ?? "unknown"` sees a missing property and falls
  back correctly. Fixed by also checking `pres.Status != ""` before using it;
  regression test: `TestDoctorReportsUnknownWhenPresenceEntryHasNoStatus`.

**A Go-only verb is not free just because it has no `check` case
(robots-xaxt).** `parlay help` prints the whole usage block, so every verb the
Go CLI documents and TS does not shows up as a diff on all four `help` cases —
`claim`, `merge-gate`, `branch-audit` and `sweep` between them turned a CLI
with no defect into a 4-FAIL harness. `run.sh`'s `GO_ONLY_VERBS` array is the
registry: its usage lines are filtered out of the **Go** side of the diff only,
so a verb that merely got forgotten on the TS side still fails normally.
`audit_go_only_verbs` keeps the list from rotting into a blanket mute — per
verb it asserts the line is still in Go's usage (else the entry is stale) and
still absent from TS's (else the verb gained a TS side and belongs in the
ordinary check list), reporting each as its own `GO-ONLY`/`FAIL` summary row.
**Adding a Go-only verb means adding it here too**, in the same change that
adds the verb.

Any new harness case must first check whether the command under test honors
`PARLAY_AGENT_HOME`/`PARLAY_STATE_HOME`, or — like `commands-status.ts`'s
`statusSink()` and its Go port — hardcodes `homedir()/.parlay/agents/<id>`
and only respects the narrower `PARLAY_STATUS_FILE` override; getting this
wrong writes real files under the live `~/.parlay` rather than the harness's
temp dirs (a `~/.parlay/agents/status-agent/` pollution incident happened
once during B10 itself, before the `$HOME`-redirection approach settled, and
had to be manually cleaned up — confirmed gone as of this note).

## `treehouse` picks its pool from the PROCESS cwd — always pin it (robots-d04t)

`treehouse get --lease` has no `--repo`/`--path` flag: it resolves which repo's
worktree pool to lease from by walking up from the process's current directory.
Any code that shells out to it while targeting a repo *other* than its own cwd
must set the child's cwd explicitly — `(cd "$PROJECT_PATH" && treehouse …)` in
bash, `cmd.Dir = projectPath` in Go. `parlay-spawn --worktree --cwd <other-repo>`
did not, so spawning from inside a firstmate worktree leased a *firstmate*
worktree and launched the agent in the wrong repository, with nothing in the
output flagging it.

The durable guard, in both `bin/parlay-spawn` (`repo_identity`) and
`tools/parlay-bin/worktree.go` (`repoIdentity`): a worktree's repo identity is
`git rev-parse --path-format=absolute --git-common-dir`, symlink-resolved. Every
linked worktree of a repo shares it with the primary checkout and no two repos
share it, so it is the right key for "is this actually a worktree of that repo?"
— `--show-toplevel` is not (it differs per worktree). Both spawn paths reject a
wrong-repo treehouse lease (falling back to plain `git worktree`) and hard-abort
before launch if the final worktree's identity does not match `--cwd`'s.
Regression coverage: `bin/parlay-spawn.worktree.test.sh` (a `curl`/`herdr`/
`treehouse`-stubbed, `$HOME`-redirected harness that actually reaches step 2c —
unlike `parlay-spawn.batch.test.sh`, which dies at the dead-server POST by
design) and `tools/parlay-bin/worktree_test.go`.

## `treehouse get` RESETS the slot it hands out — guard the pool first (robots-n8d9)

`treehouse get --lease` does not just *pick* a free slot, it **checks
origin/main out over whatever branch that slot held**, at acquire time. Its
eligibility rules are dirty / attributable-processes / already-leased and
nothing else — so a clean slot holding a live agent's work looks free, and one
spawn detached a running agent's branch out from under it. Detecting this
afterwards is useless; the checkout has already happened.

`bin/parlay-treehouse-guard` is the prevention, called by both spawn paths
immediately before `treehouse get` (`bin/parlay-spawn`, and
`guardTreehousePool` in `tools/parlay-bin/worktree.go`). It writes a
protective lease — `lease_holder: "parlay-guard:<reason>"` — into the pool's
`treehouse-state.json` for every slot that is still occupied, which treehouse
does honor (verified against the real binary: it takes another slot, or
creates a new one, rather than reclaiming a protected one). Three reasons:
`dirty`, `unlanded` (commits no remote has), and `live-agent` (some
`state/*.meta` under **any** firstmate home on the box records that path as
`worktree=` and `fm_backend_agent_alive` does not say `dead` — scanning only
the spawner's own home is the blind spot that caused the bug). Guard leases
are released on the next sweep once their reason lapses; another holder's
lease is never touched. It is best effort by design — a missing or failing
guard warns and lets the spawn proceed.

Two sharp edges for anything else that writes that state file:

- **`leased_at` must be strict RFC3339** — treehouse parses it with Go's
  `time.RFC3339`, which rejects `date +%z`'s `-0700`. A malformed stamp makes
  treehouse declare the whole file corrupt and "recover" by marking **every**
  slot leased, taking the entire pool out of service. Use
  `date -u +%Y-%m-%dT%H:%M:%SZ`.
- **Never protect on a signal the repo cannot answer.** The `unlanded` check
  selects its comparison scope (`--remotes=origin`, then any remote, else skip)
  — without that, a remote-less checkout reads as ahead on every slot and the
  guard permanently starves the pool.

Coverage: `bin/parlay-treehouse-guard.test.sh` (real repos, real origin, real
state file — every assertion reads what the guard actually wrote) and
`TestSetupWorktreeGuardsPoolBeforeLeasing` in
`tools/parlay-bin/worktree_test.go` (pins guard-before-`get` ordering and the
guard's cwd).

## Go server ticket C6: `parlay-server` launchd deploy tooling (`packages/go-server/deploy`)

`packages/go-server/deploy/{install,uninstall,ensure-up}.sh` + `lib.sh` +
`com.parlay.go-server.plist.template` give `parlay-server` (the C0–C3 Go
rewrite of `packages/server`) the same always-on macOS LaunchAgent
supervision as `tools/relay/deploy/`, which this ticket used as the
authoritative house-style reference (build-if-missing, atomic binary swap,
`sed`-rendered plist validated with `plutil -lint`, `launchctl
bootstrap/bootout/enable/kickstart -k` in `gui/<uid>`). Own label
(`com.parlay.go-server`, distinct from `com.parlay.relay`/
`com.parlay.eval-engine`), own paths under `~/Library/Application Support/
parlay/bin/` (shares the directory with the relay's binary but never
`rm`/trashes anything but its own two files there), default addr
`127.0.0.1:4242` (matches `main.go`'s coded default) with a hard refusal —
belt-and-suspenders with `main.go`'s own `refuseProductionPort` — of
`:31337`, the captain's live production Pulse instance.

**`uninstall.sh` never permanently deletes anything — every removal goes
through `parlay_goserver_trash_put` in `lib.sh`** (prefers a real `trash` CLI
— PATH, then Homebrew's keg-only install paths, e.g. `brew install trash`;
falls back to a manual move into `~/.Trash`), never `rm -rf`/`rm -f`. This is
not a stylistic choice: an earlier version of this script used plain `rm -rf`
for `--purge`, and — combined with a second bug where `uninstall.sh` had no
memory of `install.sh --state-dir`'s override and fell back to the coded
default — a smoke test's `uninstall.sh --purge` **permanently deleted the
live `~/.parlay` directory on the host** (other concurrent agents' registered
state under `~/.parlay/{agents,worktrees}` included), outside the smoke
test's own intended sandbox. Both root causes are fixed: all deletion is
trash-based (recoverable), and `--purge`'s state-dir target is resolved by
reading the real installed `-state-dir` value back out of the rendered
plist's `ProgramArguments` via `/usr/libexec/PlistBuddy`
(`parlay_goserver_installed_state_dir` in `lib.sh`) — the plist is the only
durable record of what `install.sh --state-dir`/`PARLAY_STATE_HOME` actually
resolved to at install time, so this must be read *before* the plist itself
is trashed. Any future deploy script in this repo that supports a `--purge`-
style destructive path should follow this same pattern (trash, never `rm`;
resolve real installed config from the live plist, never assume the coded
default) rather than re-deriving it from scratch.

## A best-effort probe written as `VAR=$(cmd)` is not best-effort (robots-dcag)

Every shell script here runs `set -euo pipefail`, and under it a **plain
assignment takes the exit status of its command substitution** — so
`VAR=$(curl … | sed …)` aborts the script the moment curl fails, no matter how
optional the value is. `2>/dev/null` hides curl's complaint, so the script dies
producing nothing. `parlay-monitor.sh`'s cross-server probe was written this
way: a `--max-time 2` timeout (exit 28) killed the monitor three lines before
its first "enrolling" message. Because `parlay listen` registers and announces
with Pulse *before* shelling out, the agent sat in the panel looking healthy
with no event stream at all — **registered-but-deaf**, taking no directives for
the rest of the session.

Two rules this leaves behind:

- **Consume the status explicitly** — `VAR="$(cmd)" || { …; VAR=""; }`, or call
  a helper that returns 0 on every path (`parlay_relay_reported_server` in
  `tools/relay/deploy/lib.sh` is the model: `|| return 0`, empty result means
  "unknown"). A caller must never be able to die on an optional probe.
- **Never let setup fail silently.** `parlay-monitor.sh` traps EXIT until it
  reaches `tail` and prints the exit code plus the registered-but-deaf
  consequence, so a dead stream can never look like a quiet one.

Timeouts must also be sized against the real endpoint: `/health` answers from a
socket bound before any work, but `/agents` serializes the whole registry and
grows with the fleet (>2s at 269 agents). The probe bound is
`$PARLAY_RELAY_PROBE_TIMEOUT` (default 15s), deliberately separate from
`/health`'s 2s. Regression coverage: section C of
`tools/monitor/parlay-monitor.test.sh`, whose stub relay can stall `/agents` on
demand — and which also pins that tolerating an *unknown* answer never softened
the robots-buu8 refusal into a no-op.

## The listen stream is supervised — never make an agent's reply channel one process (robots-gv6t)

Same registered-but-deaf outcome as robots-dcag, reached from the other end.
`parlay listen` registers and posts its "listening — monitor armed" announce
*before* it shells out, and the stream after that used to be terminal on both
sides: `exec tail -F` in `parlay-monitor.sh`, one `cmd.Run()` + `os.Exit(child's
code)` in `tools/cli/internal/monitor`. Whatever ended that single process ended
the channel, and it said so only on stderr — which a harness Monitor tool never
reads. The panel kept showing a ready agent receiving nothing. (The reported
symptom was exit 144 = 128+SIGURG; the killer was never identified and does not
need to be. Note `exec.ExitError.ExitCode()` answers **-1**, not 144, for a
signalled child — read `WaitStatus.Signal()` if you want to name it.)

Three rules this leaves behind for anything supervising a stream here:

- **Respawn, don't propagate.** The script restarts `tail`; the Go side restarts
  the script on any unexplained exit and treats only the script's own deliberate
  refusals (`EXIT_USAGE`/`EXIT_RUNTIME`) as terminal. Both bound the retries
  (`$PARLAY_MONITOR_MIN_UPTIME`, `$PARLAY_MONITOR_MAX_RESTARTS`) and then give up
  loudly rather than spinning.
- **Resume where delivery stopped, not where the file is now.** A counting stage
  after `tail` reports the bytes it actually forwarded, and the next `tail -c +N`
  starts there. Re-reading the spool's *current size* at restart would silently
  swallow everything spooled during the gap; `-n0` would swallow more.
- **Report on stdout.** stderr is invisible to the agent whose channel just
  dropped. Stream events are `MONITOR|<kind>|<text>` lines, deliberately distinct
  from the relay's `CHAT_MSG|`, and a give-up additionally posts "monitor DOWN"
  to the agent's own channel — the registry has no listening flag to clear, so
  the channel message is the only way to retract the announce.

Regression coverage: section D of `tools/monitor/parlay-monitor.test.sh` (kills
`tail` mid-stream, then asserts recovery, gap delivery, no duplicates, and a
bounded loud give-up) and `tools/cli/internal/monitor/supervise_test.go`.

## "Not answering /health" ≠ "not running" — never force-restart a relay (robots-mpr3)

## The relay is a per-runtime-dir singleton bound to ONE server — `PARLAY_SERVER` alone does not scope it (robots-buu8)

Setting `PARLAY_SERVER` does **not** by itself keep a sandbox off production.
`parlay listen` shells out to `tools/monitor/parlay-monitor.sh`, which enrolls
over a relay's Unix control socket — and a relay process is a singleton per
runtime dir, bound to whatever upstream server it was started with. On the
captain's box the shared `$TMPDIR/parlay` relay is bound to production `:31337`,
so enrolling there registered the agent in the captain's **live** registry no
matter what `PARLAY_SERVER` said. Nothing hardcodes `31337` in the monitor or
CLI; the leak was entirely via the shared daemon.

The fix (`tools/relay/deploy/lib.sh`, `ensure-up.sh`, `parlay-monitor.sh`):
the canonical runtime dir is reserved for the default server, any other
`PARLAY_SERVER` gets `<canonical>/srv-<hash>` and its own relay, and the monitor
reads the relay's own `GET /agents` → `server` and refuses to `POST /register`
on a mismatch. Anything else in this repo that talks to the relay must reason
about *which relay*, not just which `PARLAY_SERVER` — see
`tools/monitor/NOTES.md` § Upstream-server scoping, and
`tools/monitor/parlay-monitor.test.sh` for the reproduction.

**Unix socket paths are capped at 104 bytes (`sun_path`), and macOS's `$TMPDIR`
eats ~53 of them.** `/var/folders/xx/<28 chars>/T/parlay/relay.sock` leaves very
little room — this is why scoped runtime dirs are named `srv-<10 hex>` and not
something readable. Over the cap, `bind()` fails with `invalid argument`, which
names neither the limit nor the path. Any new path under the relay runtime dir
must budget against this; `parlay_relay_sock_path_ok` in `lib.sh` is the check.


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

## The canonical runtime dir is RESERVED — a wrong-server relay in it is a fleet outage (robots-93xu)

Scoping (robots-buu8 above) keeps non-default servers *out* of the canonical dir.
Nothing kept a non-default relay from **occupying** it. `install.sh` defaulted
`--server` from an ambient `$PARLAY_SERVER`, so one install run from a shell that
happened to export `http://localhost:4242` baked that into the LaunchAgent —
which is a fixed singleton on the canonical dir. Every default-server agent on
the box then resolved to that dir, found `:4242`, and was refused by the
pre-enroll guard. A fleet-wide enrollment outage, persistent across reboots,
whose only symptom was agents failing to start.

Three rules this leaves behind:

- **Never let an ambient env var configure an installed singleton.** `install.sh`
  now refuses any server other than the default unless `--allow-non-default-server`
  is passed, and says so louder when the value came from the environment rather
  than the flag. A non-default server needs no install at all — `ensure-up.sh`
  starts a scoped relay for it on demand.
- **A liveness probe is not a correctness probe.** `/health` says a relay
  answers, not which server it serves. ensure-up's fast path returned 0 on
  `/health` alone — a false green that handed the caller a success line and let
  it die one step later. It now compares the relay's `GET /agents` → `server`
  against the wanted one and exits **3**, distinct from 1 ("no relay could be
  started"), because the two need opposite responses. It never restarts the relay
  (robots-mpr3); `parlay-monitor.sh` recognizes 3 and defers to ensure-up's
  message instead of contradicting it with "install the relay".
- **Failure advice must fit the case that actually happens.** "Unset
  `PARLAY_RELAY_RUNTIME`/`PARLAY_RELAY_SOCK`" was a dead end here — neither was
  set, which is precisely why the resolution rule picked the squatted dir. With
  no override set the monitor now names the squatting relay as the fault and
  prints the `install.sh --server …` repair.

`parlay_relay_installed_plist_server` was fixed en route: **PlistBuddy prints its
errors on stdout** and can still exit 0, so an unreadable plist came back looking
like a server URL. Any PlistBuddy capture in this repo must validate the shape of
what it got. Pinned by `tools/relay/deploy/install.test.sh` and cases 7–8 of
`ensure-up.test.sh`.

## Never merge on a green check alone — run `parlay merge-gate <pr>` (robots-jap6)

A green status check in this repo is **not** evidence that anything reviewed
the code. CodeRabbit is the only *review* check — the CI jobs described in the
`.github/workflows/ci.yml` section below build and test, they do not review —
and it lies in two known ways: it reports the check CONCLUSION `pass`
when it never ran (the account-wide PR review limit; only the free-text
*description* says "Review rate limited"), and it reports success regardless
of how many findings it posted. `gh pr view` compounds it with
`mergeStateStatus=CLEAN`, `mergeable=MERGEABLE`, `reviews=0`. PRs #43 and #46
both landed completely unreviewed this way.

`parlay merge-gate <pr> [--repo owner/name] [--json]`
(`tools/cli/internal/commands/merge_gate.go`) is the truthful replacement. It
refuses to treat the conclusion as the merge signal: it reads each check's
*description* for a vacuous pass, requires an actual review (a human review,
or a CodeRabbit comment carrying `walkthrough_start` rather than the
rate-limit template), requires that review to name the **current head sha** —
CodeRabbit edits one comment in place, so `createdAt` can never detect a
stale review, but the body always prints the exact `base..head` range it
processed — and counts unresolved review threads via GraphQL, since
`gh pr view` has no field for thread resolution. Exit codes are fail-closed
in every direction: `0` ready/already-merged, `3` blocked on the code, `5`
review still pending, `4` needs-decision, `1` gh could not answer, `2` usage.
The mechanic contract in `claim.go`'s robots DoD now sends every merge
decision through it.

**Exit 4 is the bounded answer for "the reviewer is unavailable"
(robots-8kkq).** Non-zero alone was not enough: "a test is failing" and
"CodeRabbit is rate limited" are both blocked, but only the first is fixable
on the branch, so a mechanic told just "blocked" polls a rate limit forever —
`@coderabbitai review` recovered one PR once and then stayed limited across
three further attempts over ~40 minutes, and trillium/no-mistakes#7's
follow-up commit merged unreviewed as a result. Every blocker now carries a
`Class` (`code` / `pending` / `reviewer-unavailable`); when *every* blocker is
reviewer-unavailability the verdict is `NeedsDecision` and exit `4`, and the
notes name the only two honest options — merge-and-disclose or park — for the
captain to pick. One code-class blocker among them keeps the whole verdict at
`3`, so the downgrade can never launder a failing test into "the captain's
call".

**Exit 5 is "the review has not finished yet" (robots-rwf8).** A *pending*
check was landing in `3` — the code the mechanic contract documents as
"blocked on the CODE, fix it on the branch" — even though the check had said
nothing about the diff. Observed on trillium/no-mistakes#11: `check-pending` +
`no-review-evidence`, exit 3, and the same unchanged PR exited 0 minutes
later. An agent obeying the documented contract goes editing a branch with no
defect, and the new push restarts the review it was waiting on. "Not yet" is
neither "the code is wrong" (3) nor "the reviewer will never come" (4).
`check-pending` is `pending`-class, and while a check is pending so are
`no-review-evidence` and `stale-review` — a running check *is* the explanation
for both, which is the one thing the gate normally cannot infer. Class
precedence is **code > pending > reviewer-unavailable**: a real finding always
wins, and pending outranks needs-decision because escalating to the captain
while a review is mid-flight asks for a decision on information that is about
to arrive. Anything whose class is unset counts as code, so a forgotten class
can never become a downgrade.

A stale review is normally code-class (push again, the reviewer catches up) —
but a stale review sitting next to a *live* rate-limit template is
reviewer-unavailability, because the re-review is exactly what is being
refused. That pairing is the no-mistakes#7 shape. A live refusal outranks an
unfinished check: that reviewer has already answered, and the answer was no.

**A refusal counts wherever it is written down, and waiting never clears one
(robots-eowy).** CodeRabbit edits its ONE comment in place, so a PR whose first
push got a real review keeps that walkthrough body forever — and when a later
push is refused, the refusal exists only in the check *description*.
Classifying off the comment alone made that shape (`vacuous-pass` +
`stale-review`, trillium/no-mistakes#13) exit `3`, which sends a mechanic
hunting a defect in code no reviewer ever objected to, and every edit it pushes
restarts the review and re-consumes the limit that is blocking it. A vacuous
check now reclassifies `stale-review` **and** `no-review-evidence` exactly as a
rate-limit comment does; `no-review-evidence` was only kept code-class because
the gate could not tell WHY nothing reviewed the PR, and a check that states
the reason is that knowledge. A *green* check still explains nothing, so an
unexplained missing review keeps the harsher code.

And exit 4 now names the way out: CodeRabbit does **not** re-review when the
rate-limit window lapses — it reviews only on a new push or an explicit
`@coderabbitai review` comment — so "wait and re-run the gate" deadlocks
forever. The notes give the captain three options (re-request /
merge-and-disclose / park) instead of two. The gate deliberately does not post
that comment itself: it is a read-only verb, and a gate called in a poll loop
would spam the reviewer and re-consume the very limit at issue.

**Never let `gh` pick the repository implicitly (robots-g4qz).** gh's
base-repo resolution prefers a remote named `upstream` over `origin`, so in a
fork clone — origin=`trillium/<repo>` plus an `upstream` remote, which is every
clone the fleet works in — a bare `gh pr view N` reads the *upstream* project's
PR #N. The numbers collide freely and the failure is silent: a well-formed
verdict about somebody else's pull request, worst case exit 0 "already MERGED"
for a fork PR that is still open and unreviewed. `resolveMergeGateRepo` now
resolves once (explicit `--repo` > `origin` remote > gh's pick, and only with no
usable origin) and passes that one answer to every gh call, and every verdict
prints the repo it answered about. Any new code here that shells out to `gh`
against a PR must pass `--repo` explicitly for the same reason.

Decision logic lives in the pure `ComputeMergeGate(MergeGateSnapshot)` so
`merge_gate_test.go` pins the regressions with no gh binary and no network;
`fetchMergeGateSnapshot` is the only part that shells out. **Go-only, no TS
port** — `bin/parlay` execs the Go binary for everything except
`lavish-import`, so the verb is reachable everywhere, and `packages/cli` is
the retired path. Do not add a `check` case for it to
`tools/cli/parity/run.sh`; there is no TS side to diff against. Do add it to
that script's `GO_ONLY_VERBS` list — see the B10 parity-harness section above.

## `git diff origin/main <branch>` is not a question about the branch — use `parlay branch-audit` (robots-d988)

Two-dot diff renders the **symmetric** difference between two tips. Every file
that exists only on `origin/main` therefore comes back as a `D` (deleted) line,
and every line main gained since the branch was cut comes back as a `-`. A
branch that is merely N commits behind reads as having deleted work it never
touched — and the report is well-formed and specific, which is what makes it
convincing.

On `~/code/firstmate` (robots-90i7) a 16-commits-behind branch produced "75
files changed, 2990 deletions" and named four files it had "deleted" from two
already-merged PRs. None of the four existed at the branch's merge-base; all
four landed on main *afterward*. The real contribution was 21 files, all
additions, 1214 insertions, **zero** deletions. The false positive escalated to
"do NOT merge, captain decision required, consider discarding the branch and
redoing the work" — that second option would have thrown away sound work over a
diff direction.

`parlay branch-audit [<branch>] [--base <ref>] [--repo <path>] [--json]`
(`tools/cli/internal/commands/branch_audit.go`) never diffs tip against tip. It
reports three things separately:

- **true contribution** — `git diff <merge-base> <branch>`
  (`~/code/firstmate/bin/fm-review-diff.sh` already gets this right, via the
  equivalent `<base>...<branch>`);
- **staleness** — "N commits behind" on its own non-alarming line. Being behind
  removes nothing, so it is never a deletion and never makes the verb non-zero;
- **merge strips** — every merge in `<base>..<branch>` diffed against its **own
  parents**, the only honest test for a merge, since combining two histories is
  that commit's entire job. A file a parent had ADDED that the merge dropped is
  a real strip (exit 3, the union-merge shape). A file absent from the merge
  that predates both parents was deleted deliberately on one side: ordinary
  resolution, only a note.

Exit 0 covers a badly-behind branch **and** files the branch's own commits
delete — deleting a file is ordinary work, and a verb that blocked on it would
teach the fleet to ignore it. Whenever the classifier cannot answer (no
resolvable ancestor) it fails toward "not a strip": a false "this branch
reverted merged work" is the defect, so that is the safe direction here.
Line-level reverts inside a file a merge *modified* rather than deleted are
deliberately out of scope — that needs semantic review, and claiming to catch it
here would be the same overreach this verb removes. It is listed in `run.sh`'s
`GO_ONLY_VERBS` for the help-diff reason described in the B10 section.

Policy lives in the pure `ComputeBranchAudit(BranchAuditSnapshot)`, but the
shape tests in `branch_audit_test.go` build **real throwaway git repositories**:
the defect is about diff direction, which a hand-built snapshot cannot express.
`TestStaleBranchIsNotAReversion` reproduces robots-90i7 and asserts the two-dot
artifact still exists as its own precondition. One sharp edge that test hit:
give every fixture file distinct content, or git's rename detection pairs a
main-only file with a branch-only one and silently drops it from
`--diff-filter=D`. The mechanic contract in `claim.go`'s robots DoD now forbids
reporting a reversion off a tip-vs-tip diff and routes the question here.
**Go-only, no TS port** — same reasoning as `merge-gate` above; no `check`
case in `tools/cli/parity/run.sh`.

## Finished agents are only collected by `parlay sweep` — firstmate can never see them (robots-6xq7)

Parlay-spawned agents are **structurally invisible** to firstmate's idle>2h
auto-close: every firstmate shutdown path enumerates sessions via
`$STATE/*.meta`, and a parlay agent has no `.meta` file. Nothing else closed
them either — `crew-state` reports a terminal state, `teardown` does the safe
destroy, `supervise` is per-agent wake-on-status — so finished agents posted
`done` and waited forever (38 stale panes against 2 live orchestrators).

`parlay sweep [--apply] [--agent <id>] [--all] [--force] [--interval <sec>]
[--verbose]` (`tools/cli/internal/commands/sweep.go`) is the missing
collector: it walks every store under `~/.parlay/agents`, asks `crew-state`
for each one, and tears down the provably-finished through
`teardownAgent` — the same chain `parlay teardown` uses, factored out of
`Teardown` so a refusal (uncommitted work, unlanded commits) surfaces as an
error the sweep reports and steps over instead of an `os.Exit`. Default is a
dry run; `--apply` acts. Policy lives in the pure `ClassifySweep`, tested
with no filesystem in `sweep_test.go`. **Go-only, no TS port** — same
reasoning as `merge-gate` above; no `check` case in
`tools/cli/parity/run.sh`, but it is in that script's `GO_ONLY_VERBS`.

**Teardown closes the herdr surface too, and that is the whole point
(robots-iz9o).** The first version of the sweep unregistered, removed the
worktree and deleted the store — and left the pane running. It printed
`closed` for a fleet that was entirely still alive, and 57 panes had to be
walked by hand with `herdr tab close` afterwards. `teardownAgent` now ends by
calling `closeHerdrSurface` (`tools/cli/internal/commands/herdr.go`), so both
`sweep --apply` and a direct `parlay teardown` reclaim the terminal.

The lookup key on both sides is the parlay agent id, because `tools/parlay-bin`
spawns with `herdr agent start <id>` and `herdr tab create --label <id>`. Both
lookups are needed: a live agent resolves through `herdr agent get`, while an
agent whose process already exited has no herdr agent at all and is findable
only by its lingering labelled tab — that residue is what fills `herdr tab
list` with dead `mc-*` tabs. Two rules hold the blast radius: a tab reporting
`pane_count > 1` is shared, so only the agent's own pane is closed (`herdr tab
close` would take the bystanders with it), and the *calling* agent's surface is
never closed, so `parlay teardown $SELF` cannot kill the pane mid-command. All
of it is best-effort like `bestEffortUnregister` — no herdr on PATH, no daemon
or an unparseable reply must never block the git safety checks or the store
delete. Note `herdr` exits 0 even when it prints an `error` object, so the
reply body is the only usable signal.

Four things are never swept, and each guard exists because of a real way this
could destroy work: the sweeping agent itself; ids listed in
`$PARLAY_STATE_HOME/sweep-keep` (one id per line, `#` comments — where
long-lived dispatchers go); `needs-decision`/`blocked`/`failed`, which are
*held for the captain* because absorbing them destroys the state he needs to
read; and any agent whose `identity.md` has **no frontmatter**, held even
under `--all` (`--force` is the deliberate override).

That last guard is the important one. `--worktree`/`--project` had been
dropped from `MemValueFlags` and from `--register`'s meta-field loop during
the Go port, and `args.Parse` dies with `EXIT_USAGE` on an unknown flag
(`args.go:89`) — so
every `parlay identity --register … --worktree <path> --project <path>` that
`parlay-spawn` issues for a worktree agent exited 2 and wrote no frontmatter
at all, with `registerIdentity`'s `_ = cmd.Run()` swallowing the code. The
agent launched looking fine with an empty launch spec, and `parlay teardown`
then read no worktree, deleted the store, and orphaned the worktree plus any
unpushed commits **without ever reaching its git checks** — teardown only
checks a *recorded* worktree. Both flags are restored and pinned by
`TestRegisterRecordsWorktreeAndProject`, but stores registered before that
fix are still empty on disk, which is exactly what the hold protects. When
adding a flag to a Go-ported command, diff its table against the TS source's
(`packages/cli/src/commands-identity/store.ts` here); a dropped flag is not a
degraded flag, it is a hard exit that callers may be discarding.

Two follow-ons to that (robots-jusi). **When you add a lifecycle field to the
launch spec, add it in three places** — the flag table, the `--register` field
loop in `mem.go`, and whatever reads it back. And the reason the fatal exit
went unseen for so long: `bin/parlay-spawn` ended its registration with
`>/dev/null 2>&1 || true`; it now prints a named warning on failure instead
(still non-fatal — a launch spec isn't worth aborting a live spawn over).

Note `teardown` resolves `~/.parlay/agents` from `HOME` and ignores
`PARLAY_AGENT_HOME` (matching `commands-teardown.ts:23`); `identity` honors
`PARLAY_AGENT_HOME`. Set `HOME` when testing teardown end-to-end.

## Arming a listener is a TAKEOVER, not an addition (robots-fgyz)

`parlay listen` used to be purely additive: every restart, reconnect, or fresh
turn started another poll loop on the same channel and nothing ever ended the
previous one. The Mayor agent accumulated **12** live `parlay-cli listen
--agent mayor` processes (every other agent had exactly one), so every
captain→mayor message was delivered and processed up to 12 times, with 14
leaked long-poll shells and the Mayor session burning 20-27% CPU feeding them.

`tools/cli/internal/monitor/singleton.go` enforces one live loop per agent
channel: `CmdListen` reaps every other loop on that channel **before**
register/announce, so an HTTP failure can never leave a duplicate running.
Three decisions worth knowing before touching it:

- **Takeover, not "reuse and exit".** The process arming now is the one wired
  to the live harness `Monitor{}` task; exiting immediately would leave that
  task dead with the agent registered-but-deaf — the robots-dcag shape.
- **`ps` match, not a pidfile.** A pidfile only knows about listeners armed
  after this landed; the twelve that already existed had none, and a pidfile
  adds its own staleness failure mode. The process table cannot go stale.
- **Matching fails toward "not a duplicate", because the two error directions
  are not symmetric.** Killing a non-duplicate ends a live agent's session;
  missing one only leaves the pre-existing duplicate. So: exact token compare
  on the agent id (`--agent mayor` never matches `mayor-2`), the subcommand
  must be preceded by a parlay binary basename (a shell wrapper whose *command
  string* contains the invocation is not the listener), scanning stops at
  `--name`/`--caps` because `ps` flattens argv unquoted and a ticket title
  routinely contains `--agent`, and self plus every ancestor is protected —
  the harness arms through a shell whose command string is the whole
  invocation, so reaping an ancestor kills the reaper.

`PARLAY_LISTEN_NO_SINGLETON=1` opts out (announced on stderr). The singleton
enforcement is Go-only, no TS port — but `listen` itself exists in both CLIs,
so do **not** add it to `GO_ONLY_VERBS`. No `check` case in
`tools/cli/parity/run.sh` either: the singleton behavior causes a deliberate
divergence the harness can't reconcile.

## `parlay mechanic on|off|status` is the kill switch for the robots→mechanic auto-spawner

Filing a `robots` bead auto-dispatches a mechanic agent; `parlay mechanic off`
pauses that without touching launchd or killing the tailer/watcher.
Authoritative code: the gate is `mechanicDispatchOff()` in
`tools/cli/internal/robotswatch/handlers.go`, checked at the top of
`dispatchMechanic` — the single choke point both the PUSH (`robots-tail`) and
POLL (`robots-watch`) paths converge on. The verb itself is
`tools/cli/internal/commands/mechanic.go`. Disabled state = the sentinel file
`$PARLAY_STATE_HOME/mechanic-dispatch.off` (default `~/.parlay/`) OR env
`PARLAY_MECHANIC_DISPATCH=off`; the sentinel is durable operator intent and
wins over `PARLAY_MECHANIC_DISPATCH=on`. When OFF the tailer/poller keep
running and advancing their offsets, so re-enabling does NOT replay the
backlog. **Go-only, no TS port** — same reasoning as `merge-gate`: no `check`
case in `tools/cli/parity/run.sh`, but it must be in that script's
`GO_ONLY_VERBS` or its usage line reddens every help diff (robots-xaxt);
it was missing there until the live-commands branch added it. Complementary to the durable-mechanic-lifecycle
work (robots-jkwc) — that gates nothing, this dispatches nothing when off.

## Two-arg `git merge-tree` is not a predicate — teardown's landed check never fired (robots-ceon)

`isContentLanded` (`tools/cli/internal/commands/teardown.go`, mirrored in
`packages/cli/src/commands-teardown.ts`) is the only thing that lets `parlay
teardown`/`parlay sweep` release an agent whose work landed as a **squash**
commit — the original commits are unreachable from any remote ref, so
`hasUnpushed` is true and the git checks refuse. It was written as two-arg
`git merge-tree <branch> <head>` plus `out == "" || strings.Contains(out,
branch)`. On git >= 2.38 that form prints a bare tree OID, so `out` is never
empty and a branch name like `main` can never occur in 40 hex digits: the
function returned **false for every input** and the escape hatch had never
once fired since it shipped. It failed closed, so nothing broke loudly — it
just read as a working gate for as long as nobody tested it.

The working form (from firstmate's `bin/fm-teardown.sh` `content_in_default`)
is `git merge-tree --write-tree <ref> <head>` compared against `<ref>^{tree}`;
`<ref>` is the *remote-tracking* ref, refreshed with a best-effort fetch first,
because a stale `origin/<default>` cannot see the thing you are asking about.
Every inconclusive path still returns false — teardown refuses rather than
guesses. Pinned by `teardown_test.go` / `commands-teardown.test.ts`, both
running real git repos against a real bare origin; a mocked `sh()` would have
reproduced this bug instead of catching it.

Same ticket, second half: `teardown --help` advertised "PR patch-id (if
available) or merge-tree equality" and the TS source claimed "three
strategies". No patch-id strategy has ever existed in either CLI —
the fold design doc §3.7 (captain-private, not in this repo) describes the
firstmate original, not what was folded. When porting a gate, port its test too; a gate with no test
is indistinguishable from a gate that has never run.

## Every path that removes a worktree must go through `checkWorktreeGitSafety` (robots-cncx)

`parlay variant teardown` merged the variant's notes, then ran `git worktree
remove --force` with **no git check at all** — uncommitted changes in a
variant's worktree were permanently destroyed, while `parlay teardown` refused
the identical situation. Its `--force` flag only ever gated *unmerged memory*,
so the safe-looking bare invocation was the destructive one.

The uncommitted/unpushed/landed-content checks now live in one place —
`checkWorktreeGitSafety(cmd, agentID, worktree, force)` in
`tools/cli/internal/commands/teardown.go` (mirrored in
`packages/cli/src/commands-teardown.ts`) — called by `teardownAgent` and by
`variantTeardown`, which runs it **before** `mergeKind` writes into the primary
so a refusal leaves nothing half-merged. `--force` now means "discard the
working tree too", same as `parlay teardown`. Regression coverage:
`variant_test.go` (real repo + real origin + real worktree, `$HOME` redirected
because `parlayWktreesDir()` honors no override).

Rule for anything new here: `git worktree remove --force` is unconditional
destruction — route it through `checkWorktreeGitSafety` rather than adding a
fourth ad hoc check, and put the git gate ahead of any state mutation so a
refusal is a genuine no-op.

## `mechanic-dispatch` canonical source lives in `tools/mechanic-dispatch/`

The robots-ticket dispatcher — `robots create` → `~/data/robots/events.jsonl`
→ `parlay robots-tail` (`tools/cli/internal/robotswatch/`) →
`mechanic-dispatch <id>` → `parlay-spawn` — was for a long time an
install-only artifact at `~/.local/bin/mechanic-dispatch` with **no in-repo
source**. Its canonical source is now `tools/mechanic-dispatch/mechanic-dispatch`,
installed via `tools/mechanic-dispatch/install.sh` (backup-once, `--status`/
`--uninstall`, mirrors `tools/robots-emit/`). Edit the repo file, then re-run
the installer; never hand-edit the `~/.local/bin` copy.

Mechanics run in an **isolated git worktree**, never a repo's primary checkout:
`mechanic-dispatch` passes `--worktree` to `parlay-spawn` whenever the resolved
zone `--cwd` is inside a git repo (`git -C <cwd> rev-parse --show-toplevel`),
so a future git-repo zone in `zone_entry()` is isolated automatically.
`parlay-spawn` resolves `--worktree` against `--cwd` (creating
`<repo>/.worktrees/parlay-<id>`), so the two compose with no extra plumbing.
The `default`/`~` zone is deliberately left non-isolated (triage-only — `$HOME`
is not a repo). Bash 3.2 portable; behavior otherwise unchanged (bad-id guard,
closed-ticket skip, liveness re-dispatch). Test:
`tools/mechanic-dispatch/mechanic-dispatch.test.sh`. Phase-1 (isolation) only —
firstmate state-meta bridging and worktree teardown/landing are follow-ups.

## CI is `.github/workflows/ci.yml` — and a green check was never proof before it

Until this landed the repo had no `.github` directory at all: the only PR check
was CodeRabbit, which reports conclusion `pass` even when its account-wide rate
limit meant it never read the diff (see the merge-gate section above — that verb
exists because of the same lie). A triage of the open-PR backlog found 23/23 PRs
green and 3 provably broken.

Four parallel jobs, each pinned to action commit SHAs with `permissions:
contents: read` and no `pull_request_target`: **go** (build/vet/test/gofmt over
all five modules), **bun** (tests for `packages/{input,client,server,cli}` and
`tools/gate-tag` — which gets no `bun install`, having no `package.json` and no
dependencies — plus typecheck for `packages/input` and `tools/split-test`),
**shell** (seven hermetic harnesses, preceded by a `jq`/`curl`/`git` presence
check so a binary missing from the rolling runner image fails the step instead
of letting a harness skip itself green), **hygiene** (conflict markers, 2 MiB
tracked-file ceiling measured on the tracked *blob* via `git ls-tree -l`, never
`stat` on the worktree path, which would follow this repo's tracked symlinks).
Both hygiene gates distinguish "the tool failed" from "the tree is clean" —
`git grep`'s status 2+ and a failed or empty `git ls-tree` each fail the step.
Read the file's own comments for per-step rationale rather than re-deriving it.

Four things worth knowing before editing it:

- **Only pull-request runs share a concurrency group.** PR runs key on
  `github.ref` with `cancel-in-progress`, so a new push supersedes the old run.
  Push-to-main runs key on `github.run_id` — a group of one each. That is not
  interchangeable with `cancel-in-progress: false`: runs sharing a group key
  evict each other while still *pending*, so a burst of landings on the shared
  `refs/heads/main` key would drop the middle commits' runs before they started.
  Only a unique key guarantees every landed commit gets a verdict.
- **`gofmt` in CI is not a duplicate of `TestGofmtClean`.** That test
  (`tools/cli/internal/commands/gofmt_test.go`) resolves its root to the
  tools/cli module, so it guards one module of five; the CI step covers the
  other four. All five Go modules are pure stdlib — no `go.sum`, no external
  requires — so the whole Go suite runs in seconds and the cache is a build
  cache, not a download cache.
- **Every test step redirects `$HOME`, and it is load-bearing, not ceremony.**
  `packages/cli`'s tests resolve `join(homedir(), ".parlay", "agents", …)`
  directly and really do create it; several Go tests write `~/.parlay`,
  `~/.config/bd`, `~/.beads`. A hosted runner throws `$HOME` away, but this must
  also be safe on a self-hosted one — see the `uninstall.sh --purge` incident
  above, where a smoke test permanently deleted the live `~/.parlay`. Because Go
  derives `GOCACHE`/`GOMODCACHE` from `$HOME`, the go job pins those to explicit
  paths *before* the redirect; drop that step and the cache silently evaporates.
- **Deliberately not in CI**, because they drive live or macOS-only state:
  `tools/monitor/parlay-monitor.test.sh` (enrols over a relay control socket),
  `tools/relay/deploy/{ensure-up,install}.test.sh` (launchctl/PlistBuddy),
  `tools/cli/parity/run.sh` (stands up a real go-server fixture),
  `examples/bootstrap-sandbox.sh` (same class as the previous entry — it stands
  up a real `packages/server` fixture; it has also not been trial-run to the
  bar stated at the end of this bullet), and
  `packages/client`'s `bun run build` (its `build.ts` POSTs to the captain's
  live `:31337` — see the packages/client note above). Also not enforced:
  `tools/hooks/pre-commit`'s 250-line ceiling on staged `.ts` files — it is a
  staged-diff check, not a whole-tree one, so it does not map onto a CI job; it
  is named here so a contributor whose commit the hook rejects can find the
  rule written down. The other seven shell harnesses were each trial-run with
  `~/.parlay` and `~/.treehouse` snapshotted before and after and produced zero
  drift; that is the bar for adding one.

The `go` job's artifact guard is `git status --porcelain` being empty after
a full `go build ./...`, not a filename list, so a newly added main package
cannot reintroduce the 9.6 MB `tools/relay/relay` binary that one PR committed —
that path is now gitignored alongside the pre-existing
`packages/eval-engine/eval-engine` entry.

## The live-command registry sees only what reports itself — say so, don't fill the gap

`docs/live-commands.md` is authoritative for the design; two things about it
are easy to get wrong from the code alone.

**Registration is Go-CLI self-reporting, and the coverage gap is a feature.**
`tools/cli/main.go` wraps dispatch in `commandreport.Begin`, whose end report
goes out through *both* `httpc.Exit` (so every `httpc.Die` in every verb closes
its record without those verbs knowing) and a `defer` in `main` (normal return
and panic — a panic reports a non-zero exit so the record never reads green).
Anything that is not the Go CLI — `bin/parlay-spawn`,
`tools/monitor/parlay-monitor.sh`, the retired `packages/cli`, work the server
originates — is invisible; `parlay commands` excludes itself so the observer
never shows up in its own output; and a bare `parlay` (the fleet snapshot) has
no verb to report under, so it does not register either. Both renderers print
that limit in their empty state. Do **not** "improve" coverage by having the server infer
running commands from requests it happens to receive: an entry nothing can
close becomes a permanent zombie, which is the failure this design spends its
90s staleness reaper avoiding. `PARLAY_COMMAND_REPORT=0` opts out.

**The registry stores no free-form text — keep it that way.** Verb, agent id,
pid, flag **names** (max 8), and a short `outcome` token; never argv, flag
values, positionals, paths, or an error string, because a parlay command line
routinely carries message bodies and tokens. Both enforcement points —
`commandreport.flagNames` before sending, `internal/store/commands.go`'s
`sanitizeFlags` on arrival — apply the identical rule: after cutting the token
at its first `=`, a flag name is one or two dashes, then a letter, then only
letters, digits, and dashes, and is at most 32 characters — `maxReportedFlagName`
and `maxCommandFlagName` are twins whose comments each name the other, since
separate Go modules cannot share the constant. **A leading dash is not what
makes a token a flag** — `-- heads up: the key is …` is a message body, and
anything failing the shape or the length is dropped WHOLE, never trimmed into
conforming shape: a trimmed name arrives looking like a legitimate flag. That
rule is about flag NAMES only. The identifier fields (`id`, `verb`, `agent`,
`outcome`) are clamped in place instead — whitelisted characters up to a
length bound — because they carry no caller prose and dropping one would
render an unattributed row or, for `id`, no row at all. The 500-record cap
bounds the whole registry and is therefore server-only. The server repeats the check because the report
endpoints are unauthenticated and client-side classification is not a security
boundary. Adding a field here means adding it to that whitelist deliberately.
The three mutating routes require `Content-Type: application/json` for the same
CSRF-shaped reason `packages/server/src/guard.ts` does; the read route stays
world-readable like `/api/chat/agents`.

The "one registry, two renderers" claim is enforced, not asserted:
`packages/go-server/testdata/live-commands.golden.json` is read by the Go
handler, Go CLI, and client Bun suites, and
`TestSSEBurstAndReadEndpointCarryByteIdenticalCommands` pins that the panel's
SSE frame and the CLI's read endpoint are the same bytes. Change the wire shape
and all three fail in one commit — that is the point.

`parlay commands` is **Go-only, no TS port** — same reasoning as `merge-gate`:
no `check` case in `tools/cli/parity/run.sh`, but it is in that script's
`GO_ONLY_VERBS`. Its `--watch` mode is a stream, not a snapshot: without
`--all` it prints the terminal line for any command it already showed as
running, because an end event is how a record leaves the running set, and it
gives up loudly (a stdout notice plus a non-zero exit) when the SSE stream
closes rather than returning quietly — the robots-dcag shape.

## Need a real parlay instance to test against? `examples/bootstrap-sandbox.sh`

`examples/` is a public, sanitized two-agent configuration (`parlay-state/` →
`~/.parlay`, `data-dir/` → `$PARLAY_DATA_DIR`), and
`examples/bootstrap-sandbox.sh` instantiates it in a `mktemp` sandbox on a
kernel-picked free port, builds `tools/cli`, starts `packages/server`, and
asserts the round trip. Reach for it instead of hand-rolling another throwaway
instance — and read it before writing one, because it encodes the isolation
recipe: redirect **`$HOME`** as well as `PARLAY_DATA_DIR`/`PARLAY_STATE_HOME`/
`PARLAY_AGENT_HOME`, since `launch`/`teardown`/`variant`/`guard` resolve
`~/.parlay/agents` from `$HOME` and ignore `PARLAY_AGENT_HOME` (see the B4/B9
notes above). `PAI_DIR` too — see the `PARLAY_DATA_DIR` section above for why it
is not covered.

`sweep` is the sharpest case, because it straddles that split: a half-redirected
`sweep --apply` judges the REAL agent store against a redirected keep-list, and
it is the verb that deletes stores and removes worktrees. It fails toward held,
but redirect `$HOME` rather than relying on that. `examples/README.md` has the
per-variable breakdown.

Two traps it exists to keep you out of: `pkill -f 'bun src/index.ts'` matches
**every** worktree's sandbox server on this box, not just yours (the script
kills its own recorded pid instead); and `bin/parlay` exports
`PARLAY_SERVER=http://localhost:31337`, which outranks `config.json`, so a
sandbox must build and invoke the Go binary directly.

Anything added to the example ships publicly — it is derived from the captain's
live setup, so keep every value a stand-in and re-run the script before
committing.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
