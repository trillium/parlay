// Runtime worktree-tangle guard (task-ttza, fold C4) — the pure git-tangle
// predicates ported from firstmate fm-tangle-lib.sh. Exercises real git repos in
// a temp dir: default-branch resolution, the tangle predicate on every state, and
// the banner text/restore command.

import { afterAll, beforeAll, expect, test } from "bun:test"
import { mkdtempSync, rmSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { defaultBranch, primaryTangleBranch, guardRepo } from "./commands-guard"

function git(dir: string, ...args: string[]): void {
  const r = Bun.spawnSync(["git", "-C", dir, ...args], { stdout: "pipe", stderr: "pipe" })
  if (r.exitCode !== 0) throw new Error(`git ${args.join(" ")} failed: ${new TextDecoder().decode(r.stderr)}`)
}

let repo: string
beforeAll(() => {
  repo = mkdtempSync(join(tmpdir(), "parlay-guard-"))
  git(repo, "init", "-q", "-b", "main")
  git(repo, "config", "user.email", "t@t.t")
  git(repo, "config", "user.name", "t")
  git(repo, "commit", "-q", "--allow-empty", "-m", "init")
})
afterAll(() => rmSync(repo, { recursive: true, force: true }))

// ── defaultBranch ─────────────────────────────────────────────────────────────

test("defaultBranch resolves to the local main when no origin/HEAD", () => {
  expect(defaultBranch(repo)).toBe("main")
})

test("defaultBranch returns '' for a non-git dir", () => {
  const nd = mkdtempSync(join(tmpdir(), "parlay-guard-nogit-"))
  try {
    expect(defaultBranch(nd)).toBe("")
  } finally {
    rmSync(nd, { recursive: true, force: true })
  }
})

// ── primaryTangleBranch ───────────────────────────────────────────────────────

test("on the default branch → not tangled (silent)", () => {
  git(repo, "checkout", "-q", "main")
  expect(primaryTangleBranch(repo)).toBe("")
})

test("on a NAMED feature branch → tangled, returns the branch name", () => {
  git(repo, "checkout", "-q", "-b", "parlay-variant/foo-wt1")
  expect(primaryTangleBranch(repo)).toBe("parlay-variant/foo-wt1")
  git(repo, "checkout", "-q", "main")
})

test("detached HEAD → NOT tangled (how linked worktrees legitimately sit)", () => {
  git(repo, "checkout", "-q", "--detach", "HEAD")
  expect(primaryTangleBranch(repo)).toBe("")
  git(repo, "checkout", "-q", "main")
})

test("non-git dir → NOT tangled", () => {
  const nd = mkdtempSync(join(tmpdir(), "parlay-guard-nogit-"))
  try {
    expect(primaryTangleBranch(nd)).toBe("")
  } finally {
    rmSync(nd, { recursive: true, force: true })
  }
})

// ── guardRepo banner ──────────────────────────────────────────────────────────

test("guardRepo emits a bordered banner naming the branch + non-destructive restore", () => {
  git(repo, "checkout", "-q", "-b", "fm/readme-restructure-d3")
  const lines: string[] = []
  const orig = process.stderr.write.bind(process.stderr)
  // @ts-expect-error test shim
  process.stderr.write = (s: string) => { lines.push(String(s)); return true }
  try {
    const branch = guardRepo(repo)
    expect(branch).toBe("fm/readme-restructure-d3")
  } finally {
    process.stderr.write = orig
    git(repo, "checkout", "-q", "main")
  }
  const out = lines.join("")
  expect(out).toContain("WORKTREE TANGLE")
  expect(out).toContain("fm/readme-restructure-d3")
  expect(out).toContain(`git -C ${repo} checkout main`) // non-destructive restore
})

test("guardRepo on a clean primary is silent and returns ''", () => {
  git(repo, "checkout", "-q", "main")
  const lines: string[] = []
  const orig = process.stderr.write.bind(process.stderr)
  // @ts-expect-error test shim
  process.stderr.write = (s: string) => { lines.push(String(s)); return true }
  try {
    expect(guardRepo(repo)).toBe("")
  } finally {
    process.stderr.write = orig
  }
  expect(lines.join("")).toBe("")
})

test("guardRepo --read-only banner defers restore to the lock holder", () => {
  git(repo, "checkout", "-q", "-b", "feat/x")
  const lines: string[] = []
  const orig = process.stderr.write.bind(process.stderr)
  // @ts-expect-error test shim
  process.stderr.write = (s: string) => { lines.push(String(s)); return true }
  try {
    guardRepo(repo, { readOnly: true })
  } finally {
    process.stderr.write = orig
    git(repo, "checkout", "-q", "main")
  }
  const out = lines.join("")
  expect(out).toContain("read-only session must leave restore")
  expect(out).not.toContain("git -C") // no restore command in read-only mode
})
