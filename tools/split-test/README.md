# parlay-split — Parlay split-testing tool

> **This is a dated investigation record, not a description of the repository as
> it stands today.** Written 2026-07-18, last revised 2026-08-04. The modes and
> the safety model describe the tool; the "Isolation gaps found" section below
> records what was true of *this repository* when each gap was discovered.
> Several have since been closed — each of those is marked **RESOLVED** in place,
> with what closed it, rather than rewritten, so the record of the gap survives
> alongside its resolution.

A standalone tool for testing Parlay code and topology **without touching the
production stack**. It boots fully isolated Parlay sandboxes, compares two front
doors against one backing store, and split-tests two code checkouts side by side.

Ships as a standalone bash + TypeScript tool under `tools/split-test/` — **not** a
new eval-engine verb, and it never requires an engine rebuild or a relay restart.

```
parlay-split sandbox up   --name <n> [--branch-dir <path>] [--with-engine]
parlay-split sandbox down --name <n>
parlay-split sandbox list
parlay-split two-door     --a <urlA> --b <urlB> [--soak <seconds>]
parlay-split two-stack    --a-dir <worktreeA> --b-dir <worktreeB> [--with-engine]
```

Entry point: `tools/split-test/bin/parlay-split` (bash wrapper → `bun src/cli.ts`).

---

## Safety model — "don't break the current one"

This tool exists to test Parlay **beside** the live system, never through it.

| Rail | Enforcement |
|------|-------------|
| Never bind reserved ports | Sandboxes pick a **free high port** (42000+) via a real bind-test. `31337`, `31338`, `31339`, `4242`, `4343` are refused outright (`assertNotReserved`). |
| Never use `launchctl` | Sandbox relays run as **plain background processes**, not launchd jobs. The prod relay (`com.parlay.relay`) is never signaled. |
| Kill exactly what we started | `sandbox down` reads the recorded PID manifest and kills **only** those PIDs — after verifying each still matches the command line we started (guards against PID recycling). No broad `pkill`. |
| No prod-path writes | Each sandbox gets its own `PARLAY_DATA_DIR`, `PARLAY_RELAY_RUNTIME`, `PARLAY_AGENT_HOME`, **and `PAI_DIR`** (see Isolation gaps). Prod `~/exchange`, `$TMPDIR/parlay`, and `$PAI_DIR/MEMORY/STATE/parlay-agents.json` are never written. (The registry now follows `PARLAY_DATA_DIR` too — gap #1 below.) |
| Prod untouched, proven | `sandbox up` snapshots prod relay + eval-engine PIDs before boot and asserts they are unchanged after — failing loudly if a sandbox disturbed prod. |
| Probe traffic is quarantined | Any probe that reaches a real store uses a throwaway agent id prefixed `split-probe-…`, so it stays in its own channel. |

If **any** isolation assertion fails, `sandbox up` tears the sandbox back down and
exits non-zero. A half-booted sandbox is never left running.

---

## Mode 1 — `sandbox up` / `down`: isolated stack + isolation proof

Boots a complete, isolated Parlay stack:

- **server** — `bun packages/server/src/index.ts` on a free port, with its own
  `PARLAY_DATA_DIR` and `PAI_DIR`. *(As recorded here at the time: `src/*` are
  symlinks into the live Pulse chat module, so this always runs the same
  external source, not branch-dir-specific code. **RESOLVED** by PR #42
  (`15bd487`, 2026-08-04), which replaced the symlink farm with real tracked
  files — see gap #3.)*
- **relay** — built from the branch-dir's own Go source into a sandbox-local
  binary, run as a background process with its own `--runtime-dir`.
- **eval-engine** — optional (`--with-engine`), built from source, on a free port
  via `PARLAY_EVAL_ADDR`. **No engine grammar/manifest change, no rebuild of the
  prod engine.**

State lives under `~/.cache/parlay-split/<name>/`:

```
manifest.json        # env + component PIDs/ports/cmds
server.pid  relay.pid  eval-engine.pid
server.log  relay.log  eval-engine.log
bin/                 # per-sandbox compiled relay/eval-engine
data/                # PARLAY_DATA_DIR (chat-history.jsonl, draft)
runtime/             # PARLAY_RELAY_RUNTIME (relay.sock, <agent>.chan spools)
agents/              # PARLAY_AGENT_HOME (identity/scratchpad)
pai/                 # PAI_DIR (agent registry, tts/observability)
```

`up` is also the **proof** that the isolation env vars work end to end. After
boot it asserts:

1. the server is **listening on the chosen port** (`PARLAY_PORT` honored),
2. the data dir is **ours and not prod `~/exchange`** (`PARLAY_DATA_DIR` honored),
3. the **agent registry is empty** — a fresh sandbox that rehydrated prod agents
   would prove the registry redirect leaked (see Isolation gaps),
4. the relay created `relay.sock` **in our runtime dir**, and its `/agents`
   endpoint **reports our runtime dir** (`--runtime-dir` honored),
5. the eval-engine (if any) is **listening on the chosen port**,
6. prod relay + eval-engine PIDs are **unchanged**.

```bash
# Boot, prove isolation, print the manifest summary
parlay-split sandbox up --name probe1

# With an eval-engine too
parlay-split sandbox up --name probe1 --with-engine

# Boot from a specific checkout
parlay-split sandbox up --name probe1 --branch-dir ~/code/parlay/.worktrees/feature

# Tear down — kills exactly the recorded PIDs, removes the manifest dir
parlay-split sandbox down --name probe1

# See what's up
parlay-split sandbox list
```

---

## Mode 2 — `two-door`: same store, two front doors

The pulse-next case. Door **A** is the direct server
(`http://localhost:31337`); door **B** is the proxy (`http://localhost:31339`)
that forwards to the **same** backing store. `two-door` proves they really are
the same store and that the proxy adds no correctness cost.

Suite (all against a throwaway `split-probe-<ts>` channel):

1. both doors reachable (`/api/chat/subscribers`),
2. register via A → visible in B's registry,
3. **send via A → observe via B** (cross-door delivery A→B),
4. **send via B → observe via A** (cross-door delivery B→A),
5. poll-latency comparison (delivery round-trip through each door),
6. subscribers parity (same registered count through both doors),
7. **soak** (`--soak <seconds>`): for the whole window, inject a message every
   ~2s in each direction and verify every injected message is delivered
   cross-door — reported as `delivered/injected` per direction with average
   latency. Any drop shows up as `delivered < injected` → FAIL.

Output is a PASS/FAIL comparison table.

```bash
# The pulse-next long-poll soak: direct (:31337) vs proxy (:31339), 60s soak
parlay-split two-door --a http://localhost:31337 --b http://localhost:31339 --soak 60

# Quick comparison with no soak
parlay-split two-door --a http://localhost:31337 --b http://localhost:31339
```

Exit code is non-zero if the comparison FAILs, so it gates in CI/scripts.

> **Note:** `two-door` targets *external* doors — it never binds them. It only
> writes to a `split-probe-…` channel on the shared store.

---

## Mode 3 — `two-stack`: split-test two code checkouts

Boots **two** fully isolated sandboxes — one per checkout (baseline vs feature
branch) — and runs the same probe suite against each. Each sandbox builds its
own relay/engine from its own source, so relay/engine differences are
attributable to the **code**, not the environment. Zero prod contact.

> **RESOLVED — the limitation recorded here no longer applies.** As found:
> "Server is no longer split-tested in isolation. `packages/server/src/*` are
> now symlinks into the live Pulse chat module
> (`~/.claude/PAI/PULSE/modules/chat` — see `packages/server/README.md`), so
> every sandbox's `bun packages/server/src/index.ts` resolves to the **same**
> external source regardless of `--branch-dir`. `two-stack` can no longer
> attribute server-side behavioral differences to the branch under test."
> PR #42 (`15bd487`, 2026-08-04) replaced those symlinks with real tracked
> files — `git ls-files -s packages/server/src/` now returns 44 regular
> (`100644`) entries and no symlinks — so each `--branch-dir` resolves its own
> server source again and `two-stack` covers server code once more. See gap #3.

```bash
# Baseline (main checkout) vs a feature worktree
parlay-split two-stack \
  --a-dir ~/code/parlay \
  --b-dir ~/code/parlay/.worktrees/feature

# With eval-engines in both stacks
parlay-split two-stack --a-dir <A> --b-dir <B> --with-engine
```

Both sandboxes are always torn down at the end, even on failure. Result is a
side-by-side PASS/FAIL table; exit non-zero if either stack FAILs.

---

## Isolation gaps found (documented, not silently patched)

Building this tool surfaced places where a fresh checkout / the documented
env surface is **not** sufficient on its own. The first two are handled at the
*sandbox level* (env overrides + seeding), never by editing prod paths in the
server. The third (below) has no sandbox-level fix.

### 1. Agent registry was keyed off `PAI_DIR`, not `PARLAY_DATA_DIR` — FIXED

*Historical, kept because it explains why the sandbox sets `PAI_DIR` too.*

`packages/server/src/sse.ts` used to persist the agent registry to
`$PAI_DIR/MEMORY/STATE/parlay-agents.json` and **rehydrate it at module init**,
so `PARLAY_DATA_DIR` isolated chat *history* but not the agent *registry*. A
naive sandbox that only overrode `PARLAY_DATA_DIR` would boot with the prod
agents loaded and write the registry back to the **prod** file. This tool worked
around it by also setting `PAI_DIR` to a sandbox-local `pai/` dir.

That workaround was not enough for everyone: Pulse's `boot-smoke.test.ts` cannot
redirect `PAI_DIR` (its other modules need the real one), so it booted against
the live registry and the startup prune sweep deleted two real agent channels
(robots-jcjj). The gap is now closed at the source — `packages/server/src/paths.ts`
resolves every persisted path that is routed through it, registry included, and
`PARLAY_DATA_DIR` redirects all of those. One module sits outside that routing and
`PARLAY_DATA_DIR` does not reach it: `packages/server/src/tts.ts` resolves `PAI_DIR`
itself (`tts.ts:39`, `process.env.PAI_DIR ?? ~/.claude/PAI`), then **appends** to
`$PAI_DIR/MEMORY/OBSERVABILITY/tts-pronunciation-reports.jsonl` (`:153`, `:178`),
**creates** `$PAI_DIR/MEMORY/STATE/tts-cache/` (`:70`), and **`unlinkSync`-deletes**
clips out of that cache once it exceeds `DISK_CACHE_MAX` (100) (`:77-78`) — writing
into and deleting out of the real `$PAI_DIR` unless `PAI_DIR` is redirected too.

**Handling here is unchanged and still correct:** the sandbox sets both
`PARLAY_DATA_DIR` and `PAI_DIR` (the latter still covers the tts cache/reports
and the tool/hook tailers, which are not persistence paths and still key off
`PAI_DIR`). `sandbox up`'s assertion is behavioral — the sandbox registry must be
**empty** on first boot — so it holds either way.

### 2. `parlay-ui.{ts,js}` are untracked runtime-required working files — RESOLVED in part

*The original finding, kept verbatim:*

`packages/server/src/router.ts` imports `./parlay-ui`, but `parlay-ui.ts` and
`parlay-ui.js` are **untracked** (never committed) working files present only in
the canonical checkout. A fresh `treehouse` worktree lacks them, so the server's
import fails to resolve and it cannot boot.

**Handling:** before booting the server, the sandbox **seeds** these files into
the branch-dir from the canonical checkout (override the source with
`PARLAY_SPLIT_SEED_FROM`). If the branch-dir already has its own copy, that copy
is respected (never overwritten).

*This is a genuine finding too:* a clean checkout of Parlay cannot boot its
server without these untracked files. They should probably be committed or
generated by a build step.

**RESOLVED in part — the boot consequence stated above is no longer true.**
Verified against the current tree: `git ls-files packages/server/src/` lists
`parlay-ui.ts` as a tracked regular file, so the `./parlay-ui` import resolves
from a clean checkout. `parlay-ui.js` is genuinely still untracked — it does not
appear in `git ls-files` — but `packages/server/src/parlay-ui.ts:10-11` reads it
inside a `try`/`catch` that falls back to the string
`"// parlay-ui.js missing from server bundle"`, so its absence degrades one
served asset and does not stop the server booting. The accurate statement today:
`parlay-ui.ts` is tracked, `parlay-ui.js` is not, and a clean checkout boots
regardless.

### 3. `packages/server/src/*` are symlinks to a single external, unbranched source — RESOLVED

*The original finding, kept verbatim:*

`packages/server/src/*` (all files except `package.json`) are symlinks into
`~/.claude/PAI/PULSE/modules/chat`, the live Pulse install (see
`packages/server/README.md`). Every sandbox's `--branch-dir` still resolves
`packages/server/src/index.ts` to that same external target, so `sandbox up`
and `two-stack` always run **identical** server code no matter which branch is
under test.

**Handling:** none — this cannot be fixed at the sandbox level without either
writing into the external PULSE checkout or hardcoding a path outside this
repo, both out of scope for this tool. Treat `two-stack` results as covering
relay/eval-engine behavior only; it no longer proves anything about
branch-specific server changes.

**RESOLVED — the symlink farm is gone.** PR #42 (`15bd487`, 2026-08-04) recovered
the real source from git history and replaced every loop symlink with a real
tracked file; `packages/server/README.md` records the same. Verified against the
current tree: `git ls-files -s packages/server/src/` returns 44 entries, all mode
`100644`, and no mode `120000` symlink entries at all. Each `--branch-dir` now
resolves its own `packages/server/src/index.ts`, so `sandbox up` and `two-stack`
run branch-specific server code again and `two-stack` covers server changes once
more.

## Environment overrides

| Var | Effect |
|-----|--------|
| `PARLAY_SPLIT_CACHE` | Override the `~/.cache/parlay-split` root. |
| `PARLAY_SPLIT_SEED_FROM` | Source checkout to seed untracked runtime files from (default `~/code/parlay`). |

## Requirements

- `bun` (server + CLI runtime)
- `go` (builds the sandbox-local relay / eval-engine from source)

## Typecheck

```bash
cd tools/split-test && bun run typecheck
```
