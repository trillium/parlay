# `treehouse` picks its pool from the PROCESS cwd — always pin it (robots-d04t)

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`treehouse get --lease` has no `--repo`/`--path` flag: it resolves which repo's
worktree pool to lease from by walking up from the process's current directory.
Any code that shells out to it while targeting a repo *other* than its own cwd
must set the child's cwd explicitly — `(cd "$PROJECT_PATH" && treehouse …)` in
bash, `cmd.Dir = projectPath` in Go. `parlay spawn --worktree --cwd <other-repo>`
did not, so spawning from inside a firstmate worktree leased a *firstmate*
worktree and launched the agent in the wrong repository, with nothing in the
output flagging it.

The durable guard, `repoIdentity` in
`tools/cli/internal/spawn/worktree.go`: a worktree's repo identity is
`git rev-parse --path-format=absolute --git-common-dir`, symlink-resolved. Every
linked worktree of a repo shares it with the primary checkout and no two repos
share it, so it is the right key for "is this actually a worktree of that repo?"
— `--show-toplevel` is not (it differs per worktree). The spawn path rejects a
wrong-repo treehouse lease (falling back to plain `git worktree`) and hard-aborts
before launch if the final worktree's identity does not match `--cwd`'s.
Regression coverage: `tools/cli/internal/spawn/worktree_test.go`. The bash
harnesses that used to cover this (`bin/parlay-spawn.worktree.test.sh` and
friends) went with the bash spawner — that Go test file is now the only guard.
