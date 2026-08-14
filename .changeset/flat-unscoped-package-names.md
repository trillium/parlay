---
"parlay-cli": patch
"parlay-client": patch
"parlay-server": patch
---

Rename the remaining scoped packages to the flat, unscoped `parlay-<part>` scheme

The `@parlay` npm scope is unclaimed and cannot be published (a real publish
attempt returned 404), so every installable part of parlay uses a flat
`parlay-<part>` name. `parlay-input` already landed there; these are the
leftovers, renamed from their old scoped names to `parlay-cli`
(`packages/cli`), `parlay-client` (`packages/client`), and `parlay-server`
(`packages/server`). All three stay `private: true` — this is naming hygiene,
not a decision to publish. `tools/split-test` was renamed in the same pass to
`parlay-split` (matching its bin) but is outside the `packages/*` changeset
workspace, so it is not tracked here.
