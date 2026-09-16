# gc@main + brain probe: the path to brain as THE bead store (2026-09-15, mini1)

> **Landed (task-svq1q):** the §3 items marked "NOT done here" are done —
> gc re-pinned to `9700d9a` ([contract](../gascity-integration-contract.md)),
> the bootstrap accepts brain (`tools/cli/internal/commands/gc_store.go`),
> and agents work the shared `parlay` family store
> ([prerequisite](../gc-prerequisite.md)). This note remains the live-probe
> evidence behind those decisions.

Task-svq1q asked for brain (trillium/brain, the federated bead store) as THE
bead store behind parlay's gc launcher. This note records the live probes
that decide the design. Base: parlay origin/main `fd56dd0`, work branch
`work-svq1q`. Toolchain: go1.26.5 darwin/arm64. gc: pin `ac6c9c6` built via
`tools/gc-build/build-gc.sh` (version `dev`); gc@main `9700d9a` hand-built to
`/tmp/gc-main/gc-main` (since removed — rebuild per §4 to repeat).
Upstream bd: `~/go/bin/bd` v1.1.0 (dev), built
`CGO_ENABLED=0 go install
github.com/steveyegge/beads/cmd/bd@v1.1.1-0.20260805093327-bf97b73749ac`
(the pin's vendored beads version). Brain bd: `~/.local/bin/bd`
`1.2.2+brain` (brain base = beads v1.2.2). dolt 2.1.10, herdr + pi on PATH,
`PARLAY_STATE_HOME` unset (default `~/.parlay`).

## 1. Pinned gc + brain: blocked, failure reproduced exactly

Scratch city (scaffold copy), `gc beads health` OK (port recorded), brain
`bd init --prefix pa --server --server-port <port>` OK, `config set
types.custom …` OK, `bd list` OK (`[]`) — then:

```
gc session new: listing sessions: search issues: search issues:
Error 1105 (HY000): column "row_lock" could not be found in any table in scope
{"schema_version":"1","ok":false,…}
```

The pin's vendored beads lib (v1.1.1-0.20260805…) queries `row_lock`;
brain-base (v1.2.2) stores don't have it. Module-cache archaeology:

| beads version | `row_lock` references | consumer |
|---|---|---|
| v1.1.1-0.20260805… (gc pin's lib) | 38 files | pinned gc — queries it |
| v1.2.2 (brain's base) | 0 | brain — stores lack it |
| v1.3.0-rc.2 (gascity main's lib) | 69 files | gc@main — see §2 |

Upstream removed `row_lock` (revision-guard era) between the pin's lib and
v1.2.2, then brought it back by v1.3.0-rc.2 with the `bd-qhn8y`
never-migrate-without-consent doctrine.

## 2. gc@main + current brain: GREEN, no brain rebase needed

`git ls-remote https://github.com/gastownhall/gascity refs/heads/main` →
`9700d9a`, whose `go.mod` vendors `github.com/steveyegge/beads v1.3.0-rc.2`.
Fresh scratch city, same recipe with the CURRENT brain binary, `session new`
via `/tmp/gc-main/gc-main`:

```
{"schema_version":"1","ok":true,"session_id":"pa-wisp-3ej",
 "session_name":"probe-m-adhoc-c519c1d9ae","template":"parlay.probe-m",…}
```

Verified live in the session directory (`session list`: `pa-wisp-3ej`,
state `active`, full `/usr/bin/env … /bin/sh -c 'sleep 120'` command line
delivered), then `session close` → `state: closed`. Two caveats, both
non-blocking:

- gc@main logs `refusing to auto-apply 10 pending schema migrations to a
  shared server database (v56 -> v66)` as a WARN and falls back to bd
  shell-outs, which work. The brain-joined store stays v56; nothing migrates
  (so old bd clients are never locked out). Native-lib mode is degraded, not
  dead — worth noting, not worth fixing here.
- The scratch run first failed with `herdr server for session "city" did
  not become ready` because mini1 lacked the documented macOS socket bridge
  (`docs/gc-prerequisite.md`): created
  `~/Library/Application Support/herdr -> ~/.config/herdr` (machine-local,
  reversible, the documented prerequisite — not a repo change). gc@main
  still resolves via `os.UserConfigDir()`, so the bridge is still required.

## 3. What this means for the design

- Brain-as-substrate needs ONLY the gc re-pin (pin → gascity main
  `9700d9a`, i.e. beads v1.3.0-rc.2 generation). No brain rebase: current
  brain (v1.2.2 base) already drives gc@main end to end.
- After the re-pin, the fork-refusal gate being built on `work-svq1q`
  (`resolveUpstreamBD`/`bdProbeFork` in `tools/cli/internal/commands/`)
  must FLIP to accept brain — the bootstrap recipe itself is unchanged
  (proven above with the brain binary verbatim).
- The re-pin is the documented procedure (`third_party/gascity/PIN` +
  `openapi.json` + `LICENSE` refresh, rebuild, contract re-verification) and
  is NOT done here — it invalidates every gc-gated test baseline and touches
  vendored files. It needs the captain's explicit word.
- Open product questions (also captain-level): should the city store be
  registered into the brain family (`brain stores add`, search-visible,
  exfiltrated) or stay a standalone brain-joined store? Do agent sessions
  operate on family stores (e.g. `task`) directly? The macbook↔mini1 sync
  (`~/brain-sync-setup`) means a registered city store would federate across
  machines — decide before registering.

## 4. Cleanup

All probe sessions closed, scratch cities/dolt servers reaped and removed,
probe gc binaries removed. Machine changes that REMAIN (all documented
prerequisites): the built pin gc (`tools/gc-build/dist/gc`, gitignored
build output), upstream bd (`~/go/bin/bd`), the herdr socket symlink.
