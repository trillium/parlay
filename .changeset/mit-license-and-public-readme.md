---
"parlay-input": patch
---

License the repo under MIT and make the README readable by someone who has never seen it

The repo had no `LICENSE` file and no `license` field in any manifest except
`packages/input`, which claimed `"license": "MIT"` with nothing at the root to
back it. Under default copyright that makes the code legally not open source —
nobody may use, copy, or modify it — which is the opposite of what the project
is for.

- Add a root `LICENSE` (MIT, © Trillium Smith) and set `"license": "MIT"` in
  every manifest. No dependency forces a different choice: there are **zero**
  runtime dependencies anywhere in the repo, all five Go modules are stdlib-only,
  and the devDependencies are MIT (`@changesets/cli`, `bun-types`,
  `@happy-dom/global-registrator`) or Apache-2.0 (`typescript`) — nothing
  copyleft. Correct `packages/input/LICENSE`'s copyright year from 2024 to 2026,
  the year of the repo's first commit.
- Rewrite the README's framing for a reader arriving cold: lead with what it does
  and why they'd care, name the voice-first capability up front instead of
  burying it, and state the real prerequisites before the Quickstart rather than
  three commands in — including, honestly, that the web panel has no public host
  because Pulse is not open source.
- Replace the Pulse-and-tailnet Quickstart with a local-only one that was run
  verbatim end-to-end against a scratch server: start `packages/server` on
  `:4242`, point the CLI at it, send and read back a message. No Pulse, no
  Tailscale, no accounts.
- Add `docs/README.md` separating docs that are generally useful from those that
  document integration with the author's private agent fleet, and give
  `PARLAY_FIRSTMATE_FOLD.md` and `CLI_VERBS_AND_EVENTS.md` a framing note saying
  so. Correct `api-contract.md`'s header, which told readers the server source
  could not be read from a checkout — that symlink loop has since been fixed.
