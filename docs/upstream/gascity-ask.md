# Upstream ask — gastownhall/gascity

**Status: DRAFT — NOT POSTED.** Posting this upstream is captain-gated
(task-4cfpv.18; the parlay-side units landed, the posting step did not). Everything below
the divider is the ready-to-post issue body. Every claim was verified against upstream
commit `ac6c9c685` (`v1.4.0-681-gac6c9c685`, main's head on 2026-08-30, the ref parlay
pins in `docs/gascity-integration-contract.md`) — line anchors cite that ref.

Suggested issue title:

> Integrator feedback: publish openapi.json as a release artifact; `requires_gc` is parsed
> but never enforced; two silent-failure findings; two doc/code mismatches; rotation
> anchor can reuse archived seqs

---

Hi — we're integrating Gas City as the execution plane for an agent-fleet project
(sessions, liveness, dispatch, event bus), talking to the supervisor over the `/v0`
HTTP + SSE API rather than linking Go packages. The typed API has been a pleasure to build
against — Huma generating the spec straight from the handlers means it can't silently
drift, and machine-readable failures (`{"ok":false,"error":{...}}`) make even refusals a
usable contract. In the course of pinning a contract against `ac6c9c685` we hit a few
things you may want to know about, in rough priority order. Line anchors below are at
`ac6c9c685`.

## 1. Publish `openapi.json` as a first-class, tagged contract artifact

`internal/api/openapi.json` (127 paths, OpenAPI 3.1) is exactly what an out-of-process
integrator needs, and it is already committed to the repo — but its path says `internal/`,
and nothing states it is a supported artifact rather than an implementation detail.
Today we vendor it from a pinned commit of the working tree and hash it in CI; that
detects drift but pins us to arbitrary commits rather than to your releases.

You already run release automation (v1.4.1 shipped binaries, checksums, and an SPDX SBOM;
`edge` is a rolling prerelease of main). The ask is small: **attach `openapi.json` to each
release as an asset alongside the checksums file** — or, even cheaper, document that
`raw.githubusercontent.com/gastownhall/gascity/<tag>/internal/api/openapi.json` is the
supported contract URL for a tag. One wrinkle worth covering either way: release tags are
cut on release branches, so main describes as `v1.4.0-681` even though `v1.4.1` exists —
an integrator tracking a feature that only exists on main currently has no tagged spec
that contains it. The `edge` release ("cross-version contract radars") looks like the
natural vehicle: an `openapi.json` asset on `edge` plus one on each version release would
retire our vendoring-from-working-copy setup completely.

## 2. `requires_gc` is parsed but never enforced — an integrator trap

This one is a bug report rather than a feature request. The `requires_gc` field in the
pack config round-trips cleanly — declaration (`internal/config/config.go:850-851`),
mirrored structs and copies (`cmd/gc/cmd_init.go:42`, `:853`, `:881`, `:1049`), and
round-trip tests (`internal/config/undecoded_test.go:536-547`) — but there is **no semver
parse or comparison anywhere in `cmd/`, `internal/`, or the tests**. We grepped the whole
tree at `ac6c9c685`: every occurrence is declaration, struct copy, or fixture.

`docs/reference/specs/pack-spec.md:215` does say "Parsed and preserved; not currently
enforced during load/import/doctor," which is honest — but it's one table cell, and the
field's shape does the opposite of warning. A pack author writes
`requires_gc = ">=0.16.0"`, watches it load and run without complaint on an older `gc`,
and reasonably concludes a version gate exists. An absent feature is safer than a present,
silent one: either enforcing it (a semver check at load/import/doctor with a loud error)
or failing loudly on the field until enforcement exists would close the trap. We'd be
happy with either.

## 3. Two silent-failure findings

Both share a shape: a defensible narrow local decision that becomes a silent loss once an
external system depends on it.

### 3a. `FileRecorder.Record` drops events with no signal a caller can see

`internal/events/recorder.go:236` — `Record` returns nothing. On a lock-acquisition
failure (the 250 ms flock budget at `:33`, spun by `lockRecorderFile`, `:330-343`) it
writes one line to `r.stderr` and returns; the event is gone and the caller cannot know.
The package documents the posture up front ("Recording is best-effort: errors are logged
to stderr but never returned to callers"), so this is a deliberate trade — but the one
signal it emits is stderr, and a supervisor-spawned writer whose stdio is detached (ours
are, and we suspect we're not unusual) drops events **completely silently**. `AppendBatch`
(`:274`) already exists and returns every failure, which is what we build on — but
`Record` is the path most call sites use. A counter, a health surface, or an optional
error-reporting hook on `Record` would make the loss observable without changing the
best-effort contract.

### 3b. The two worktree reclaimers answer "is this safe to remove?" differently

`internal/git/git.go` has two gates: `HasUnpushedCommitsResult` (`:210` — HEAD not
reachable from any remote) and `HasUnreachableCommitsResult` (`:242` — HEAD not reachable
from any local branch, remote, or tag). The session worktree pruner uses the stronger one
(`cmd/gc/session_worktree_prune.go:102`, `:177`); the bead worktree reaper uses the weaker
one (`cmd/gc/bead_worktree_reaper.go:303`).

The doc comment at `git.go:228-241` explains why the weaker gate exists (after a
squash-merge deletes the remote branch, the stronger gate returns true forever and blocks
all cleanup), and the reaper is carefully built — fail-closed on probe error, separate
gates for uncommitted work and stashes, and it records the branch name in its decisions.
We also want to be fair about severity: because neither reclaimer deletes a branch ref,
the weaker gate removes a *checkout*, not commits — work on a committed-but-never-pushed
local branch survives in the object store and is recoverable with `git worktree add`.

Still: committed-but-never-pushed on a local branch is the *normal* state of an in-flight
agent worktree, `--branches` lets that branch vouch for itself, and the two reclaimers
disagreeing means where a worktree's fate lands depends on which path reaches it first.
If the split is intentional, a comment on each caller saying why it uses its gate would
stop integrators (us) from having to diff the gates to find out; if not, the pruner's
choice looks like the right default, perhaps paired with a tree-comparison check for the
squash-merge case (comparing `<ref>^{tree}` against the default branch answers "is this
content already landed?" without the reachability approximation).

## 4. Two doc/code mismatches

- `engdocs/architecture/invariants.md:364` states "There is no opaque `payload: {}`
  anywhere" on the wire — but `internal/api/event_envelope_schemas.go:200` publishes a
  literal `"payload": {}` as the custom-envelope variant for non-known types, and
  `internal/events/events.go:388-392` (the comment on `KnownEventTypes`) explicitly says
  `ProviderHealthGateAlert` is delivered through it pending the typed SSE projection. The
  code comment tells the true story; the invariant doc states an absolute the wire
  doesn't keep. One sentence in invariants.md acknowledging the custom-envelope escape
  hatch (and its intended retirement) would reconcile them.
- `engdocs/architecture/beads.md` carries "Last verified against code: 2026-03-01" (`:5`)
  and its backend census has drifted in both directions: it lists the `Store`
  implementations as "BdStore, FileStore, MemStore, exec.Store" (`:13`), but the factory
  (`internal/beads/factory.go`) selects among BdStore, FileStore, ExecStore, and
  **NativeDoltStore** (`internal/beads/native_dolt_store.go`) — NativeDoltStore is absent
  from the census, and MemStore is a test double (as `:265` itself notes), not a
  factory-selectable backend.

## 5. Rotation can allocate an anchor event below already-archived seqs, breaking city-wide seq uniqueness

Found while end-to-end testing our event-bus consumer against a scratch city (pinned
`ac6c9c685`, `archive_retain_age=1ms`). When a second process has appended to the active
events file since the rotating recorder's own last write, the `events.rotated` anchor is
allocated a seq **below** seqs already sealed into the archive, and post-rotation appends
then reuse archived seqs.

**Exact repro.** A supervisor holding its in-process `FileRecorder`; five events appended
by CLI `gc event emit` subprocesses (direct file writes under flock), taking the active
file's seqs to 10–14 while the supervisor recorder's in-memory `r.seq` is still 9 (its own
last write); then `POST /v0/city/{city}/events/rotate?wait=true`. The response reports the
archive as `first_seq=1 last_seq=14` — but `anchor_event.seq=10`. Appends after the
rotation continue from the anchor (11, 12, …), reusing seqs 11–14 that exist in the
archive.

**Mechanism** (`internal/events/recorder.go` at `ac6c9c685`): `rotateLocked` reads the seq
window (`:481`), renames the active file away (`:497`), opens the fresh empty active file
(`:510`), and only **then** writes the anchor through `writeRecordLocked` (`:533`). But
`writeRecordLocked`'s resync (`:386-388`) reads `readLatestActiveSeq(r.path)` — which is
now the just-created empty file — and only ever raises `r.seq` (`latest > r.seq`), so the
recorder's stale in-memory seq wins and the anchor regresses below the archived window.
The flock is held throughout; this is not a race but an ordering bug — the resync source
is emptied one step before the resync runs.

**Why it matters to an integrator.** City-wide seq uniqueness is the property that makes
at-least-once delivery consumable: any consumer that dedups by seq — including gc's own
`--follow` resume — will treat the reused post-rotation seqs as already-seen and silently
drop real events whose seqs are ≤ its cursor. In our case the consumer's contract is
"never skip silently; announce every gap," and a seq that goes *backwards* across an
`events.rotated` anchor produces exactly the unannounced gap that contract exists to
prevent. It also makes the anchor self-inconsistent: its payload says the archive sealed
`first_seq..last_seq`, while its own envelope seq sits inside that window.

**Suggested fix direction.** `rotateLocked` already computed the pre-swap window's `last`
at `:481` — seeding `r.seq = max(r.seq, last)` before writing the anchor closes the hole
with one line; alternatively `writeRecordLocked`'s resync could read the just-rotated
file's tail for the anchor write. Either preserves the existing locking and best-effort
posture.

---

A note on our side of #5: until it's fixed we defensively key rotation-crossing dedup on
the anchor rather than trusting absolute monotonicity — a consumer that sees
`anchor_event.seq` ≤ the archive's `last_seq` must treat subsequent seqs as a fresh
epoch, not as replays. We'd rather delete that workaround than document it.

Items 1–4 don't block us — we've built our contract around what the code actually does, and
the code has generally been the more trustworthy of the two surfaces, which is its own
compliment. Happy to provide more detail on any of these, or to PR the doc fixes in #4 if
that's welcome.
