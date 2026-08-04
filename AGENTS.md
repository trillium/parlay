# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- Add durable project-specific notes here as they are discovered through real work.

## `packages/server` is a symlink into the live Pulse install

`packages/server/src/*` (all files except `package.json`) are symlinks into
`~/.claude/PAI/PULSE/modules/chat` — the actual code that runs live inside
Pulse. There is one copy of the source; edits from either path hit the same
file. See `packages/server/README.md` for the rationale and the known
`tools/split-test` tradeoff (per-branch server testing no longer works since
every branch resolves to the same external PULSE code).

**As of 2026-08-01, every file in that chain is a broken self-referential
symlink loop** (verify with `python3 -c "import os; print(os.path.realpath(p))"`
— it resolves to a path `os.path.exists()` reports `False` for). Confirmed on
both a disposable treehouse worktree and the primary checkout at
`~/code/parlay`, so it is not worktree-specific. No file under
`packages/server/src/` can currently be read or edited through normal tooling
until someone fixes the symlinks directly under `~/.claude/PAI/PULSE` (outside
any git worktree — not something an isolated agent should attempt). New
files with names that don't already exist in that farm are unaffected (Write
creates a plain file), but they can't be wired into `router.ts`/`index.ts`
until the loop is fixed. `packages/server/src/debug-log.ts` was added this
way — a standalone, not-yet-wired handler with wiring instructions in its
header comment.

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
independent of the symlink structure below.

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

## `bun test` only works from inside a package directory

There is no root `bunfig.toml`, so running `bun test` from the repo root
does not preload `@happy-dom/global-registrator` for packages whose tests
touch `document`/`window` (e.g. `packages/client`, `packages/input`) — those
tests fail with `ReferenceError: document is not defined` at the root even
though they pass cleanly run from their own directory (`cd packages/X && bun
test`). This predates any one package; always run a package's tests from
inside that package when validating.

## Publishable `@parlay/*` packages (npm scope not yet claimed)

This section covers npm packages under `packages/` (those with a
`package.json`); Go modules like `packages/go-server` (no `package.json`)
are outside this private/publishable dichotomy entirely. Every npm package
under `packages/` is `private: true` except `packages/input`
(`@parlay/input`) and `packages/parlay-input` (unscoped `parlay-input`,
holds the bare name and re-exports `@parlay/input`) — these are scaffolded
for eventual publishing but have never actually been published, and the
`@parlay` npm scope itself has not been claimed. Neither `bun build` nor
`tsc` alone produces a complete publishable output for a TS package here:
`bun build.ts` emits the JS bundle to `dist/`, and a package-local
`tsc --emitDeclarationOnly` (via a package-local `tsconfig.json`) emits the
`.d.ts` — `bun run build` runs both. `packages/server` was evaluated and is
not reasonably publishable: it's a `bun serve()` application with side
effects on import (opens a port, touches disk), not an importable library,
and its source is symlinked into the user's live personal Pulse install
(see the section above) — it remains `private: true`.

`packages/client` also gained `main`/`module`/`exports`/`types` fields and a
`dist/index.js` build target (`src/lib.ts`), but stays `private: true` — this
is a same-monorepo/workspace library entry point for external host apps (e.g.
herdr-web) importing the server-eval dispatcher, not npm publishing prep.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
