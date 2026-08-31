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
7c817e0640fae801631043005f1d54b17ce3e97c   (short: 7c817e064, git describe: v1.4.0-504)
```

That is genuine upstream `main` as of 2026-08-20 and builds clean.
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

## Safety

The Gas City supervisor is a **shared machine-wide singleton**
(`127.0.0.1:8372`, launchd `com.gascity.supervisor`). Never run state-mutating
`gc` verbs against the default `GC_HOME`; every experiment redirects **both**
`GC_HOME` and the supervisor port. Read contract §9.1 before running any `gc`
command against anything you did not create yourself.
