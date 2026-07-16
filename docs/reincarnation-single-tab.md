# Reincarnation — the single-tab guarantee

> Reasoning for the reliability + observability hardening of `bin/reincarnate`
> (Mayor brief, 2026-07-15). Companion to the code; read with `bin/reincarnate`
> and `bin/parlay-spawn`.

## The failure this prevents

A reincarnation once produced a **duplicate** herdr tab for one agent id. The
env-less duplicate became the live conversational session, so bare `reply`
failed with `no agent identity`. The old reboot used a **fail-open** pre-close:

```sh
tab=$(herdr agent get "$AID" | jq -r '.result.agent.tab_id // empty')
[ -n "$tab" ] && herdr tab close "$tab" || true   # if the tab wasn't found or
                                                  # close failed → ghost survives
```

If that close missed (tab not found, race, error), the relaunch created a
**second** tab and nothing reconciled them.

## The guarantee

**After a `--reboot` reincarnation there is exactly one tab labelled `<id>`, and
it is verified live + env-wired — or a failure receipt is written.**

It rests on three layers, each of which alone narrows the window, and which
together close it:

1. **Reliable pre-clear (not fail-open).** Before relaunch the watcher closes
   *every* tab whose label equals the agent id, not just the one `agent get`
   happened to return:
   ```sh
   for _t in $(_tabs_for "$AID"); do herdr tab close "$_t" || true; done
   ```
   herdr agents die with their tab, so this also removes the stale **agent
   record** — which is what lets the relaunch proceed at all (see layer 2).

2. **parlay-spawn's duplicate guard (up-front).** `parlay-spawn` refuses to
   create an agent whose herdr id already exists, and rolls back the tab it
   created if `herdr agent start` fails. So a relaunch can only succeed once the
   old record is gone (layer 1 guarantees that), and a half-failed spawn never
   leaves a ghost. Interactive double-spawns are refused outright.

3. **Post-relaunch reconcile.** Even if a race left more than one tab for the id,
   the watcher keeps the **newest** (highest tab `number`) and closes the rest:
   ```sh
   _keep=$(herdr tab list | jq -r --arg id "$AID" \
     '[.result.tabs[]?|select(.label==$id)]|max_by(.number)|.tab_id // empty')
   for _t in $(_tabs_for "$AID"); do [ "$_t" != "$_keep" ] && herdr tab close "$_t"; done
   ```

Layer 1 makes duplicates impossible in the common path; layer 2 makes a
concurrent duplicate impossible; layer 3 is the belt to those suspenders.

## Observability (so a break is never silent)

- **Durable receipt.** Every reincarnation appends one JSONL line to
  `${PARLAY_AGENT_HOME:-~/.parlay/agents}/<id>/reincarnations.log` (ISO-UTC ts,
  old claude pid, reboot cmd, outcome, reconciled tab id) — not just the
  ephemeral `$TMPDIR` watcher log.
- **Self-verify.** After relaunch the watcher polls the parlay subscribers
  endpoint (≤90s) for the reborn agent to appear on **its own** channel. That
  requires both a live process *and* `PARLAY_AGENT_ID` wired — exactly the two
  properties the env-less duplicate lacked. Success → receipt `verified`;
  timeout → receipt `verify_failed`, so a broken reincarnation is discoverable.

## No regression

`--reboot`, `--dry`, no-reboot (deliberate end), `--cmd <custom>`, and `--help`
all behave as before; the hardening lives entirely in the reboot branch of the
external watcher. Verified by dry-run across all paths.
