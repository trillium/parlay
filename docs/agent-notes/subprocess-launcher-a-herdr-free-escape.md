# `subprocess` launcher: a herdr-free escape hatch in `bin/parlay-spawn` (`[spawn] launcher`)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->

`bin/parlay-spawn` supports a second launcher path, selected by
`LAUNCHER` — resolved as a per-invocation `--subprocess` flag >
`PARLAY_SPAWN_LAUNCHER` env var > `~/.parlay/config.toml`'s `[spawn]
launcher` (parsed via `python3 -c 'import tomllib'`, so a missing `python3`
silently falls through to the default) > `herdr`. The pre-rename spelling is
still accepted at every one of those three inputs for one release: the
`--gascity` flag, and the value `gascity` from either the env var or the
config key (both normalized to `subprocess` in one place, after the
env-over-config resolution), each printing a deprecation notice on stderr.
`--subprocess` is parsed in all three call shapes (single, `--ephemeral`,
and batch `id=repo` dispatch, where it is a shared flag forwarded to every
pair like `--worktree`) and is the cheapest way to try the launcher for one
spawn without touching config or env. Registration, hello-reply, `context.json`,
worktree setup (including the treehouse lease), DoD/prompt composition,
pre-trust, `.env`/`.envrc` collection, and `identity --register` are all
launcher-agnostic and run unconditionally regardless of `$LAUNCHER` — only
the actual terminal/process launch (herdr Step 5) and the post-launch
liveness check (Step 6) branch on it.

**Why this exists**: herdr has a known SIGKILL failure mode in headless/
no-WindowServer environments. The `subprocess` path launches a detached
subprocess instead of a herdr tab, with no windowing dependency at all.

**Why it's a from-scratch port, not an import or a `gc` wrapper**: the
original scoping brief called for importing
`github.com/gastownhall/gascity/internal/runtime/subprocess` directly — Go's
`internal/`-visibility rule makes that structurally impossible from a
different module (no `replace` directive changes import-path visibility).
Shelling out to gascity's own `gc` CLI was also evaluated. An earlier
version of this file rejected `gc` on the grounds that it "requires a
`city.toml`, a dolt DB, and k8s client wiring" and "doesn't even build"
(CGO dolt dependency, missing ICU regex header). **That rejection is
retired** — corrected 2026-08-28 (P0) in
`docs/gascity-integration-contract.md`, which is the authority on the wider
Gas City adoption and keeps the measured shell-out cost and the chosen
hybrid seam. What actually holds: upstream Gas City builds clean (the seen
failure was keg-only Homebrew `icu4c` plus the captain's local merge
branch, not Gas City), and the two remaining real constraints are that the
Go surface is `internal/`-only (no import possible — above) and that `gc
session new` is template-shaped (it scaffolds a named session against
gascity's own daemon-state machinery, which is not the lifecycle this
launcher needs). So `tools/parlay-bin/subprocess_spawn.go` is a from-scratch
port of just the lifecycle semantics (detached `sh -c` child,
process-group signaling, SIGTERM-then-SIGKILL stop) — see that file's own
header comment for the full rationale, including one further deliberate
departure: gascity's real provider tracks liveness via a unix control
socket that requires the creating process to stay alive, which doesn't fit
`subprocess-spawn`/`subprocess-stop` being separate one-shot CLI
invocations with no supervisor in between — a plain PID file does the same
cross-process liveness job with no persistent listener required.

**Rename note (Gas City spawn lift unit 1)**: the launcher was called
`gascity`, though it contains no Gas City code. It now goes by `subprocess`
everywhere: the `--subprocess` flag, `PARLAY_SPAWN_LAUNCHER=subprocess`,
the `[spawn] launcher = "subprocess"` config value, and the three
`tools/parlay-bin` commands `subprocess-spawn` / `subprocess-stop` /
`subprocess-ping`. Every old spelling is still accepted as a deprecated
alias for one release and prints a notice. The **on-disk state dir keeps
its literal `gascity` segment on purpose** — it is the live state path of
every session already running under the old name, so `subprocess-stop`
always finds its own child's pid file and the rename orphans no session
(`$AGENT_DIR/gascity`, compatible with the brief's norename constraint).

**The three `tools/parlay-bin` subcommands** (`subprocess-spawn <agent-id>
<command> <workdir> [--state-dir DIR] [--env K=V ...] [--worktree-path P]`,
`subprocess-stop <agent-id> [--state-dir DIR]`, `subprocess-ping <agent-id>
[--state-dir DIR]`) always take an explicit `--state-dir` from
`bin/parlay-spawn` (`$AGENT_DIR/gascity`, i.e.
`$HOME/.parlay/agents/<id>/gascity`) rather than relying on their own
`agentHomeDir()` default — `agentHomeDir()` honors `PARLAY_AGENT_HOME`, but
bash's `AGENT_DIR` does not, so passing it explicitly is what keeps the two
in agreement. `tools/parlay-bin` is built if missing or stale, mirroring
`bin/parlay`'s own build-if-stale pattern for `tools/cli` (same `find … -newer`
check), at `tools/parlay-bin/bin/parlay-bin`.

**Treehouse integration (the part the captain flagged critical)**: a
treehouse-leased worktree must be returned on stop, or the pool starves
(see the `treehouse get` RESETS-the-slot section above). `WORKTREE_PATH` is
passed to `subprocess-spawn` as `--worktree-path` only when it came from
`TREEHOUSE_LEASED_PATH` — a variable set *only* on a verified treehouse
lease, deliberately distinct from `WORKTREE_PATH`/`_wt_created`, which are
also set on the plain-`git worktree`-fallback path. Passing a plain
worktree here would make `subprocess-stop` call `treehouse return` on a
path treehouse never leased. `subprocess-spawn` writes the path into a
`treehouse-path` sidecar in the state dir; `subprocess-stop` reads it and,
if `treehouse` is on PATH, runs `treehouse return <path>` **before**
signalling the process — always with `cmd.Dir` set to the leased path
itself, since `treehouse` resolves its pool from the process cwd, not a
flag (robots-d04t, documented above). Every step here is best-effort: a
missing sidecar, a missing `treehouse` binary, or a failing `treehouse
return` never blocks the stop that follows it.

**Charter delivery differs structurally from herdr's**: herdr launches
`claude` bare in a pane and delivers the charter afterward through `agent
prompt`'s paste-safe channel (a multi-line prompt can't be embedded as
`agent start` argv). The subprocess path has no pane to paste into, so the
charter (`$STARTUP_PROMPT`) is written to `$AGENT_DIR/startup-prompt.txt`
and piped into the child's stdin as part of its one-shot launch command
(`unset CLAUDECODE …; cd <cwd> && exec claude --dangerously-skip-permissions
--fallback-model sonnet [--model M] < startup-prompt.txt`), matching the
literal brief's "prompt via stdin/temp-file" instruction. **This is an
unverified assumption, flagged explicitly rather than silently trusted**:
whether the `claude` CLI headlessly consumes a piped-stdin prompt the same
way herdr's interactive pane submission does — and whether it stays
attached to a non-TTY stdin for a genuinely persistent, multi-turn session
rather than exiting after one one-shot response — has not been confirmed
against a real headless run in this environment. Verify this before relying
on the subprocess path for a real unattended agent.

**Post-launch liveness** also has no herdr equivalent to poll (`agent wait`
needs a herdr-registered agent). Step 6's subprocess branch instead polls
`GET $PARLAY/api/chat/subscribers` for an entry whose `channel` equals the
agent id, for up to `PARLAY_SPAWN_LIVENESS_TIMEOUT_MS` (default 60000, same
env var as the herdr watchdog), backgrounded/disowned the same way so batch
spawns keep their fast return; disable with `PARLAY_SPAWN_NO_WATCHDOG=1`.
Unlike the herdr watchdog it never resubmits the charter on timeout — there
is no paste-safe re-delivery channel here, only a warning naming
`subprocess-ping` for manual inspection.

Tests: `tools/parlay-bin/subprocess_spawn_test.go` covers the start/stop/
ping lifecycle, duplicate-session refusal, idempotent/stale-pid stop,
SIGKILL escalation (via the `stopGrace` var, shrunk in-test), the treehouse
sidecar write/return/cleanup cycle (a PATH-stubbed fake `treehouse` shim,
never a real pool), and the rename compat guarantee — a session started at
the pre-rename state dir (`.../<id>/gascity`) is still found and stopped by
the renamed stop path.
