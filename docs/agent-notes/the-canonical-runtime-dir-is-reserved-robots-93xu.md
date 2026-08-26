# The canonical runtime dir is RESERVED — a wrong-server relay in it is a fleet outage (robots-93xu)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Scoping (robots-buu8 above) keeps non-default servers *out* of the canonical dir.
Nothing kept a non-default relay from **occupying** it. `install.sh` defaulted
`--server` from an ambient `$PARLAY_SERVER`, so one install run from a shell that
happened to export `http://localhost:4242` baked that into the LaunchAgent —
which is a fixed singleton on the canonical dir. Every default-server agent on
the box then resolved to that dir, found `:4242`, and was refused by the
pre-enroll guard. A fleet-wide enrollment outage, persistent across reboots,
whose only symptom was agents failing to start.

Three rules this leaves behind:

- **Never let an ambient env var configure an installed singleton.** `install.sh`
  now refuses any server other than the default unless `--allow-non-default-server`
  is passed, and says so louder when the value came from the environment rather
  than the flag. A non-default server needs no install at all — `ensure-up.sh`
  starts a scoped relay for it on demand.
- **A liveness probe is not a correctness probe.** `/health` says a relay
  answers, not which server it serves. ensure-up's fast path returned 0 on
  `/health` alone — a false green that handed the caller a success line and let
  it die one step later. It now compares the relay's `GET /agents` → `server`
  against the wanted one and exits **3**, distinct from 1 ("no relay could be
  started"), because the two need opposite responses. It never restarts the relay
  (robots-mpr3); `parlay-monitor.sh` recognizes 3 and defers to ensure-up's
  message instead of contradicting it with "install the relay".
- **Failure advice must fit the case that actually happens.** "Unset
  `PARLAY_RELAY_RUNTIME`/`PARLAY_RELAY_SOCK`" was a dead end here — neither was
  set, which is precisely why the resolution rule picked the squatted dir. With
  no override set the monitor now names the squatting relay as the fault and
  prints the `install.sh --server …` repair.

`parlay_relay_installed_plist_server` was fixed en route: **PlistBuddy prints its
errors on stdout** and can still exit 0, so an unreadable plist came back looking
like a server URL. Any PlistBuddy capture in this repo must validate the shape of
what it got. Pinned by `tools/relay/deploy/install.test.sh` and cases 7–8 of
`ensure-up.test.sh`.
