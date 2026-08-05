---
"@parlay/cli": patch
---

Stop `parlay listen` from enrolling into the LIVE relay/registry when `PARLAY_SERVER` points elsewhere (robots-buu8).

A relay process is a per-runtime-dir **singleton bound to one upstream server**, chosen when it starts — so the relay you enroll on, not your `PARLAY_SERVER`, decides whose registry you land in. `tools/monitor/parlay-monitor.sh` always enrolled via the shared `$TMPDIR/parlay` relay, which on the captain's box is bound to production `:31337`. Any sandbox or test running `PARLAY_SERVER=http://127.0.0.1:<scratch> parlay listen --agent <id>` therefore registered a phantom channel in the captain's live agent registry, silently, with no hardcoded port anywhere to blame.

Two mechanisms, both needed:

1. **Server-scoped runtime dir.** The canonical dir is now reserved for the default server (`http://localhost:31337`, what `bin/parlay` exports). Any other `PARLAY_SERVER` resolves to `<canonical>/srv-<hash>` and gets its own relay (`parlay_relay_scoped_runtime_dir` in `tools/relay/deploy/lib.sh`), exported so `ensure-up.sh` and the relay launcher agree. `ensure-up.sh` correspondingly stops kickstarting the launchd relay when its runtime dir or its plist's `PARLAY_SERVER` do not match what was asked for — previously it kickstarted the production relay and then dead-waited 10s on a socket that would never appear. Scoped relays also log beside their own socket instead of into production's `~/Library/Logs/parlay` crash trail.
2. **Pre-enroll refusal.** Whatever socket is resolved, the monitor reads the relay's own `GET /agents` → `server` and exits 1 **before** `POST /register` if it disagrees with `PARLAY_SERVER`. This holds when scoping is bypassed — explicit `PARLAY_RELAY_RUNTIME`/`PARLAY_RELAY_SOCK`, a hand-started relay, or a checkout without `lib.sh`. A relay that reports no server is "unknown", not a mismatch, so older relays keep working.

The scoped dir name is a bare hash (`srv-<10 hex>`), not a readable slug: a Unix socket path is capped at 104 bytes (`sun_path`) and macOS's `$TMPDIR` already eats ~53 of them. A readable slug overflowed the cap and the relay died with `bind: invalid argument`, which names neither the limit nor the path — the monitor now checks the length up front and says so. The upstream URL stays discoverable via the relay's `/agents` and a `server` marker file dropped in the scoped dir.

Regression harness: `tools/monitor/parlay-monitor.test.sh` (17 cases, stub relay on a unix socket). The cross-server case asserts `/register` is *never* reached; run against the pre-fix script it reproduces the leak exactly (`GET /health`, `POST /register`).

**Deploy note:** re-run `tools/relay/deploy/install.sh` after this lands so the installed `lib.sh`/`ensure-up.sh` copies match the repo.
