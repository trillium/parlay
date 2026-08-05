---
"@parlay/cli": patch
---

Make `tools/relay/deploy/ensure-up.test.sh` actually exercise the launchd start/wait policy it claims to pin (robots-t66l).

5 of its 7 cases failed on a clean `origin/main`. The harness pointed `PARLAY_RELAY_RUNTIME` at a temp dir, so the robots-buu8 scoping guard in `ensure-up.sh` correctly refused the launchd path ("requested runtime … is not the launchd relay's"), fell through to methods 2 and 3, found no relay binary inside the sandbox, and exited 1 — the stubbed `launchctl` assertions were never reached. The harness predates that scoping and was never updated for it. Net effect: the robots-mpr3 regression suite (never force-restart a running relay, wait adaptively) failed identically whether the policy was right or wrong, so it could not catch a regression.

Fixed by moving the canonical runtime dir into the sandbox instead of overriding it out of band: a new `getconf` stub answers `DARWIN_USER_TEMP_DIR` with `$ROOT` (delegating every other query to the real `getconf`), so `$RUNTIME` is `$ROOT/parlay` — the canonical dir — and all three scoping checks (runtime dir, socket, upstream server) match. The fake LaunchAgent plist is now a real plist carrying the default `PARLAY_SERVER` rather than a `touch`ed empty file, so the plist-server check is exercised instead of passing vacuously on a PlistBuddy error.

`run()` also gained a harness self-check: if `ensure-up` ever prints "not using launchd" again, that surfaces once as an explicit HARNESS failure ("fix the stubs, not the assertions") rather than as several unrelated-looking policy failures. Verified by mutation — reverting the robots-mpr3 policy in `ensure-up.sh` now fails cases 2 and 3, where before the fix it changed nothing. No production script changed; this is a test-harness fix only.
