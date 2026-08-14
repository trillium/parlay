---
"@parlay/cli": patch
---

`parlay sweep --apply` now actually closes the swept agent's terminal, not just its relay record.

Teardown gained a final step: it resolves the agent's herdr surface (live `herdr agent get <id>`, falling back to the tab parlay-spawn labelled with the same id for an agent whose process already exited) and closes it — the tab when the agent owns it alone, otherwise just its pane, so a shared tab never takes bystander panes down with it. Best-effort: no herdr, no daemon or an unknown agent leaves teardown's real work untouched, and it never closes the calling agent's own pane.

Before this, a sweep printed `closed` for agents whose panes and OS processes were all still alive; 57 of them had to be closed by hand afterwards.
