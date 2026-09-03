# Deploying parlay-server as an always-on service (macOS)

`parlay-server` (`packages/go-server/cmd/parlay-server`) is the Go rewrite of
`packages/server`, Pulse's HTTP/SSE chat server. This directory gives it the
same launchd `KeepAlive` supervision as the relay
(`tools/relay/deploy/`, see `tools/relay/RELAY_DEPLOY.md`) and the eval-engine
(`tools/eval-engine/deploy/`).

## What gets installed

| Thing | Location | Tracked in git? |
|-------|----------|-----------------|
| Server binary | `~/Library/Application Support/parlay/bin/parlay-server` | no (built artifact, `go build`) |
| lib.sh copy | `~/Library/Application Support/parlay/bin/parlay-server-lib.sh` | no |
| LaunchAgent plist | `~/Library/LaunchAgents/com.parlay.go-server.plist` | **no — machine config, outside the repo by design** |
| stdout/stderr logs | `~/Library/Logs/parlay/go-server.{out,err}.log` | no |
| State (messages/agents/drafts/settings/uploads) | `~/.parlay` (`$PARLAY_STATE_HOME`) | no |

Unlike the relay, `parlay-server` is a plain TCP HTTP server (`-addr`/
`-state-dir` flags, `PARLAY_SERVER_ADDR`/`PARLAY_STATE_HOME` env) — there is
no unix-socket runtime dir to resolve at launch time, so the plist execs the
installed binary directly with no wrapper launcher script (contrast the
relay's `parlay-relay-launch.sh`).

The installed binary shares the relay's `.../parlay/bin` directory (distinct
filenames avoid collision), but `uninstall.sh` only ever removes this
service's own two files there — never the shared directory itself.

`parlay-server` also serves every route through its origin guard
(`internal/guard`; policy in `docs/api-contract.md` § Origin guard), which
reads `PARLAY_ALLOWED_ORIGINS` to admit extra browser origins (no `Origin`
header, same-origin, loopback, `.local` and private-LAN are always allowed
regardless — that covers the CLI, hooks and the panel). Setting the variable
in a shell only affects a server started from that shell, not the launchd
one — launchd does not inherit the shell environment. To reach the
installed, launchd-run server, bake it in at install time with
`--allowed-origins` (below); the rendered plist's `EnvironmentVariables`
dict is the only thing launchd actually sees.

## Install

```sh
packages/go-server/deploy/install.sh                    # build if missing, install, load, verify
packages/go-server/deploy/install.sh --rebuild           # force a fresh `go build` first
packages/go-server/deploy/install.sh --addr 127.0.0.1:4242 --state-dir ~/.parlay
packages/go-server/deploy/install.sh --allowed-origins "https://tunnel.example.com,https://other.example.com"
```

`install.sh` is macOS-only and idempotent — re-run to update the
binary/plist; it boots out the old agent, bootstraps + enables + kickstarts
it in `gui/<uid>`, and finishes by polling `/health`. Listen address defaults
to `127.0.0.1:4242` (matches `main.go`'s own coded default); state dir
defaults to `~/.parlay`. **Refuses to deploy against `:31337`** — that is
the captain's live production Pulse server (see this repo's `CLAUDE.md`) —
both here and, redundantly, in the binary's own `refuseProductionPort`.

`--allowed-origins <comma,separated,origins>` (env `PARLAY_ALLOWED_ORIGINS`)
bakes `internal/guard`'s origin allow-list into the rendered plist's
`EnvironmentVariables` — the only way it reaches the installed, launchd-run
server. Omitting the flag on a re-install **preserves** whatever value is
already installed (read back out of the current plist,
`parlay_goserver_installed_allowed_origins` in `lib.sh`) rather than wiping
it; pass `--allowed-origins ""` to explicitly clear it.

## Operate

```sh
launchctl print gui/$(id -u)/com.parlay.go-server        # full state, pid, last exit
launchctl kickstart -k gui/$(id -u)/com.parlay.go-server  # force a restart now
tail -f ~/Library/Logs/parlay/go-server.err.log            # follow the log
curl -s http://127.0.0.1:4242/health                       # {"ok":true,...}
```

`ensure-up.sh` guarantees a server is answering `/health`, starting one via
the best available method (launchd → installed binary → repo binary, built
if missing) if it is not already up. Idempotent and concurrency-safe (a
per-user lock file prevents two concurrent callers from both starting a
server). Useful as a pre-flight check before anything that talks to the
server (CLI commands, tests, tooling):

```sh
packages/go-server/deploy/ensure-up.sh            # ensure it's up; exit 0/1
packages/go-server/deploy/ensure-up.sh --quiet    # suppress the info line
```

## Teardown

```sh
packages/go-server/deploy/uninstall.sh            # keeps state dir + logs
packages/go-server/deploy/uninstall.sh --purge    # also deletes state dir + logs
```

Boots the job out of launchd, then **trashes** (never `rm -rf`/`rm -f`) the
rendered plist and the installed binary + lib.sh copy — via the real Finder
Trash (`trash` CLI if installed, e.g. `brew install trash`, else a manual
move into `~/.Trash`), so nothing removed by this script is ever
unrecoverable. `--purge`'s state dir target is not assumed to be the coded
default (`~/.parlay`) — it is resolved by reading the actual `-state-dir`
value back out of the installed plist (`parlay_goserver_installed_state_dir`
in `lib.sh`), so `--purge` always acts on whatever `install.sh --state-dir`
really used, even if that was a non-default location. Idempotent — safe to
run when nothing is installed.
