---
"@parlay/cli": patch
---

Make a failed `identity --register` loud in `parlay-spawn` instead of silent (robots-jusi).

`--worktree`/`--project` being missing from the Go `identity --register` flag table was not a case of two fields going missing: `internal/args.Parse` treats an unknown flag as fatal (`args.go:89`, exit 2), so the whole call died before writing anything and a worktree spawn got **no launch spec at all** — no name, color, cwd, mode, yolo, and no `context.json`. The flags themselves are restored in robots-6xq7; this change removes the reason nobody noticed for so long.

`bin/parlay-spawn` ended that registration with `>/dev/null 2>&1 || true`, discarding both the error text and the exit code. It now runs the call in an `if !` branch: the CLI's own error reaches stderr and the script names the consequence explicitly — the agent cannot self-reconstitute after a context reset, and `parlay teardown` will not see a worktree it holds, so it will skip its uncommitted/unpushed guards. Registration stays non-fatal (a launch spec is not worth aborting a live spawn over), but the next parity regression is visible instead of silent.

Also pinned by `TestRegisterRecordsAllLifecycleFields` in `internal/identity/identity_test.go`, which drives the full spec `parlay-spawn` actually issues — `mode`, `effort`, `kind`, `yolo`, `worktree`, `project` — through one `--register` call and checks every field round-trips into the frontmatter. Because one missing flag aborts the whole write, a single-field test cannot distinguish "this field is recorded" from "this call happened to parse"; the all-at-once form is what matches the real call site.
