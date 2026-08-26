# A best-effort probe written as `VAR=$(cmd)` is not best-effort (robots-dcag)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Every shell script here runs `set -euo pipefail`, and under it a **plain
assignment takes the exit status of its command substitution** — so
`VAR=$(curl … | sed …)` aborts the script the moment curl fails, no matter how
optional the value is. `2>/dev/null` hides curl's complaint, so the script dies
producing nothing. `parlay-monitor.sh`'s cross-server probe was written this
way: a `--max-time 2` timeout (exit 28) killed the monitor three lines before
its first "enrolling" message. Because `parlay listen` registers and announces
with Pulse *before* shelling out, the agent sat in the panel looking healthy
with no event stream at all — **registered-but-deaf**, taking no directives for
the rest of the session.

Two rules this leaves behind:

- **Consume the status explicitly** — `VAR="$(cmd)" || { …; VAR=""; }`, or call
  a helper that returns 0 on every path (`parlay_relay_reported_server` in
  `tools/relay/deploy/lib.sh` is the model: `|| return 0`, empty result means
  "unknown"). A caller must never be able to die on an optional probe.
- **Never let setup fail silently.** `parlay-monitor.sh` traps EXIT until it
  reaches `tail` and prints the exit code plus the registered-but-deaf
  consequence, so a dead stream can never look like a quiet one.

Timeouts must also be sized against the real endpoint: `/health` answers from a
socket bound before any work, but `/agents` serializes the whole registry and
grows with the fleet (>2s at 269 agents). The probe bound is
`$PARLAY_RELAY_PROBE_TIMEOUT` (default 15s), deliberately separate from
`/health`'s 2s. Regression coverage: section C of
`tools/monitor/parlay-monitor.test.sh`, whose stub relay can stall `/agents` on
demand — and which also pins that tolerating an *unknown* answer never softened
the robots-buu8 refusal into a no-op.
