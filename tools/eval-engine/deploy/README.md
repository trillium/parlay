# Deploying the Parlay eval-engine as an always-on service (macOS)

The eval-engine (`parlay eval serve` — `tools/cli/internal/evalengine`, compiled
Go, `:4343`) computes every
voice-command action for the Parlay chat panel. In the pure server-side-eval
model the client does no local matching, so if the engine is down every phone
voice command silently fails (submit "bravely", tab switches, clear, …). It
used to run as a bare `nohup` process with no supervisor and stayed down for
days after a reboot (robots-t9f). This directory gives it the same launchd
`KeepAlive` supervision the relay has (see `tools/relay/RELAY_DEPLOY.md`).

## What gets installed

| Thing | Location | Tracked in git? |
|-------|----------|-----------------|
| Engine binary | `tools/cli/bin/parlay-eval-engine` (dedicated build of the unified parlay CLI) | no (built artifact, `go build`) |
| LaunchAgent plist | `~/Library/LaunchAgents/com.parlay.eval-engine.plist` | **no — machine config, outside the repo by design** |
| stdout/stderr logs | `~/Library/Logs/parlay/eval-engine.{out,err}.log` | no |

Since the task-0ke9 unification the engine ships inside the single parlay
binary; the plist runs `parlay-eval-engine eval serve`. A dedicated copy is
built (rather than pointing at an interactive `parlay` install) so rebuilding
the CLI never yanks the supervised service's binary mid-flight.

Unlike the relay, the plist execs the **checkout binary in place** rather than
a copy installed elsewhere — the engine reads an optional `commands.json` next
to the binary (`os.Executable` dir, so `tools/cli/bin/commands.json`) for
hot-reloadable command customization, so keeping it in the checkout preserves
that beside-binary manifest feature (see the template's header comment for the
full rationale).

## Install

```sh
tools/eval-engine/deploy/install.sh            # build if missing, install, load, verify
tools/eval-engine/deploy/install.sh --rebuild  # force a fresh `go build` first
```

`install.sh` is macOS-only and idempotent — re-run to update the binary/plist;
it boots out the old agent, bootstraps + enables + kickstarts it in `gui/<uid>`,
and finishes by polling `/health`. Listen address defaults to `127.0.0.1:4343`;
override with `PARLAY_EVAL_ADDR`.

## Operate

```sh
launchctl print gui/$(id -u)/com.parlay.eval-engine        # full state, pid, last exit
launchctl kickstart -k gui/$(id -u)/com.parlay.eval-engine # force a restart now
tail -f ~/Library/Logs/parlay/eval-engine.err.log          # follow the log
curl -s http://127.0.0.1:4343/health                       # {"ok":true,...}
```

## Teardown

```sh
tools/eval-engine/deploy/uninstall.sh
```

Boots the job out of launchd and deletes the rendered plist. Leaves the
checkout binary and logs in place. Idempotent.
