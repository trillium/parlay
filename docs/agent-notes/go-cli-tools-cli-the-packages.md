# Go CLI (`tools/cli`, the `packages/cli` rewrite) cites two docs that don't exist in this checkout

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


Comments across `tools/cli/**/*.go` (config, args, identity, monitor, …)
repeatedly cite `docs/scope-go-cli.md` and `docs/plan-go-migration-tickets.md`
as authoritative for naming/behavior decisions (env var names, exit codes,
FNV color-hash parity, etc.) — neither file exists under `docs/` as of this
writing (`docs/api-contract.md`, also cited from `internal/httpc`, does
exist and is accurate). Treat the `scope-go-cli.md`/`plan-go-migration-tickets.md`
rationale as historical/aspirational: verify against the actual TS source
(`packages/cli/src/*.ts`, not symlinked, safe to read directly) and the
landed Go code's own tests instead of trying to open those paths.
`tools/cli/internal/monitor` (ticket B2: `monitor`/`listen`) is
a straight shell-out port — the relay path runs
`tools/monitor/parlay-monitor.sh` as a supervised child (its own process group,
torn down when this process is signalled or orphaned — see robots-3pvi below),
resolved via `exec.LookPath` first (so a
future PATH install is picked up) and falling back to a repo-relative path
computed from the Go source file's own location, the closest Go equivalent
of the TS original's `import.meta.url`-relative resolution.
