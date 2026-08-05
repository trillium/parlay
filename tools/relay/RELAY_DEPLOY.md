# Deploying the Parlay relay as an always-on service (macOS)

The relay is the single central fan-out for Parlay agent channels (one process,
one upstream poll loop per agent, one spool file per agent — see
[`NOTES.md`](./NOTES.md)). For agents to reach the captain reliably, the relay
must be **always up**: surviving crashes, logout/login, and reboots, and it must
be started automatically by `parlay monitor` if it is ever found down.

This directory (`tools/relay/deploy/`) makes that reproducible with a launchd
**LaunchAgent** plus an **ensure-up** safety net.

## What gets installed

| Thing | Location | Tracked in git? |
|-------|----------|-----------------|
| Relay binary | `~/Library/Application Support/parlay/bin/parlay-relay` | no (built artifact) |
| Launcher | `~/Library/Application Support/parlay/bin/parlay-relay-launch.sh` | no (copy of the repo script) |
| Shared lib | `~/Library/Application Support/parlay/bin/lib.sh` | no (copy) |
| LaunchAgent plist | `~/Library/LaunchAgents/com.parlay.relay.plist` | **no — machine config, outside the repo by design** |
| stdout/stderr logs | `~/Library/Logs/parlay/relay.{out,err}.log` | no |
| Runtime dir (socket + spools) | `$TMPDIR/parlay/` | no (volatile) |

The **repo** holds only the reproducible source: the plist *template*, the
launcher, the shared lib, and the install/uninstall/ensure-up scripts. The
installed plist lives outside the repo because launchd plists cannot reference
`$HOME` and must carry resolved absolute paths for this specific machine/user.

## Why a LaunchAgent (not a LaunchDaemon)

The relay must run in the **logged-in user's GUI session** (`gui/<uid>` domain)
so it inherits the same per-user `$TMPDIR` that an interactive shell and every
`parlay monitor` see. That shared temp dir (`getconf DARWIN_USER_TEMP_DIR`) is
where the control socket (`relay.sock`) and per-agent spools live, so the relay
and monitors must agree on it. A LaunchDaemon (system domain) would get a
different temp dir and the sockets would never line up.

`RunAtLoad=true` starts it at login; `KeepAlive=true` restarts it on any exit
(crash or kill). `ThrottleInterval=5` prevents a crash-loop from spinning.

## Runtime dir & the control socket

The relay, the monitors, and every deploy script resolve the runtime dir the same
way (see `parlay_relay_runtime_dir` in `deploy/lib.sh`):

1. `$PARLAY_RELAY_RUNTIME` if explicitly set (the monitor honors it too), else
2. `$TMPDIR/parlay` via `getconf DARWIN_USER_TEMP_DIR` (the reliable per-user
   temp dir — identical between a shell and a launchd job), else
3. `$TMPDIR/parlay` / `/tmp/parlay`.

The socket is `<runtime>/relay.sock` (or `$PARLAY_RELAY_SOCK` if set). Keep the
runtime path short: a Unix socket path must fit `sun_path` (~104 bytes on macOS);
`$TMPDIR/parlay/relay.sock` is well within it.

## Install

```sh
tools/relay/deploy/install.sh            # build if needed, install, load, verify
tools/relay/deploy/install.sh --rebuild  # force a fresh build first
tools/relay/deploy/install.sh --server http://localhost:31337   # custom Pulse URL
```

`install.sh` is **idempotent** — re-run it to update the binary/plist; it boots
out the old agent and reloads cleanly. It finishes by verifying the relay answers
`/health` on its socket and prints the launchd state.

It is **additive**: it does not touch any running agent monitor. It stands up a
relay on the shared runtime dir so new `parlay monitor --agent <id>` enrollments
use it; any legacy poll-path monitors keep working untouched.

## The ensure-up safety net

`parlay monitor --agent <id>` (via `tools/monitor/parlay-monitor.sh`) calls
`tools/relay/deploy/ensure-up.sh` before enrolling. ensure-up:

1. Returns immediately if the relay already answers `/health` (no double-start).
2. Otherwise takes a per-user lock (atomic `mkdir`) so two monitors starting at
   once cannot both launch a relay, re-checks health, then starts the relay by
   the best method available:
   - **launchd** — if the agent is installed but **not running**, `launchctl
     kickstart` (or `bootstrap` if it was unloaded). This restores full
     `KeepAlive` supervision. If launchd reports a **live pid**, nothing is
     started: the relay already exists and is simply not answering yet.
   - **installed binary** — unsupervised fallback if no plist is present.
   - **repo binary** — dev fallback when nothing is installed.
3. Waits adaptively for `/health`, then returns 0/1.

The relay's control socket is single-binder (a second live relay fails to bind
and exits), so even a lost lock race can never produce two live relays.

### It never force-restarts a running relay (robots-mpr3)

"Not answering `/health`" and "not running" are different states, and ensure-up
must not confuse them. It used to `launchctl kickstart -k` unconditionally —
`-k` **kills** a running job — and then wait only 10s. On a real fleet that
killed relays that were alive but mid-startup and then reported them dead,
silently breaking agent enrollment for the affected monitor.

Two things keep that from recurring:

- **The relay binds its control socket before replaying the spool**, so
  `/health` answers in milliseconds no matter how many agents are enrolled.
  (Previously the replay ran first — ~7s for 206 agents.)
- **The wait is adaptive.** `parlay_relay_wait_health` (in `lib.sh`) polls
  `/health` for `$PARLAY_RELAY_HEALTH_WAIT` seconds (default 45) and grants
  itself another budget whenever the relay's log has grown — evidence it is
  alive and still working — up to `$PARLAY_RELAY_HEALTH_MAX_WAIT` (default
  300). A wedged relay that has gone quiet still fails on the base budget, so
  this can never hang.

Force-restarting is now an explicit, deliberate act:

```sh
tools/relay/deploy/ensure-up.sh                # heal the relay from anywhere
tools/relay/deploy/ensure-up.sh --force-restart # restart even a running relay
```

Behavior is pinned by `tools/relay/deploy/ensure-up.test.sh` (hermetic: stubbed
`launchctl`/`curl`, redirected `$HOME` and runtime dir) and by
`TestControlSocketBindsBeforeSpoolResume` in `tools/relay/startup_test.go`.

## Operate

```sh
launchctl print gui/$(id -u)/com.parlay.relay        # full state, pid, last exit
launchctl kickstart -k gui/$(id -u)/com.parlay.relay # force a restart now
tail -f ~/Library/Logs/parlay/relay.err.log          # follow the relay log

SOCK="$(getconf DARWIN_USER_TEMP_DIR)parlay/relay.sock"
curl -s --unix-socket "$SOCK" http://relay/health    # {"ok":true}
curl -s --unix-socket "$SOCK" http://relay/agents     # registered agents
```

## Teardown (fully reversible — leaves no trace)

```sh
tools/relay/deploy/uninstall.sh          # stop, remove plist + install dir + socket
tools/relay/deploy/uninstall.sh --purge  # also delete runtime spools and logs
```

Exact manual teardown, if you prefer to do it by hand:

```sh
# 1. Stop + unload (KeepAlive stops restarting it once booted out):
launchctl bootout gui/$(id -u)/com.parlay.relay

# 2. Remove the machine config:
rm -f ~/Library/LaunchAgents/com.parlay.relay.plist

# 3. Remove the installed binary/launcher/lib:
rm -rf ~/Library/"Application Support"/parlay

# 4. Remove the socket (and optionally the whole runtime dir + logs):
rm -f "$(getconf DARWIN_USER_TEMP_DIR)parlay/relay.sock"
rm -rf "$(getconf DARWIN_USER_TEMP_DIR)parlay"   # optional: spools too
rm -rf ~/Library/Logs/parlay                     # optional: logs too
```

`uninstall.sh` (without `--purge`) deliberately keeps the runtime spools and logs
so nothing a monitor is mid-drain-on is yanked; pass `--purge` for a bit-for-bit
clean slate.

## Reproduce on another machine

The repo is self-contained: clone it, ensure Go ≥ 1.26 is installed, and run
`tools/relay/deploy/install.sh`. The build step produces the binary; the install
step renders the plist for that machine's user and loads it. Nothing here is
pinned to this checkout's path — the binary and plist are installed to stable
`~/Library` locations, so the repo can move or be deleted afterward and the
service keeps running (though you would re-run `install.sh` to update it).
