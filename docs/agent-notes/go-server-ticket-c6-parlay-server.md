# Go server ticket C6: `parlay-server` launchd deploy tooling (`packages/go-server/deploy`)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`packages/go-server/deploy/{install,uninstall,ensure-up}.sh` + `lib.sh` +
`com.parlay.go-server.plist.template` give `parlay-server` (the C0–C3 Go
rewrite of `packages/server`) the same always-on macOS LaunchAgent
supervision as `tools/relay/deploy/`, which this ticket used as the
authoritative house-style reference (build-if-missing, atomic binary swap,
`sed`-rendered plist validated with `plutil -lint`, `launchctl
bootstrap/bootout/enable/kickstart -k` in `gui/<uid>`). Own label
(`com.parlay.go-server`, distinct from `com.parlay.relay`/
`com.parlay.eval-engine`), own paths under `~/Library/Application Support/
parlay/bin/` (shares the directory with the relay's binary but never
`rm`/trashes anything but its own two files there), default addr
`127.0.0.1:4242` (matches `main.go`'s coded default) with a hard refusal —
belt-and-suspenders with `main.go`'s own `refuseProductionPort` — of
`:31337`, the captain's live production Pulse instance.

**`uninstall.sh` never permanently deletes anything — every removal goes
through `parlay_goserver_trash_put` in `lib.sh`** (prefers a real `trash` CLI
— PATH, then Homebrew's keg-only install paths, e.g. `brew install trash`;
falls back to a manual move into `~/.Trash`), never `rm -rf`/`rm -f`. This is
not a stylistic choice: an earlier version of this script used plain `rm -rf`
for `--purge`, and — combined with a second bug where `uninstall.sh` had no
memory of `install.sh --state-dir`'s override and fell back to the coded
default — a smoke test's `uninstall.sh --purge` **permanently deleted the
live `~/.parlay` directory on the host** (other concurrent agents' registered
state under `~/.parlay/{agents,worktrees}` included), outside the smoke
test's own intended sandbox. Both root causes are fixed: all deletion is
trash-based (recoverable), and `--purge`'s state-dir target is resolved by
reading the real installed `-state-dir` value back out of the rendered
plist's `ProgramArguments` via `/usr/libexec/PlistBuddy`
(`parlay_goserver_installed_state_dir` in `lib.sh`) — the plist is the only
durable record of what `install.sh --state-dir`/`PARLAY_STATE_HOME` actually
resolved to at install time, so this must be read *before* the plist itself
is trashed. Any future deploy script in this repo that supports a `--purge`-
style destructive path should follow this same pattern (trash, never `rm`;
resolve real installed config from the live plist, never assume the coded
default) rather than re-deriving it from scratch.
