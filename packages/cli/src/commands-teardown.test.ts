// robots-ceon: isContentLanded had no test at all, which is exactly why it
// shipped returning false for every input (two-arg `git merge-tree` prints a
// bare tree OID; the old check asked whether that OID was empty or contained
// the branch name). These run against real on-disk git repos with a real bare
// "origin" — the defect lived entirely in what git actually prints, so a mock
// would have reproduced the bug rather than caught it.
//
// Mirrors tools/cli/internal/commands/teardown_test.go, which covers the same
// cases for the Go CLI that `bin/parlay` actually execs.

import { afterAll, describe, expect, test } from "bun:test"
import { mkdtempSync, rmSync, writeFileSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { isContentLanded } from "./commands-teardown"

const tmpRoots: string[] = []
afterAll(() => {
  for (const d of tmpRoots) rmSync(d, { recursive: true, force: true })
})

function git(dir: string, ...args: string[]): string {
  const r = Bun.spawnSync(["git", "-C", dir, ...args], { stdout: "pipe", stderr: "pipe" })
  if (r.exitCode !== 0) {
    throw new Error(`git ${args.join(" ")} failed: ${new TextDecoder().decode(r.stderr)}`)
  }
  return new TextDecoder().decode(r.stdout).trim()
}

// Builds a repo with a bare origin, one base commit on origin/main, and a
// checked-out `feature` branch holding one unpushed commit.
function fixture(): { repo: string; featureHead: string } {
  const root = mkdtempSync(join(tmpdir(), "parlay-teardown-"))
  tmpRoots.push(root)
  const origin = join(root, "origin.git")
  const repo = join(root, "repo")

  Bun.spawnSync(["git", "init", "--bare", "-b", "main", origin], { stdout: "pipe", stderr: "pipe" })
  Bun.spawnSync(["git", "init", "-b", "main", repo], { stdout: "pipe", stderr: "pipe" })
  git(repo, "config", "user.email", "test@example.com")
  git(repo, "config", "user.name", "Test")
  git(repo, "config", "commit.gpgsign", "false")

  writeFileSync(join(repo, "base.txt"), "base\n")
  git(repo, "add", "-A")
  git(repo, "commit", "-m", "base")
  git(repo, "remote", "add", "origin", origin)
  git(repo, "push", "-u", "origin", "main")
  // isContentLanded reads refs/remotes/origin/HEAD for the default branch; a
  // clone gets it free, `git init` + `git remote add` does not.
  git(repo, "remote", "set-head", "origin", "main")

  git(repo, "checkout", "-b", "feature")
  writeFileSync(join(repo, "feature.txt"), "feature work\n")
  git(repo, "add", "-A")
  git(repo, "commit", "-m", "feature work")

  return { repo, featureHead: git(repo, "rev-parse", "HEAD") }
}

describe("isContentLanded", () => {
  test("true when the content squash-merged upstream (commits unreachable from any remote)", () => {
    const { repo, featureHead } = fixture()
    git(repo, "checkout", "main")
    git(repo, "merge", "--squash", "feature")
    git(repo, "commit", "-m", "squashed feature work")
    git(repo, "push", "origin", "main")
    git(repo, "checkout", "feature")

    expect(isContentLanded(repo, featureHead)).toBe(true)
  })

  test("true when the content merged upstream", () => {
    const { repo, featureHead } = fixture()
    git(repo, "checkout", "main")
    git(repo, "merge", "--no-ff", "-m", "merge feature", "feature")
    git(repo, "push", "origin", "main")
    git(repo, "checkout", "feature")

    expect(isContentLanded(repo, featureHead)).toBe(true)
  })

  test("false for work that never landed anywhere", () => {
    const { repo, featureHead } = fixture()
    expect(isContentLanded(repo, featureHead)).toBe(false)
  })

  test("false when there is no refs/remotes/origin/HEAD to resolve", () => {
    const { repo, featureHead } = fixture()
    git(repo, "checkout", "main")
    git(repo, "merge", "--squash", "feature")
    git(repo, "commit", "-m", "squashed")
    git(repo, "push", "origin", "main")
    git(repo, "checkout", "feature")
    git(repo, "remote", "set-head", "origin", "--delete")

    expect(isContentLanded(repo, featureHead)).toBe(false)
  })

  test("false on a merge conflict — inconclusive means refuse", () => {
    const { repo } = fixture()
    writeFileSync(join(repo, "base.txt"), "feature edit\n")
    git(repo, "add", "-A")
    git(repo, "commit", "-m", "feature edits base")
    const featureHead = git(repo, "rev-parse", "HEAD")

    git(repo, "checkout", "main")
    writeFileSync(join(repo, "base.txt"), "main edit\n")
    git(repo, "add", "-A")
    git(repo, "commit", "-m", "main edits base")
    git(repo, "push", "origin", "main")
    git(repo, "checkout", "feature")

    expect(isContentLanded(repo, featureHead)).toBe(false)
  })
})
