# parlay-split — Parlay split-testing tool

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
| No prod-path writes | Each sandbox gets its own `PARLAY_DATA_DIR`, `PARLAY_RELAY_RUNTIME`, `PARLAY_AGENT_HOME`, **and `PAI_DIR`** (see Isolation gaps). Prod `~/exchange`, `$TMPDIR/parlay`, and `$PAI_DIR/MEMORY/STATE/parlay-agents.json` are never written. |
| Prod untouched, proven | `sandbox up` snapshots prod relay + eval-engine PIDs before boot and asserts they are unchanged after — failing loudly if a sandbox disturbed prod. |
| Probe traffic is quarantined | Any probe that reaches a real store uses a throwaway agent id prefixed `split-probe-…`, so it stays in its own channel. |

If **any** isolation assertion fails, `sandbox up` tears the sandbox back down and
exits non-zero. A half-booted sandbox is never left running.

---

## Mode 1 — `sandbox up` / `down`: isolated stack + isolation proof

Boots a complete, isolated Parlay stack:

- **server** — `bun packages/server/src/index.ts` on a free port, with its own
  `PARLAY_DATA_DIR` and `PAI_DIR`.
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
   would prove `PAI_DIR` leaked (see Isolation gaps),
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
branch) — and runs the same probe suite against each. Because each sandbox
builds its own relay/engine from its own source and runs the server from its own
tree, any behavioral difference is attributable to the **code**, not the
environment. Zero prod contact.

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

Building this tool surfaced two places where a fresh checkout / the documented
env surface is **not** sufficient on its own. Both are handled at the *sandbox
level* (env overrides + seeding), never by editing prod paths in the server.

### 1. Agent registry is keyed off `PAI_DIR`, not `PARLAY_DATA_DIR`

`packages/server/src/sse.ts` persists the agent registry to
`$PAI_DIR/MEMORY/STATE/parlay-agents.json` and **rehydrates it at module init**.
`PARLAY_DATA_DIR` isolates chat *history* but **not** the agent *registry*. A
naive sandbox that only overrode `PARLAY_DATA_DIR` would:

- boot with the ~20+ prod agents already loaded, and
- write the registry back to the **prod** file.

**Handling:** the sandbox also sets `PAI_DIR` to a sandbox-local `pai/` dir. That
cleanly redirects the registry (and the tts cache/reports + tool/hook tailers,
which also key off `PAI_DIR`) into the sandbox. `sandbox up` then asserts the
sandbox registry is **empty** on first boot — proving the redirect took.

*This is a genuine finding about Parlay's env contract:* `PARLAY_DATA_DIR` alone
does not fully isolate a server. Full isolation needs `PARLAY_DATA_DIR` **and**
`PAI_DIR`. Worth folding into the server's own isolation story eventually.

### 2. `parlay-ui.{ts,js}` are untracked runtime-required working files

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

---

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
