# `gc` is a runtime prerequisite

Gas City's `gc` binary is a **documented runtime prerequisite** for parlay's
Gas City execution plane. It is not bundled into parlay and not fetched at
install time — the settled packaging decision follows the Q5b `bd` precedent:
absent-or-too-old is a **named error with an install pointer, never a silent
degrade**. (Decision recorded in the spawn-lift scope report's ordering
section; reversible later to bundling or install-time fetch without changing
any seam code.)

## The pin

The pinned Gas City commit lives in [`third_party/gascity/PIN`](../third_party/gascity/PIN):

```
9700d9a48fb35a2063e1fcb9ee49ab664df26de0   (short: 9700d9a, upstream main as of 2026-09-15)
```

That is genuine upstream `main` as of 2026-09-15 and builds clean. Re-pinned
from `ac6c9c685` by task-svq1q: the new pin vendors beads `v1.3.0-rc.2`,
which is what lets a brain-joined city store start sessions (the old pin's
lib queried a `row_lock` column brain-base stores lack — evidence:
[`agent-notes/gc-main-brain-probe.md`](agent-notes/gc-main-brain-probe.md)).
**Never pin — or build — the captain's local `~/code/gascity` HEAD**: it is the
local branch `progname/monolith`, which does not compile. Full evidence and
the re-pin procedure: [`gascity-integration-contract.md`](gascity-integration-contract.md) §1.
Upstream has moved past the pin; the write-free drift check is:

```sh
git ls-remote https://github.com/gastownhall/gascity refs/heads/main
```

Moving the pin means: update `PIN`, re-run the build verification below,
refresh `third_party/gascity/openapi.json` + `LICENSE` from the same ref
(contract §3's vendoring convention), and re-verify the contract's
ref-sensitive tables.

## Building it

```sh
tools/gc-build/build-gc.sh            # → tools/gc-build/dist/gc
tools/gc-build/build-gc.sh --out ~/somewhere/gc
```

The script materialises the pinned commit (from `$GC_SRC`, from
`~/code/gascity` read-only via `git archive`, or by shallow network fetch),
builds `./cmd/gc` with `CGO_ENABLED=0`, and smoke-checks that the result runs
and speaks the typed `--json` contract. CI runs the same script when the pin
or the recipe changes (`.github/workflows/gc-build.yml`).

`CGO_ENABLED=0` is the default because this machine's Homebrew `icu4c` is
keg-only and unlinked; a cgo build needs all four ICU flags (the load-bearing
one is `CGO_CPPFLAGS`, not `CGO_CXXFLAGS` — contract §1):

```sh
export PKG_CONFIG_PATH=/opt/homebrew/opt/icu4c@77/lib/pkgconfig
export CGO_CPPFLAGS=-I/opt/homebrew/opt/icu4c@77/include
export CGO_CXXFLAGS=-I/opt/homebrew/opt/icu4c@77/include
export CGO_LDFLAGS=-L/opt/homebrew/opt/icu4c@77/lib
tools/gc-build/build-gc.sh --cgo
```

> **Operational trap — repeat this to anyone you hand a command to.** The
> captain's interactive zsh aliases `gc` to `git commit`. Scripts are
> unaffected (aliases don't expand non-interactively), but a human typing
> `gc supervisor stop` runs `git commit supervisor stop`. Every command a
> human is meant to paste must spell the **absolute path** to the binary.

## How parlay checks for it

`parlay doctor` carries a `gc` prerequisite check:

- **Missing** → named WARN with an install pointer (this file). It is a WARN,
  not a FAIL, only because no shipped verb requires `gc` yet; when the `gc`
  launcher is selected (`PARLAY_SPAWN_LAUNCHER=gc`), the same conditions
  escalate to FAIL.
- **Present but broken, or too old** (version floor `1.1.1` — the oldest
  artifact verified to answer the session/typed-JSON surface) → the same
  named line, at FAIL severity when the `gc` launcher is selected.
- **Working probe, not just presence** (contract §4: a locally-broken
  checkout must fail at the tool boundary, not at spawn time): the check runs
  `gc config show --json` under a scratch `GC_HOME` with the supervisor port
  redirected and requires the typed `{"schema_version": …}` refusal on
  stdout. `gc version` alone proves nothing — a from-source build of the pin
  reports `dev`, indistinguishable from an unrelated dev build (contract §2),
  and the stale `0.15.1.trillium` fork emits **no** typed JSON at all, which
  is exactly what the probe catches.

`PARLAY_GC` overrides where doctor (and later, the spawn path) looks for the
binary; otherwise it is resolved from `PATH`.

## The bd half: the city store needs the brain bd

`gc` alone is not enough to launch: the city scaffold parlay materialises
is inert files until its bead store is bootstrapped, and the bootstrap is
owned end to end by `parlay gc-spawn`: it resolves the bd (`PARLAY_BD`
wins, else first on `PATH` — the brain binary, `trillium/brain`), refuses a
missing or broken bd loudly, joins the store idempotently (`gc beads
health` for the managed-dolt side effect → `bd init --prefix pa --server
--server-port <recorded>` → `bd config set types.custom …` → `bd list`),
and puts the resolved bd first on the gc child's `PATH` so gc's own
shell-outs agree. A `session new` against an unbootstrapped store used to
die before emitting typed JSON (empty stdout); the verb now bootstraps
before launching, so that failure mode is closed at the seam rather than
documented around it.

This holds since the task-svq1q re-pin (gc@9700d9a, beads `v1.3.0-rc.2`
generation): a brain-joined city starts sessions end to end, proven live in
[`agent-notes/gc-main-brain-probe.md`](agent-notes/gc-main-brain-probe.md).
Under the old pin the fork's schema diverged and upstream was required —
that history and the upstream-build fallback recipe live in
[`agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md`](agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md).

## Agents operate on family stores directly

The city store holds only runtime/transport beads (sessions, convoys,
messages). Agent work state lives in the brain family: every synthesised
template stamps `BEADS_ACTOR=parlay-<id>` (derived, never caller-supplied)
and teaches the store conventions — read-wide via `brain search`, write to
the shared `parlay` family store (`~/data/parlay/.beads`, provisioned once
with `brain stores create parlay --no-wrapper`; no wrapper, so the name
never collides with this CLI, addressed explicitly per invocation via
`BEADS_DIR=…`), never exporting `BEADS_DIR` (gc's own bd shell-outs must
keep resolving the city-local store), and never writing runtime handles
(session IDs, ports, PIDs, panes) where the sync can reach them.

## herdr provider: the macOS socket symlink

The scaffold city runs the **herdr** session provider
(`city/city.toml [session]`). At the pin, the provider finds its
session-server socket via Go's `os.UserConfigDir()` — which on macOS is
`~/Library/Application Support`, **ignoring `XDG_CONFIG_HOME` entirely**
(verified: Go 1.26.4 returns `~/Library/Application Support` with XDG both
set and unset). herdr itself honors XDG and binds under `~/.config/herdr`,
so without a bridge the provider dials a path no server ever binds and every
`session new` fails with `herdr server for session "parlay" did not become
ready` (proven 2026-09-08: server listening, socket present, 40/40 dials
missed). The bridge is one symlink (machine-local, outside the repo):

```sh
ln -s ~/.config/herdr ~/Library/Application\ Support/herdr
```

Upstream fix belongs in gascity (`socketPath` should mirror herdr's own XDG
resolution instead of `os.UserConfigDir`); until a pin bump carries it, this
symlink is a prerequisite for the gc launcher on macOS. Requires herdr
≥0.7.5 on `PATH` (this box: 0.9.0).

## Safety

The Gas City supervisor is a **shared machine-wide singleton**
(`127.0.0.1:8372`, launchd `com.gascity.supervisor`). Never run state-mutating
`gc` verbs against the default `GC_HOME`; every experiment redirects **both**
`GC_HOME` and the supervisor port. Read contract §9.1 before running any `gc`
command against anything you did not create yourself.
