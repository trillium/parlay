---
"@parlay/cli": patch
---

Block a PR whose green checks were earned against a base that has since moved (robots-1hs5, follow-on to robots-ot20).

GitHub runs `pull_request` workflows on `refs/pull/N/merge` — the merge *result* — and recomputes that ref when the base branch moves, but it does **not** re-trigger the check run. A PR that went green hours ago keeps that green forever while describing a merge with a main that no longer exists, so merging it lands a combination no CI ever evaluated. On trillium/firstmate, #76/#77/#79 each held such a green and, merged in turn, collectively broke main with duplicate entries in a shared JSON file.

`parlay merge-gate` now reports `behind-base` (code-class, exit `3`, fixed by merging the base in or rebasing) when the head is behind its base. It deliberately does **not** read `mergeStateStatus=BEHIND` as its primary signal: GitHub only reports BEHIND on a base branch protected with "require branches to be up to date before merging", and the repos this gate runs against have no protection at all — every behind PR there reports `CLEAN` or `UNSTABLE`, so that field would be a blocker that never fires in exactly the case it was written for. The gate asks the compare API (`repos/O/R/compare/base...head` → `behind_by`) instead, which is true regardless of repo settings, and treats `BEHIND` as corroboration when present. If the compare call cannot be made, the verdict says base freshness is UNKNOWN rather than assuming the branch is current, and `--json` reports `behindBy: null` so "never asked" is distinguishable from "0 commits behind".
