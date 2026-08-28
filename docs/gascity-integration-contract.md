# The Gas City integration contract

**Status:** binding for every unit of the Gas City adoption epic (P0–P13).
**Unit:** P0. This document blocks every other unit.
**Written:** 2026-08-28.
**Scope:** documentation and constants only. This document's own PR adds no Gas City
dependency, implements no seam, and is inert at runtime apart from one comment fix.

Later units cite this file rather than re-deriving its facts. Where a fact here disagrees
with one of the five scoping reports in the firstmate home, **this file wins** — every claim
below was re-verified against `~/code/gascity` or the parlay tree before being written down,
and §13 lists the seven places where verification came back different from the input.

`~/code/gascity` and `~/code/beads` are **read-only**. Nothing in this unit wrote to either.

---

## 1. The pinned Gas City ref

```
github.com/gastownhall/gascity @ 7c817e064
git describe: v1.4.0-504-g7c817e064
```

**Pin this. Never pin the captain's local checkout.** `~/code/gascity` is on the local branch
`progname/monolith` at `1e5229b6d`, which sits on merge `16f072610` ("take upstream for all
conflicts"). That branch **does not compile**. `7c817e064` is that merge's own parent and is a
genuine upstream commit.

### Verification commands, and their recorded results

Both builds were run out of tree. `git archive` reads the source repo and writes only to the
scratchpad — unlike `git worktree add`, `git fetch`, or `git checkout`, all of which write
into `~/code/gascity/.git` and would violate the read-only constraint.

```sh
# Materialise a ref without writing to the source repo.
mkdir -p "$SCRATCH/gcbuild"
git -C ~/code/gascity archive 7c817e064 | tar -x -C "$SCRATCH/gcbuild"

cd "$SCRATCH/gcbuild"
export PKG_CONFIG_PATH=/opt/homebrew/opt/icu4c@77/lib/pkgconfig
export CGO_CPPFLAGS=-I/opt/homebrew/opt/icu4c@77/include
export CGO_CXXFLAGS=-I/opt/homebrew/opt/icu4c@77/include
export CGO_LDFLAGS=-L/opt/homebrew/opt/icu4c@77/lib
go build ./...
```

| ref | `go build ./...` | `go build ./cmd/gc` |
|---|---|---|
| `7c817e064` (pinned, upstream) | **exit 0 — clean** | clean |
| `1e5229b6d` (captain's local HEAD) | **exit 1** | **exit 1** |

`1e5229b6d` fails two different ways depending on target:

- `./cmd/gc` — `internal/beads/sqlite_store.go:1891: numericIDSuffix redeclared` (against
  `hqstore_core.go:874`), plus `internal/beads/hqstore.go:15,16: undefined:
  beadmeta.ExpiresAtMetadataKey` / `beadmeta.HQStoreClosedAtMetadataKey`. These are artifacts
  of the local merge, not upstream defects.
- `./...` — additionally `missing go.sum entry for go.etcd.io/bbolt`, because the *committed*
  `go.sum` at `1e5229b6d` is incomplete. `~/code/gascity`'s working tree has uncommitted
  `M go.mod` / `M go.sum` that add exactly that entry, so an in-place build masks a failure a
  clean checkout hits. **Never conclude "it builds" from the captain's dirty tree.**

### The local toolchain gap, stated correctly

Homebrew `icu4c` is keg-only and unlinked on this machine. Gas City reaches ICU transitively:
Dolt → `github.com/dolthub/go-icu-regex` → its `internal/icu` cgo package.

**The load-bearing flag is `CGO_CPPFLAGS`, not `CGO_CXXFLAGS`.** `go-icu-regex`'s `icu.go`
carries `// #include "unicode/uregex.h"` in a **C** cgo preamble. `CGO_CXXFLAGS` reaches only
the package's `file.cpp`, never that preamble. With no `-I` on the C path, cgo resolves
`unicode/uregex.h` from the **macOS SDK's own copy**
(`/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/include/unicode/uregex.h`), which
has ICU symbol renaming disabled. cgo then emits unversioned `_uregex_open`, `_uregex_find`,
… while Homebrew's `libicui18n` exports `uregex_open_77` — so the link fails with
`Undefined symbols for architecture arm64` even though `CGO_LDFLAGS` was correct.

Set all four variables above. `CGO_ENABLED=0` is the alternative and also works.

**This is a local environment gap, not a Gas City defect.** Do not file it upstream.

### Upstream has moved past the pin

```sh
git ls-remote https://github.com/gastownhall/gascity refs/heads/main
```

As of 2026-08-28 this returns **`eec4a2fb625279ac62f29ff4f6a554168bc77b1a`**. The local
remote-tracking ref `upstream/main` is **stale** at `7c817e064` (dated 2026-08-20).

`git ls-remote` is the write-free drift check — it queries the remote and writes nothing
locally, unlike `git fetch`. Use it, not `fetch`, while `~/code/gascity` is read-only.

We pin `7c817e064` anyway, deliberately: it is a genuine upstream commit, it is present in
the local object store (so every unit can materialise it offline), and it is the only ref
whose build has actually been verified here. **P4 owns re-pinning.** Moving the pin requires
re-running the build verification above and re-recording the sha256 in §3.

---

## 2. The pinned `gc` version, and the skew

**The contract targets the pinned source at `7c817e064`, built from source. It does not
target any `gc` binary currently installed on this machine.** Every one of them is wrong in a
different way.

| artifact | version | `gc beads list` / `show`? |
|---|---|---|
| `~/.local/bin/gc` → `~/go/bin/gc` (**first on PATH**) | `0.15.1.trillium` | **neither — `gc beads` has only `city` and `health`** |
| `/opt/homebrew/bin/gc` | `dev` | not probed |
| `~/code/gascity/gc` (in-tree prebuilt, Jul 20) | `1.1.1` | yes |
| `7c817e064` built from source (**the pin**) | `v1.4.0-504-g7c817e064` | yes |
| `1e5229b6d` (captain's local HEAD) | `v1.4.0-511-g1e5229b6d` | does not build |

`~/.local/bin/gc` is a symlink to `~/go/bin/gc`; they are the same binary, so there are
**four** distinct artifacts, not five.

Verified by running `<path> version` on each, and `~/.local/bin/gc beads --help`, whose
`Available Commands:` block lists exactly `city` and `health`.

### What must be upgraded, and when

**Any step that depends on `gc beads list` or `gc beads show` is blocked until the installed
binary is replaced.** The binary the fleet would actually invoke today (`0.15.1.trillium`) has
no such subcommand — this is not a version-too-old problem that degrades gracefully, it is an
unknown-subcommand hard error. Tracked as `task-0k2po`.

This constrains ordering: no unit may assume `gc beads` works until that upgrade lands.

> **A note on scope.** The status seam's recommended bead topology imports
> `github.com/steveyegge/beads` directly as a Go module and never invokes `gc beads` at all,
> which sidesteps this skew entirely. That topology is not yet decided — see §6(d) and Q4.

### Operational trap — `gc` is shell-aliased on this box

The captain's zsh has `alias gc='git commit'`.

```
$ zsh -ic 'whence -w gc'     # interactive
gc: alias
gc='git commit'

$ zsh -c 'command -v gc'     # non-interactive, i.e. scripts
/Users/trilliumsmith/.local/bin/gc
```

**Scripts are unaffected — aliases are not expanded in non-interactive shells.** Only humans
are affected, and they are affected badly: an interactive `gc supervisor stop` runs
`git commit supervisor stop`.

> **Every instruction in this document aimed at a human types the absolute path to the
> binary, never bare `gc`.** Any later unit that writes human-facing runbook text must do the
> same, and must repeat this warning rather than assume the reader has seen it.

---

## 3. The vendored OpenAPI artifact

```
path:    third_party/gascity/openapi.json
source:  internal/api/openapi.json @ 7c817e064
sha256:  cc238449f10adf4434ca447e0edb6a9b5617e1c5b48210ad7df7b0f4938d61ab
bytes:   1384762   (1.32 MiB)
```

Re-check with:

```sh
shasum -a 256 third_party/gascity/openapi.json
```

**1.32 MiB is under CI's 2 MiB tracked-blob ceiling** (`.github/workflows/ci.yml:455`), with
room to spare but not unlimited room. A future pin that grows this file past 2 MiB fails the
hygiene job — that is a feature, not a bug: it forces a conscious decision rather than
silently landing a large blob.

### What the artifact says

| property | value |
|---|---|
| `openapi` | `3.1.0` |
| `info.title` | `Gas City Supervisor API` |
| `info.version` | `0.1.0` |
| total paths | **127** |
| paths under `/v0` | **126** |
| paths outside `/v0` | `/health` — exactly one |

Five SSE (`text/event-stream`) endpoints:

```
GET /v0/events/stream
GET /v0/city/{cityName}/events/stream
GET /v0/city/{cityName}/session/{id}/stream
GET /v0/city/{cityName}/agent/{base}/output/stream
GET /v0/city/{cityName}/agent/{dir}/{base}/output/stream
```

`GET /v0/events/stream` accepts **both** resume mechanisms, per its own parameter
descriptions in the spec:

- `Last-Event-ID` (header) — "Reconnect cursor (composite per-city cursor)."
- `after_cursor` (query) — "Alternative to Last-Event-ID for browsers that can't set custom
  headers."

Omitting both starts at the current supervisor position rather than replaying from zero.

**`info.version` is `0.1.0` and is not the `gc` version.** It has not moved with the CLI and
must never be used as a compatibility signal. See §4.

### What this unit does and does not establish

This unit **establishes the pinned artifact and its hash, and nothing else.** The CI drift
check that compares the vendored copy against a freshly-pinned upstream copy is **P4**. Until
P4 lands, nothing detects that this file has gone stale — treat the hash as a manual
checkpoint, not an enforced invariant.

---

## 4. The version contract — there is none. This is a NON-GUARANTEE.

**Gas City has a `requires_gc` field in its config schema. It is parsed, preserved, and never
compared against anything.**

Verified by grepping every `requires_gc` / `RequiresGC` occurrence in the pinned tree. All
eleven Go occurrences are declaration, struct copy, or round-trip test:

| location | what it does |
|---|---|
| `internal/config/config.go:850-851` | field declaration, `toml:"requires_gc,omitempty"` |
| `cmd/gc/cmd_init.go:42, 853` | mirrored struct declarations |
| `cmd/gc/cmd_init.go:881, 1049` | struct-to-struct copy, `dst.Pack.RequiresGC = src.Pack.RequiresGC` |
| `internal/config/undecoded_test.go:536-547` | asserts the *string survives parsing* — `cfg.Pack.RequiresGC == ">=0.14.0"` |
| `cmd/gc/cmd_agent_test.go:552, 604` | fixture text |

**There is no semver parse, no comparison, and no enforcement anywhere in `cmd/`,
`internal/`, or the tests.**

Gas City documents this itself. `docs/reference/specs/pack-spec.md:213`:

> | `requires_gc` | string | no | Minimum compatible `gc` version metadata. **Parsed and
> preserved; not currently enforced during load/import/doctor.** |

### Why this is worse than an absent feature

An absent feature is honest: a caller sees nothing, knows there is no version contract, and
builds its own gate. A field named `requires_gc` that round-trips cleanly through config
**looks like a working version gate** to anyone reading a `city.toml`. A pack declaring
`requires_gc = ">=0.16.0"` will load, run, and misbehave silently against `gc 0.15.1` — which
is *precisely the version first on this machine's PATH* (§2).

> **BINDING: parlay must never rely on `requires_gc` for compatibility. Any parlay unit that
> needs a Gas City version floor must implement its own check and fail loudly.**

The pattern to copy is the settled Q5b `bd` precedent: absent-or-too-old is a **named error
with an install pointer**, at the verbs that need it, never a silent degrade. `parlay doctor`
must assert a *working* `gc` of a known version, not a merely present one — a locally-broken
checkout (§1) must fail at the tool boundary, not at spawn time.

---

## 5. The integration mode: HYBRID

**Decision:**

- **CONTROL verbs → shell out to `gc <verb> --json`.**
- **LIVENESS and EVENT STREAMS → the typed `/v0` HTTP + SSE API.**

### Why, with citations

**Shell-out is correct for control** because every session verb takes `--json`, there is a
global `--json-schema string[="manifest"]` flag, and even *failures* are typed. Probed
directly, with `GC_HOME` and the supervisor port redirected:

```
$ gc config show --json          # outside a city
{"schema_version":"1","ok":false,"error":{"code":"command_failed","message":"…"}}
```

A machine-readable refusal is a usable contract. Control verbs are low-frequency and
human-triggered, so per-call cost is affordable.

**Shell-out is wrong for liveness and streams** because of its floor. Measured on this box
against the in-tree `1.1.1` binary, 15 runs each, `GC_HOME` redirected:

| command | min | median | max |
|---|---|---|---|
| `/bin/echo` (fork+exec floor) | 2.5 ms | **2.8 ms** | 4.1 ms |
| `gc version` | 30.6 ms | **34.5 ms** | 39.0 ms |
| `gc config show --json` | 32.2 ms | **33.8 ms** | 37.7 ms |

Roughly **12× the bare process floor**, and it is a floor — it buys zero work. The HTTP
`/health` figure of **~1.1 ms** is carried forward from the topology report and was **not**
re-measured here, because doing so means sending traffic to the captain's live supervisor.
Treat 1.1 ms as indicative; the ratio, not the absolute number, carries the decision, and the
ratio is ~30× in HTTP's favour even if the HTTP figure is off by 3×.

Polling liveness for N agents at 34 ms per probe does not scale, and event streaming through
repeated process spawns has no resume story at all. The HTTP API has **durable cursors and
`Last-Event-ID` resume** (§3) — that is the capability shell-out cannot provide at any price.

> Per project CLAUDE.md, elapsed-time measurements are **decision inputs, never test
> assertions.** No unit may write a test that asserts on these numbers.

### The Go-library import mode is CLOSED

Not a preference — a language rule.

```
$ ls pkg/
eventexport                       # exactly one package, at the pinned ref
$ find internal -name '*.go' ! -name '*_test.go' -exec dirname {} \; | sort -u | wc -l
160
```

Go's `internal/` visibility rule blocks any import of `…/gascity/internal/…` from code not
rooted at `…/gascity/`. **No `replace` directive changes this** — a `replace` changes where
source is fetched from, not import-path visibility. parlay's `tools/cli` is its own module
(`module github.com/trillium/parlay/tools/cli`), so it is not rooted there and never can be.

`cmd/gc/` is worse, not better: it is `package main`, which is strictly less reachable than
`internal/`. Note that several load-bearing things live there — the runtime registry
(`cmd/gc/runtime_registry.go`), the reconcile loop (`cmd/gc/city_runtime.go`) — so an
`internal/`-only survey of Gas City's capabilities will miss them. Grep `cmd/` too.

**Do not re-litigate this mode.** Re-verify the `ls pkg/` output when the pin moves; that is
the only thing that could reopen it.

### The fourth mode, recorded as a documented alternative — Gas City PUSHES to parlay

No brief listed this. It is not the chosen mode, but it is real and it inverts the dependency
direction, so later units should know it exists before designing around its absence.

Gas City can push events **to** parlay via `[events.export]`
(`cmd/gc/event_export.go`). The supervisor watches its own per-city providers, projects each
event to an envelope-only shell, and POSTs batches to a configured endpoint. From its own
doc comment:

> "It is opt-in: with no endpoint the supervisor ships nothing. … It runs in its own
> goroutine, holds its cursor on sink failure, and applies backpressure rather than blocking
> event [recording]."

Startup is **fail-closed**: arming is deferred until the exporter loads a durable cursor, so
a corrupt cursor leaves the feature off rather than half-on.

The egress surface is deliberately narrow (`pkg/eventexport/project.go`):

- `const SchemaVersion = 6` (`:82`) — stamped on every batch.
- `var allowedTypes = map[string]bool{…}` (`:111`) — **exactly 22 entries**, default-deny.
- The map is **unexported**; only `func IsAllowed(typ string) bool` (`:158`) is exported, so
  no importer can widen the egress surface at runtime.

**Coverage: 22 of Gas City's ~93 event types.** That is the reason this is an alternative and
not the choice — a push feed that structurally cannot carry two thirds of the event space
cannot be the primary event seam. It remains attractive for a narrow, well-defined subset
because it removes parlay's polling cost entirely and inverts who must be up first.

If a later unit wants it, it is additive to the hybrid decision, not a replacement for it.

---

## 6. The agent record owner — DECIDED, and binding

> # THE SPAWN SEAM OWNS THE AGENT RECORD. THE STATUS SEAM ATTACHES TO IT.

The spawn seam and the status seam have the same natural target — Gas City's `session` bead.
If both land independently there are **two beads per agent and a reconciliation problem that
neither seam owns.** This decision exists to make that outcome impossible. It is settled here
and no later unit reopens it.

### (a) The spawn seam creates exactly ONE bead per agent

The spawn seam creates exactly one Gas City session bead per agent, and is the **sole
writer** of that bead's identity fields — id, agent name, worktree, project, bead binding,
creation time. One writer, one creation point, no exceptions.

### (b) The status seam writes STATE, and never creates

The status seam writes **state** onto that existing bead and appends **events** to the event
log. It **never creates the bead.**

> **An absent bead is an error to REPORT, not a bead to mint. Minting on read is exactly how
> a duplicate arises.**

If the status seam looks up an agent's bead and finds nothing, the correct behaviour is to
report the inconsistency and stop — not to helpfully create what it expected to find. A
status seam that can create beads is a second creation point, and the whole point of this
decision is that there is only one.

### (c) The existing fail-open rule stands, and is now load-bearing

`tools/cli/internal/identity/worklink.go:75-85`'s fail-open rule is unchanged by this decision
and becomes **load-bearing** under it.

What the code says today is narrower than the rule this section needs, so both are stated.
`BoundWorkItemClosed`'s own doc comment (`:70-74`):

> "It returns closed=true **ONLY on an affirmative closed status**; a missing binding, an
> unresolvable item, or **any store error yields closed=false (fail open)**."

That is a rule about **closure**. §6 extends the identical posture to **creation**:

> **A lookup that merely FAILED is not evidence of a closed or absent bead, and must not
> trigger creation.**

The extension is new; the posture is not. `worklink.go` already refuses to read a store error
as an affirmative answer, and this decision requires every seam touching the agent record to
do the same.

The three states are distinct and must stay distinct in code:

| observation | meaning | action |
|---|---|---|
| bead found | normal | attach, write state |
| bead definitively absent | inconsistency | **report** (per (b)) |
| lookup failed / errored | **unknown** | fail open — do nothing, do not create |

Collapsing "the query errored" into "there is no bead" converts a transient store outage into
a duplicate-bead storm. This is the same failure shape as robots-me7m on the parlay side,
where "the relay did not answer" had to be kept distinct from "the relay says no" — see
`crew_state.go`'s `enrollment` type, whose third case exists for exactly this reason.

### (d) Ownership is settled. SUBSTRATE is NOT.

**This decision fixes *who writes the record*. It does not choose what the record is stored
in.** No unit may assume a bead backend until Q4 closes.

Q4 has been narrowed to two candidates:

1. A JSONL archive.
2. Direct import of `github.com/steveyegge/beads` as a Go module, at a parlay-controlled
   `beadsDir`, with no `gc` binary and no PAI federation involved. (The status scope
   recommends this. It also makes §2's `gc beads` skew irrelevant.)

**`GC_BEADS=file` has been ELIMINATED on evidence.** It is not a cheap adoption path because
it is not a beads database at all: `FileStore` persists **gascity's own JSON structure** —
`fileData{Seq, Beads, Deps, Revisions, …}` (`internal/beads/filestore.go:16`) — written by whole-file
`MarshalIndent` and rename. **`bd` cannot read it.** It is readable only by gascity's
`internal/beads`, which parlay cannot import (§5). Adopting it would produce a store that is
not a beads database and that only a foreign binary can read.

Relatedly, and contrary to a widely-repeated claim: `GC_BEADS=file` **does** take a flock.
`OpenFileStore` installs `NewFileFlock(path + ".lock")` whenever the filesystem is `fsys.OSFS`
(`internal/beads/filestore.go:183-185`), and `FileFlock.Lock()` is a **blocking**
`syscall.Flock(fd, LOCK_EX)` with no timeout and no deadline (`internal/beads/flock.go:36`).
The lock-free `nopLocker` applies only to non-OS filesystems, whose sole in-tree
implementation is the test fake.

---

## 7. The ordering rule

> # READ BEFORE WRITE. OBSERVE BEFORE CONTROL.

**This is a rule, not a suggestion.** A unit that violates it is rejected on ordering grounds
regardless of its own quality.

```
P0  contract (this document)
 └─ P1  Gas City client package
     └─ P4  typed client from vendored openapi.json + CI drift check
         └─ P6  events seam            [SHADOW]
             └─ P7  liveness seam      [SHADOW]
                 └─ P11 degraded-mode contract
                     └─ P9  spawn seam
                         └─ P10 teardown ordering
                             └─ P12 install handoff
                                 └─ P13 flip default, delete legacy path
```

### Why this order and not the obvious one

Every brief written before the topology scope tacitly put process-launching first, because a
half-built `--gascity` switch already exists and looks like the natural entry point.

**That is the wrong first move.**

**Spawn is the one seam that cannot run in shadow.** You cannot half-launch a process. Every
other seam — events, liveness, status — can run *alongside* parlay's own implementation, with
disagreements logged and nothing depending on the answer, while parlay is still driving
itself. That is free evidence about whether Gas City's model actually matches parlay's, paid
for with zero risk.

Leading with spawn spends the entire risk budget before one piece of evidence has been
collected. If Gas City's liveness oracle disagrees with signal-0 in 5% of cases, you want to
know that from a shadow log, not from an agent that failed to launch.

**P11 before P9** for the same reason at smaller scale: "what happens when Gas City is down"
must be defined *before* anything depends on Gas City being up.

**P9 and P10 must not land in the same PR** — see §8.

Everything before P13 reverses by flipping a switch. P13 is the only flag-day.

---

## 8. The collision register

Points where two units touch the same thing. Each is a real serializer, not a style note.

### 8.1 `gascityAlive` — one function, and the coupling is looser than reported

`tools/parlay-bin/gascity_spawn.go:220`.

```go
func gascityAlive(stateDir string) bool {
	pid := readPID(stateDir)
	if pid == 0 { return false }
	if pidAlive(pid) { return true }
	// Stale pid file — clean it up so a later spawn doesn't see a false "already running".
	_ = os.Remove(pidFilePath(stateDir))
	return false
}
```

**Correction to the input reports.** They state this function is read by the stop path, by
`parlay sweep`, **and** by `parlay status` — "three seams, one function," and call it the
highest-collision point in the migration. **That is not true of the tree as it stands.**

Verified: `gascityAlive` has exactly **two callers, both in the same file** — `:188` (the
`gascity-ping` verb) and `:237` (inside `gascityStop`). It is an unexported function in
`package main` of `tools/parlay-bin`, which is a **separate Go module**
(`github.com/trillium/parlay/tools/parlay-bin`) from `tools/cli`
(`github.com/trillium/parlay/tools/cli`) where `sweep`, `status`, `stale`, and `crew-state`
live. It is not importable from there, and grep finds **no `gascity` reference at all** in
`sweep.go`, `status_verb.go`, `stale.go`, or `crew_state.go`. The one repo-wide mention
outside the file is `bin/parlay-spawn:1445`, and that is error-message text, not a call.

**The ordering rule survives the correction, for a better-stated reason.** `gascityAlive` has
a **side effect**: it deletes the pid file when it observes a dead pid. It is simultaneously a
liveness predicate and a state mutation. P9 (spawn) and P10 (teardown) both change what
"alive" means for a session, and both would inherit that mutation.

> **BINDING: P9 and P10 must not land in the same PR.** A predicate that mutates state, being
> changed by two units at once, is how a stale-pid race ships.

Any later unit that widens `gascityAlive`'s reach — for instance by making `parlay sweep` exec
`parlay-bin gascity-ping` — **creates** the three-seam collision the reports describe. Doing
so is a decision, not a refactor, and must be called out explicitly in that unit's PR body.

### 8.2 `crew-state` is a FROZEN WIRE CONTRACT

Consumed by `parlay sweep` and `parlay stale`. **Four of its guards each came from a real
incident.** Changing any of it silently changes sweep's hold-guard behaviour.

Exit codes (`tools/cli/internal/commands/crew_state.go:96-101`) — frozen:

```go
const (
	ExitCrewNoStatus         = 3  // enrolled, nothing usable on disk yet ("no news")
	ExitCrewNotEnrolled      = 4  // relay answered and does NOT list this agent ("gone")
	ExitCrewStatusUnreadable = 5  // a status file exists but cannot be read or parsed
	ExitCrewRelayUnreachable = 6  // relay lookup failed AND no status to fall back on
)
```

Code `6` is documented in-file as *"the ONLY code that means crew-state has no opinion."*
Nothing may be added to that set.

Source suffixes (`crew_state.go:233-244`) — frozen strings:

| value | meaning |
|---|---|
| `status` | the relay confirmed enrollment |
| `status-unenrolled` | qualified: relay did not list the agent |
| `status-degraded` | qualified: relay unreachable, falling back to disk |

> **BINDING: the status seam may not change these exit codes or these three strings.** If it
> needs to express something new, it adds a *new* channel; it does not overload an existing
> one.

### 8.3 The event recorder's 250 ms lock is a SHARED WRITE BUDGET

`internal/events/recorder.go`:

```go
recordFlockTimeout       = 250 * time.Millisecond   // :33
recordFlockRetryInterval = 5 * time.Millisecond     // :38
```

`lockRecorderFile` (`:330-343`) spins `LOCK_EX|LOCK_NB` every 5 ms until the 250 ms deadline.
Gas City serialises **every writer in a city** through one file on that budget.

**The topology change is the danger.** Today parlay is *N files × 1 writer each*. After the
lift it is *1 file × N writers*, with two seams' producers — events and status — competing
for the same 250 ms window. The budget does not grow when the writer count does.

> **BINDING: the events seam owns this budget.** The status seam inherits whatever it decides
> and may not independently add high-frequency producers. See §9.1 for what happens when the
> budget is exhausted — it is worse than backpressure.

### 8.4 The typed event-name registry is a shared namespace

`RegisterPayload(eventType string, sample Payload)` / `RegisteredPayloadTypes()`
(`internal/events/payload.go:51, 79`).

> **BINDING: the events seam (P6) owns the event-name registry. The status seam inherits it.**

Two seams adding names independently is how a name collision ships. P6 is the gate for wave 3
for precisely this reason — four decisions ride on it (recorder ownership, rotation policy,
SSE contract, and this registry) and the status seam inherits all four.

### 8.5 HARD BOUNDARY — parlay's ingress must not widen

`POST /api/chat/events` is parlay's **out-of-process ingress seam**
(`packages/go-server/internal/handlers/events_ingress.go`). Its allowlist is
**one name per real producer**, and today that is exactly one entry:

```go
"tool_event": true,      // :73
```

From the file's own comment at `:24-27` — *"The allowlist: one name per real producer, and
nothing else … That is `tool_event` alone — the TS tool tailer."*

> **BINDING: no Gas City unit may widen `POST /api/chat/events` to carry the panel-aiming
> events.** Status is panel-aiming. This ingress is not the seam for it.

And the guard registration, which is a **dual-plane** requirement:

> **BINDING: any new `/api/chat` route must be registered in BOTH:**
> - `GUARDED_CHAT_PATHS` — `packages/server/src/guard/paths.ts:51` (Bun plane)
> - `internal/guard.GuardedPaths` — `packages/go-server/internal/guard/guard.go:127` (Go plane)
>
> **A new route is unguarded until you do.** Registering one plane and not the other is a
> silent hole.

Both planes also guard whole subtrees — `GUARDED_PREFIXES` (`paths.ts:135`) and the Go
`guardedPrefixes`, covering `/api/chat/agents/`, `/api/chat/plugin/`, `/api/debug/` — so
anything added *under* those is guarded before you get there. `JSON_EXEMPT_PATHS`
(`paths.ts:196`) is a **closed three-member list**; do not grow it one bug report at a time.

A route is guarded by what its handler **does**, not by its HTTP method. `GET /subscribers` is
guarded because it hands out identifiers.

---

## 9. The irreversibility register

> ## Do not run any of these casually.
>
> Everything in the 39-unit plan reverses by flipping a switch or reverting a PR — **except
> what is listed here.** Read this section before running any `gc` command against anything
> you did not create yourself.

### 9.1 THE SUPERVISOR SINGLETON IS SHARED, NOT PER-WORKTREE

> ### This is the single most dangerous fact in this document.

Gas City runs **one supervisor per machine**, on `127.0.0.1:8372` by default, under the
launchd label `com.gascity.supervisor` (`cmd/gc/cmd_supervisor_lifecycle.go:1294`).

**`gc supervisor stop`, `gc supervisor reload`, or `gc start` against the default home acts on
the captain's RUNNING city — the one with the mayor session in it.** Not a copy of it. Not a
test instance that resembles it. It. There is no worktree isolation, no per-checkout scoping,
and no confirmation prompt. This is the same hazard class as a broad `pkill`, and parlay has
already been burned by exactly this shape twice — the relay is a per-runtime-dir singleton
bound to ONE server (robots-buu8), and the canonical runtime dir is RESERVED because a
wrong-server relay in it is a fleet outage (robots-93xu).

> ### BINDING, in the strongest terms this document has:
>
> **EVERY experiment involving `gc` MUST redirect BOTH `GC_HOME` AND the supervisor port.
> Neither alone is sufficient.** `GC_HOME` alone still leaves the process contending for
> `:8372`. The port alone still reads and writes the captain's city state.
>
> **No unit past P3 may run outside a sandbox that also redirects `PARLAY_STATE_HOME`,
> `PAI_DIR`, `HOME`, and `PARLAY_DATA_DIR`** — per project CLAUDE.md, `PARLAY_DATA_DIR`
> covers only what goes through `paths.ts`, and the tailers replay live agent turns into
> whatever hub answers `PARLAY_HUB_URL`. Use `examples/bootstrap-sandbox.sh` rather than
> hand-rolling the isolation.
>
> **Never target port `:31337`** — the captain's live Pulse instance (project CLAUDE.md).

The probe run for §5 of this document redirected `GC_HOME` to a scratch directory and the
supervisor port to `18372`, and invoked only `version`, `--help`, `config show --json`, and
`session list` — none of which start, stop, or reload anything. That is the template.

### 9.2 `gc supervisor install` mutates a live service

`installSupervisorLaunchd` (`cmd/gc/cmd_supervisor_lifecycle.go:1770`) writes
`~/Library/LaunchAgents/com.gascity.supervisor.plist` and loads a KeepAlive job.

**Running it while `com.gascity.supervisor` is already loaded IS a live-service mutation.**
Verified by reading the function: unless the rendered content is byte-identical to the
existing plist *and* `supervisorAliveHook() != 0` (the no-op fast path), it unconditionally
runs `launchctl unload` on the existing job and then reloads it. The captain's running
supervisor is stopped and restarted.

It has exactly **one** refusal guard, and it is narrow: if the existing plist references a
different `gc` binary than the one now resolving, it refuses rather than overwrite — *unless*
`--force`. There is rollback on load failure
(`restorePreviousSupervisorLaunchdInstall`), which limits the blast radius of a *failed*
install but does nothing about a successful one that restarted a service you did not intend
to restart.

**P12 owns this verb. No earlier unit runs it.**

### 9.3 Correction: launchd and systemd uninstall are SYMMETRIC

The input reports and the P0 brief warn that `uninstall` refuses to remove an active
*systemd* unit while the launchd path is a separate function whose safety must not be
assumed. **The instruction to verify was right; the warning turns out not to apply at the
pinned ref.**

Both were read. Both refuse:

| | function | active check | behaviour when active and socket unavailable |
|---|---|---|---|
| launchd | `uninstallSupervisorLaunchd` `:1841` | `supervisorLaunchdActive(...)` | prints *"…is active but the control socket is unavailable; run 'gc supervisor start' to re-adopt sessions, then retry uninstall"*, **returns 1** |
| systemd | `uninstallSupervisorSystemd` `:2113` | `supervisorSystemctlActive(...)` | same shape, same message, same refusal |

They are genuinely separate functions, so the reports were right that symmetry could not be
*assumed* — but at `7c817e064` the symmetry is real.

**One thing that is still true and matters more:** `uninstall` is not passive. When the
control socket **is** available it performs a socket-protocol stop of the running supervisor
before removing the plist. It stops the service deliberately — the refusal path exists only
for when it *cannot* stop it cleanly. "Uninstall refuses when active" does not mean "uninstall
never touches a live service."

**Re-verify this table whenever the pin moves.** It is two separate code paths; they can
diverge again.

### 9.4 SILENT DATA LOSS — `FileRecorder.Record` drops events and tells no one

`internal/events/recorder.go:236`. Verbatim:

```go
func (r *FileRecorder) Record(e Event) {
	...
	fd := int(r.file.Fd())
	if err := lockRecorderFile(fd, r.path); err != nil {
		fmt.Fprintf(r.stderr, "events: lock: %v\n", err) //nolint:errcheck // best-effort stderr
		return
	}
	...
}
```

**`Record` returns nothing.** On a 250 ms lock timeout (§8.3) it writes one line to
`r.stderr` and returns. The event is gone. The caller cannot know.

**This is invisible in exactly parlay's configuration.** parlay's spawned processes
deliberately redirect stdio to `/dev/null` — see `tools/parlay-bin/gascity_spawn.go`'s
detached-child design. A writer whose stderr goes nowhere drops events **completely
silently**. The one signal the code emits is the one parlay throws away.

The package documents the posture up front (`internal/events/events.go:1-10`): *"Recording is
best-effort: errors are logged to stderr but never returned to callers."* There is also no
fsync.

**The fix is in the same file.** `AppendBatch` (`:274`), from its own doc comment:

> "AppendBatch strictly appends a complete event batch under one mutex and one cross-process
> file lock. It assigns contiguous sequence numbers, prepares the complete JSONL payload
> before writing, performs exactly one write, and **returns every lock, marshal, write, or
> unlock failure to the caller.**
>
> **Unlike Record, AppendBatch is not best-effort** and does not auto-rotate. It is intended
> for bounded operator-authored snapshots whose caller must know whether the complete append
> succeeded."

> **BINDING: parlay uses `AppendBatch` and checks the error. `Record` is forbidden on any
> path where a lost event is a correctness problem.**

Note the tradeoff `AppendBatch` names honestly: it does **not** auto-rotate. The events seam
(P6) owns rotation policy as a consequence — that is one of the four decisions §8.4 says ride
on P6.

Filed for the captain as `brain-gpqwa`.

### 9.5 Gas City's two reclaimers disagree — but the danger is narrower than reported

> **Correction to the input reports.** The liveness scope calls this "THE BLOCKING FINDING"
> and describes the weaker gate as reaping committed-but-never-pushed work. **On verification
> that is overstated.** The divergence is real, deliberate, and documented; the consequence is
> a disappearing *checkout*, not destroyed *commits*. The correct conclusion for parlay is
> unchanged and is if anything better supported — see the binding rule at the end.

Gas City has two worktree-reclaiming paths that ask the same question with **different
commands**.

`internal/git/git.go`:

```go
func (g *Git) HasUnpushedCommitsResult() (bool, error) {          // :164  — STRONGER
	out, err := g.run("log", "HEAD", "--oneline", "--not", "--remotes")
	...
}

func (g *Git) HasUnreachableCommitsResult() (bool, error) {       // :196  — WEAKER
	out, err := g.run("log", "HEAD", "--oneline", "--not", "--branches", "--remotes", "--tags")
	...
}
```

**The whole difference is `--branches` and `--tags`.**

Walk it through with a parlay agent branch — `parlay/<agentID>`, committed locally, never
pushed:

| gate | question it actually asks | answer for our branch | verdict |
|---|---|---|---|
| `HasUnpushedCommitsResult` | is HEAD reachable from any **remote**? | no → **has unpushed = true** | refuses to reap |
| `HasUnreachableCommitsResult` | is HEAD reachable from any **local branch, remote, or tag**? | yes, `parlay/<agentID>` **is** a local branch → **unreachable = false** | reaps the worktree |

`--branches` makes the branch vouch for itself. Committed-but-never-pushed work on a local
branch is precisely parlay's normal state for an in-flight agent, and the weaker gate reads it
as safe to remove.

The two callers disagree: the bead worktree reaper uses the **weaker** gate
(`cmd/gc/bead_worktree_reaper.go:303`, default off), and the session worktree pruner uses the
**stronger** one (`cmd/gc/session_worktree_prune.go:102`, `:177`, default **on**).

### Why this is deliberate, and what it actually costs

Gas City's own doc comment (`internal/git/git.go:182-195`) states the reasoning, and it is
sound:

> "…it is **deliberately narrower** than HasUnpushedCommitsResult: `git worktree remove`
> deletes the checkout, **not refs/heads**, so commits a local branch still reaches survive
> the removal.
>
> The distinction is load-bearing for merge workflows that delete the branch from the remote
> after merging. Once the remote branch is gone — and once a squash-merge has given the merged
> change a different SHA on the target branch — no remote-tracking ref reaches the worktree's
> HEAD ever again, so **HasUnpushedCommitsResult reports true permanently even though nothing
> is at risk. Callers gating destructive cleanup on that answer never clean anything up.**"

Verified against the code:

- **Neither reclaimer deletes a branch.** Grep finds no `branch -D`, `DeleteBranch`, or
  equivalent in `bead_worktree_reaper.go` or `session_worktree_prune.go`. The reaper actually
  *records* the branch name in its decision (`bead_worktree_reaper.go:316`).
- The reaper is **fail-closed on probe error** — `unreachableErr != nil` protects the tree,
  with the in-code justification *"an errored probe proves nothing, and treating it as a clean
  answer would fail open."*
- It gates on `HasUncommittedWork` and `HasStashes` **as well**, so uncommitted work and
  stashes are separately protected.

**So the accurate statement is:** the weaker gate can remove a *checkout* whose commits live
only on an unpushed local branch. The commits are **not** destroyed — the branch ref survives
in the common git dir and the work is recoverable with `git worktree add` on that branch. That
is disruptive and surprising, but it is not silent data loss. Genuine loss needs a *second*
actor to delete the branch ref afterwards.

Gas City is also solving a real problem here that the stronger gate genuinely has: after a
squash-merge with the remote branch deleted, `HasUnpushedCommitsResult` returns true forever
and blocks all cleanup.

> ### BINDING: parlay's own `isContentLanded` must stay UNCHANGED.
>
> `tools/cli/internal/commands/teardown.go:62`. It is not equivalent to either Gas City gate
> and must not be "harmonised" with them. It:
>
> 1. resolves the default branch from `refs/remotes/origin/HEAD`;
> 2. **refreshes the remote-tracking ref first** — "the whole point of this check is work that
>    landed upstream after the worktree last synced, which a stale `origin/<branch>` cannot
>    see" — best-effort, so an offline teardown degrades rather than refusing;
> 3. prefers the **remote-tracking** ref, because that is what "landed" actually means, and
>    falls back to the local branch only for a repo with no origin at all;
> 4. compares **trees**, via `<ref>^{tree}`, not two-arg `git merge-tree` (which is not a
>    predicate — project CLAUDE.md, robots-ceon).
>
> Take from Gas City the **gate ordering and the fail-closed posture**. Keep parlay's
> `hasUnpushed` + `isContentLanded` exactly as they are. P10 may reorder; it may not replace.

**And note which direction the lesson runs.** The problem Gas City's weaker gate exists to
solve — "a squash-merged branch whose remote ref is gone looks unpushed forever, so cleanup
never runs" — is one parlay **already solves better**. `isContentLanded` does not approximate
the answer through reachability at all; it refreshes the remote-tracking ref and compares
**trees**, asking *"is this content already in the default branch"* directly. That answers
correctly for a squash-merge (same tree, different SHA) **without** the false-safe that
`--branches` introduces.

parlay's gate is the stronger of the three. Do not weaken it to match either Gas City gate,
and do not treat this section as a request to adopt `HasUnreachableCommitsResult`.

---

## 10. Translation table

Gas City vocabulary ↔ parlay vocabulary. **Later units cite this instead of re-deriving it.**
Where the mapping is lossy, the Notes column says so — those are the rows that cost time.

| Gas City | parlay | Notes |
|---|---|---|
| **session** | **agent** | The core mapping. A Gas City session is bead-backed and survives supervisor restart; a parlay agent is a directory under `~/.parlay/agents/<id>/`. |
| session **bead** | agent record / `identity.md` frontmatter | parlay's is a plain file whose *absence* is the documented failure mode of robots-6xq7. Gas City's is a store row. §6 makes the spawn seam its sole writer. |
| **bead** | **task** (federated store item) | Same word, different systems. A Gas City bead is a row in its own store; a parlay `task-…` is a bead in the PAI federation. `parlay spawn --bead <id>` binds the latter. Do not conflate. |
| **city** | *(no parlay equivalent)* | A city is a config-plus-state root: `city.toml` + `.gc/`. parlay has no such scoping concept. Every city-scoped `gc` verb refuses outside one. |
| `GC_HOME` | `PARLAY_STATE_HOME` / `PARLAY_DATA_DIR` | Loosely analogous. **Not interchangeable, and redirecting one does not redirect the other** — §9.1. |
| **supervisor** | *(no parlay equivalent)* | parlay has **no supervisor at all** — zero `.plist` in the repo. Gas City's is a machine-wide singleton (§9.1). |
| **runtime provider** | **launcher** | `PARLAY_SPAWN_LAUNCHER` selects parlay's; `cmd/gc/runtime_registry.go` registers Gas City's. |
| `herdr` provider | the `herdr` launcher (default) | **Both shell out to the same `herdr` binary.** This is the reason the spawn lift is L and not XL. |
| `subprocess` provider | the `gascity` launcher | Naming collision — parlay's `gascity` launcher is a from-scratch port of subprocess semantics and contains **no Gas City code** (§11). Neither has an input-injection channel. |
| **event** `Seq` | **cursor** | Gas City: monotonic `Seq` in `.gc/events.jsonl`, exactly-once per watcher via `Watch(ctx, afterSeq)`. Over HTTP the same position is `Last-Event-ID` / `after_cursor` (§3). |
| `.gc/events.jsonl` | `~/exchange/chat-history.jsonl` | **Not equivalent.** parlay's is live history — do not clobber. Gas City's is an append-only event log with 256 MiB gzip rotation. |
| **Nudge** | *(nearest: `parlay send`)* | **Lossy and dangerous.** `parlay send` is a chat POST that the agent's own `parlay listen` loop receives. Gas City's `Nudge` is terminal injection. They are not the same operation and must not be mapped 1:1. |
| `ErrNudgeSubmitUnconfirmed` | *(no equivalent)* | parlay verifies **startup**, not **steering**. A capability parlay would gain. |
| `Provider.Stop` | `gascity-stop` / teardown | Gas City's is idempotent (nil if absent). parlay's is SIGTERM → 100 ms poll to a 5 s deadline → SIGKILL. |
| `IsRunning` vs `ProcessAlive` | registry ∩ process table | Gas City distinguishes "the session record says running" from "a process is on the table". parlay's rule is the intersection — see project CLAUDE.md, robots-jkwc. |
| `ListRunning(prefix)` | `parlay status` / `crew-state` | Gas City returns names; parlay's returns a verdict with a **frozen** exit-code contract (§8.2). |
| `AddressDirectory.ResolveAddress` | agent-id lookup | Gas City **refuses** an ambiguous address rather than picking a winner. parlay's `parlay send` needed robots-ngg5 to stop minting phantom channels — same bug class, already solved on the Gas City side. |
| `PreStart` | worktree setup (`bin/parlay-spawn:920`) | "Failures abort startup so agents never launch into an unprepared workDir." |
| `SessionSetup` | the `CLAUDECODE` unset block (`:1218-1238`) | Semantically identical; different insertion point. |
| `ReadyPromptPrefix` / `ReadyDelayMs` | the `READY_$$` handshake (`:1218-1238`) | parlay's bespoke echo trick becomes configuration. The `$`-literal vs `$`-expanded distinction is deliberate — preserve the *intent*, not the mechanism. |
| `requires_gc` | *(nothing — do not use)* | **Parsed and never compared.** §4. |
| `[events.export]` | `POST /api/chat/events` | Superficially symmetric, opposite directions. Gas City's is opt-in egress, 22 of ~93 types, default-deny. parlay's is ingress with a one-name allowlist that **must not widen** (§8.5). |

---

## 11. The stale comment block at `tools/parlay-bin/gascity_spawn.go`

**Corrected in this PR. Not renamed, not restructured — that is P9's job.**

### The file's name is residue

`tools/parlay-bin/gascity_spawn.go` is **418 lines of parlay's own process supervision and
contains zero Gas City code.** It mentions "gascity" 58 times; the only reference to the
actual project is line 8, inside the comment block, describing an import that was never made.

The flag `--gascity`, the value `PARLAY_SPAWN_LAUNCHER=gascity`, the `config.toml [spawn]
launcher` key, and the project CLAUDE.md line describing "the `gascity` launcher" all imply an
integration that **does not exist**. Anyone scoping this seam by reading the flag name reaches
a wrong conclusion. At least one scoping worker nearly did.

**Renaming is P9's, and it is not cosmetic** — the flag, the env value, the config key, and
the docs move together or not at all.

### What was wrong, clause by clause

The block rejected shelling out to `gc` on four grounds. The first half of the comment — the
Go `internal/`-visibility argument — is **correct and re-verified** (§5). The rejection of
shell-out is what had gone stale:

| clause | verdict | evidence |
|---|---|---|
| "`gc` … requires a city.toml" | **partly true** | True for city-scoped verbs; `gc version` and `gc --help` run fine outside one. Verified by probe. |
| "… a dolt DB" | **false as a runtime requirement** | `gc version` and `gc --help` exit 0 with no database present. |
| "… k8s client wiring" | **false as a runtime requirement** | Compiled in, not required to invoke. |
| "does not even build in this environment (missing system lib for a CGO dolt dependency)" | **false** | Upstream `7c817e064` builds clean (§1). The failure was keg-only `icu4c` (a local gap) plus the captain's local merge — and the dependency is `go-icu-regex`, reached transitively via Dolt, not "a CGO dolt dependency". |

A working prebuilt `gc 1.1.1` exists in-tree and answers correctly, with `--json` on every
session verb and a global `--json-schema` flag. **"Cannot build from source here" was never
"cannot use"** — and it was not even true.

The comment now records the current state and points here.

---

## 12. What this unit does NOT do

Stated so later units do not assume otherwise:

- **No Gas City dependency is added to any `go.mod`.** Verify with `git diff` on this PR.
- **No seam is implemented.** No client package, no flag, no adapter.
- **No behaviour changes anywhere.** This PR is inert at runtime apart from the comment fix
  in §11.
- **No drift check exists yet** for the vendored `openapi.json`. That is P4 (§3).
- **The bead substrate is not chosen.** §6(d). Q4 is open.
- **The HTTP latency figure is carried forward, not re-measured** (§5).
- **Nothing was written to `~/code/gascity` or `~/code/beads`.** Both stayed read-only; the
  two builds ran out of tree via `git archive` into a scratch directory.

---

## 13. Where verification disagreed with the input

The five scoping reports are excellent and were treated as input, not scripture. Seven claims came back different. **Three of them change what a later unit should do.**

| # | Claim as given | What is actually true | Impact |
|---|---|---|---|
| 1 | `7c817e064` **is** `upstream/main` | It **was**. `git ls-remote` returns `eec4a2fb6…` for `refs/heads/main` as of 2026-08-28; the local remote-tracking ref is stale. | **Material.** "Pin upstream/main" is ambiguous now. §1 pins the commit explicitly and names the write-free drift check. |
| 2 | keg-only `icu4c` needs `PKG_CONFIG_PATH` and **`CGO_CXXFLAGS`** | **`CGO_CPPFLAGS`** is the load-bearing flag. The cgo preamble in `go-icu-regex`'s `icu.go` is compiled as **C**; `CGO_CXXFLAGS` never reaches it, cgo silently picks up the macOS SDK's own `uregex.h`, and the link fails on unversioned symbols. | **Material.** Following the given recipe fails with a confusing linker error. §1 has the corrected four-variable recipe. |
| 3 | `uninstall` refuses an active **systemd** unit; the launchd path is separate, so do not assume symmetric safety | They are separate functions, but at `7c817e064` they are **symmetric** — both check active, both refuse with the same message and exit 1. | Reduces a warned-about risk. §9.3. The instruction to verify was correct; re-verify when the pin moves. |
| 4 | `gascityAlive` is read by the stop path, `parlay sweep`, **and** `parlay status` — "three seams, one function" | **Two callers, both in the same file.** It is unexported in `package main` of a *different Go module* from `sweep`/`status`; no `gascity` reference exists in those files. | Corrects the stated reason. §8.1 keeps the P9/P10 separation on the stronger ground that the predicate **mutates state**. |
| 5 | ~172 `internal/` packages | **160** at the pinned ref `7c817e064`. 172 is the count at the captain's unbuildable local HEAD. | Cosmetic. Recorded so the figure is not re-derived from the wrong tree. §5. |
| 6 | `gc version` ≈ **14 ms** | **34.5 ms median** (min 30.6, max 39.0), 15 runs, same in-tree `1.1.1` binary, `GC_HOME` redirected. `/bin/echo` floor measured 2.8 ms, matching the report's 3 ms. | **Strengthens** the §5 decision — shell-out is worse than believed, so the case for HTTP on the hot paths is stronger, not weaker. Hardware and load vary; treat as indicative. |
| 7 | `HasUnreachableCommitsResult` is a **BLOCKING FINDING**: Gas City's weaker reclaimer "reaps committed-but-never-pushed work on a local branch" | **Overstated.** The divergence is real and deliberate, with a sound documented rationale (`internal/git/git.go:182-195`). **Neither reclaimer deletes a branch ref**, so removal drops a *checkout*, not commits — recoverable via `git worktree add`. The reaper is additionally fail-closed on probe error and gates separately on uncommitted work and stashes. | **Material, in the safe direction.** Lowers the severity but **does not change the ruling** — §9.5 still forbids touching parlay's gates, now on the stronger ground that `isContentLanded`'s tree comparison beats *both* Gas City gates at the problem Gas City is solving. |

Two further findings that are corrections *within* the reports rather than against them, and
which this document adopts:

- **`GC_BEADS=file` does take a blocking flock**, contrary to the widely-repeated
  "no dolt, no bd, no flock" claim. §6(d).
- **`GC_BEADS=file` is not a beads database at all.** `bd` cannot read it. This is what
  eliminated it from Q4. §6(d).

---

## 14. Maintaining this document

- **Moving the pin (§1) requires re-running the build verification and re-recording the
  sha256 (§3).** Both, together, in the same PR.
- **§9.3's symmetry table is a snapshot of two separate code paths.** Re-read both functions
  whenever the pin moves.
- **§13 is not a changelog.** When a row stops being a discrepancy — because the report was
  fixed, or the code changed — delete the row and fix the body. Do not let it accumulate.
- The binding rules in §§6, 7, 8, 9 are **binding**. A later unit that needs one changed
  raises it as a decision, not as a refactor.
