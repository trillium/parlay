---
"parlay-cli": patch
---

Stop `parlay ensure-up` from killing a healthy relay and then declaring it dead (robots-mpr3).

Enrolling an agent intermittently failed with `relay did not answer /health within 10s`, even though the relay was fine — `curl --unix-socket .../relay.sock http://relay/health` returned `{"ok":true}` seconds later, and re-arming the same monitor succeeded. `parlay claim` still registered and announced (a plain HTTP POST to Pulse), so the agent *looked* enrolled while its listen loop was dead and captain messages never reached it. Silent: the only signal was a failed Monitor task.

Two independent causes, both fixed:

- **The relay replayed its whole spool before binding the control socket** (`tools/relay/main.go`). `/health` was therefore unanswerable for the entire replay — ~7s for the 206 spooled agents on this host, and growing with the fleet. The socket is now bound and served *first*, so `/health` answers in milliseconds regardless of fleet size; the resume walk (extracted to `resumeFromSpools`) runs after, and is safe to overlap because `register()` is mutex-guarded and idempotent. Binding first also surfaces a duplicate-relay bind failure before doing any replay work.

- **`ensure-up.sh` force-restarted on every miss and gave up at 10s.** It ran `launchctl kickstart -k` unconditionally — `-k` *kills* a running job — so a relay that was alive but mid-startup got killed and had to replay from zero, which then blew the 10s bound. ensure-up now starts a job only when launchd reports **no pid**, uses a plain (non-`-k`) `kickstart`, and waits via the new adaptive `parlay_relay_wait_health`: `$PARLAY_RELAY_HEALTH_WAIT` seconds (default 45), re-granted whenever the relay's log has grown, capped at `$PARLAY_RELAY_HEALTH_MAX_WAIT` (default 300). A wedged, quiet relay still fails on the base budget, so it can never hang. Deliberate restarts get an explicit `--force-restart` flag. Losing the start lock now waits on the peer's relay with the same adaptive bound instead of failing the moment the 10s lock spin expires, and `install.sh`'s verify step — a fixed 5s, same defect class — uses the adaptive wait too.

Regression coverage: `tools/relay/deploy/ensure-up.test.sh` (hermetic; stubbed `launchctl`/`curl`, redirected `$HOME`/runtime dir; asserts a running relay is never kickstarted) and `TestControlSocketBindsBeforeSpoolResume` in `tools/relay/startup_test.go` (runs the real binary and asserts the bind line precedes the first resume line). Both fail against the pre-fix code.
