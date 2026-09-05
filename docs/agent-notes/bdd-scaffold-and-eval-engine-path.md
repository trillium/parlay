# BDD scaffold, and where eval-engine's matcher actually lives

`make test-bdd` runs the repo's Gherkin/Cucumber scaffold end-to-end:
Cucumber.js scenarios under `features/spawn/account-resolution.feature`
(steps in `packages/ccjuggler/src/features/*.steps.ts`), plus Godog scenarios
under `features/spawn/agent-spawn.feature` and
`features/eval-engine/profile-matching.feature` (steps in
`tools/cli/internal/spawn/features_test.go` and
`tools/cli/internal/evalengine/features_test.go`, run as `go test -run
TestFeatures`). `packages/ccjuggler`'s `bun run test:bdd` must invoke
`bun node_modules/.bin/cucumber-js` directly, not `bunx cucumber-js` — bunx
shells out to Node, and Node's ESM loader cannot resolve the extensionless
TS import `../index` that `bun`-native resolution handles fine.

There is no `packages/eval-engine` package — despite that path appearing in
older task briefs. The real eval-engine phrase-matching code
(`platformEligible`, `CompiledMatcher`, `CommandManifest`) lives at
`tools/cli/internal/evalengine/`, a Go internal package under the `tools/cli`
module (same one `docs/agent-notes/go-cli-tools-cli-the-packages.md`
describes). `tools/eval-engine/` is a different thing entirely — just a
launchd deploy plist template. Point any future eval-engine work at
`tools/cli/internal/evalengine/`, and remember it inherits the
`CGO_ENABLED=0` requirement noted above for the rest of `tools/cli`.

Similarly, `internal/spawn`'s `gascity_spawn.go` was renamed to
`subprocess_spawn.go` in 2026-08 (its own header comment has the full
rationale); the functions are `subprocessSpawn`/`subprocessStop`/
`subprocessAlive`, not `gascitySpawn`/`gascityStop`/`gascityAlive`. Only the
CLI verbs (`gascity-spawn` etc.) and the on-disk state-dir segment keep the
old spelling, as deprecated aliases / for backward compatibility with
sessions started under the old name.
