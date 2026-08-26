# `packages/server` is a standalone Bun server for the Parlay chat API

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


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
handlers spread never reaches the wire. The rest — history, version, pages,
and `GET /api/chat/uploads/<name>`, which an `<img src>` must load — keeps
the old wildcard and stays world-readable.

**The rule, on both servers: inside the mutating and identifier-aiming
surface, a route is guarded by what its handler DOES, REGARDLESS OF HTTP
METHOD.** The verb is not evidence — `GET /subscribers` is guarded because it
hands out identifiers, and `GET /poll` because it registers the channel it is
polling for. Classify by reading the handler.

The set now covers the whole identifier surface. The two TS routes long
tracked as accepted residue under `identifier-disclosure-remains-on-sse` —
`GET /api/chat/events` (writes `sseClients` from an attacker-supplied
`?device=` in `router-events.ts`, and the `tts_event` frames its stream
carries reach every connected client with that device uuid in them:
`router-tts-events.ts` broadcasts `{ …, device, ...body }` unfiltered) and
`GET /api/chat/agents` (`router-messages.ts`, every registered agent id, the
same class of disclosure `/subscribers` was guarded for) — are **guarded**
now, closing that tracking item. Guarded, not Go's no-ACAO-ever, because
`parlay-input` (`packages/input`, the published npm client) legitimately
opens a cross-origin `EventSource` to `/api/chat/events` from loopback/LAN
pages, which the guard answers with a reflected ACAO; only foreign pages get
403. Both are GETs, so the content-type gate never applies. `GET
/api/chat/history` deliberately stays on the wildcard: it has always been the
documented world-readable surface, and closing it is a product decision, not
a guard classification. The Go server's unguarded routes send no ACAO at all,
so a foreign page cannot read their bodies.

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
`PARLAY_DATA_DIR` does not cover, and every event they see is now pushed **out
of this process over HTTP** to `PARLAY_HUB_URL` (default the Go server on
`http://127.0.0.1:4242` — see the hub-ingress section below), as a
`system_update` chat message or a `tool_event` frame. A scratch
instance booted on a host that has a populated `$PAI_DIR` therefore replays real,
live agent turns from the agents running on that host into whatever hub answers
that address — on a development box, the captain's live one, whose history keeps
them. Set `PAI_DIR` to an empty scratch dir alongside `PARLAY_DATA_DIR` whenever
you boot a test instance (or point `PARLAY_HUB_URL` at a dead port), or the run
writes into and deletes out of the real one;
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
