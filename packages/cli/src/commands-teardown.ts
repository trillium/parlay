// Fold §3.7 + Slice 3 — parlay-teardown: safe destroy of agents with worktrees.
//
// Refuses to destroy uncommitted changes or unpushed commits unless explicitly
// forced (--force). Validates that work is either committed + pushed, or that
// its content is already present in the default branch (merge-tree equality —
// see isContentLanded). There is no PR/patch-id strategy here; the docs that
// claimed one were wrong (robots-ceon).
//
// Steps:
// 1. Check git status for uncommitted changes
// 2. Check for unpushed commits
// 3. Validate landed-content containment (merge-tree tree-OID equality)
// 4. Deregister agent from relay
// 5. Remove worktree
// 6. Delete agent store (ephemeral policy)

import { existsSync, readFileSync, rmSync } from "fs"
import { homedir } from "os"
import { join } from "path"
import { EXIT_USAGE, serverUrl } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

const AGENTS_DIR = join(homedir(), ".parlay", "agents")
const WKTREES_DIR = join(homedir(), ".parlay", "worktrees")

// Shell helper: run a command and return { ok, out, err }
function sh(cmd: string, args: string[]): { ok: boolean; out: string; err: string } {
  const r = Bun.spawnSync([cmd, ...args], { stdout: "pipe", stderr: "pipe" })
  return {
    ok: r.exitCode === 0,
    out: new TextDecoder().decode(r.stdout).trim(),
    err: new TextDecoder().decode(r.stderr).trim(),
  }
}

// Parse frontmatter from identity.md to extract metadata
function parseFm(src: string): Record<string, string> {
  const m = src.match(/^---\n([\s\S]*?)\n---/)
  if (!m) return {}
  const out: Record<string, string> = {}
  for (const line of m[1].split("\n")) {
    const kv = line.match(/^(\w+):\s*"?([^"]*)"?\s*$/)
    if (kv) out[kv[1]] = kv[2]
  }
  return out
}

// Read identity.md for an agent
function readIdentity(agentId: string): Record<string, string> {
  const f = join(AGENTS_DIR, agentId, "identity.md")
  return existsSync(f) ? parseFm(readFileSync(f, "utf-8")) : {}
}

// Check if there are uncommitted changes in a repo
function hasUncommitted(repoPath: string): boolean {
  const r = sh("git", ["-C", repoPath, "status", "--porcelain"])
  return r.ok && r.out.length > 0
}

// Check if there are unpushed commits
function hasUnpushed(repoPath: string): boolean {
  const r = sh("git", ["-C", repoPath, "log", "HEAD", "--not", "--remotes"])
  return r.ok && r.out.length > 0
}

// Validate landed-content containment. ONE strategy, not three: three-way merge
// headRef into the default branch and compare the resulting tree to the default
// branch's own tree. Equal trees mean headRef introduces nothing the default
// branch lacks — which is how squash-merged work, unreachable from any remote
// ref, is still recognised as landed. No PR/patch-id strategy exists here.
//
// This replaces a version using two-arg `git merge-tree <branch> <head>` plus
// `out === "" || out.includes(branch)` (robots-ceon): on git >= 2.38 that form
// prints a bare tree OID, so `out` was never empty and a branch name like
// "main" can never appear in 40 hex digits — it returned false unconditionally
// and the landed escape in cmdTeardown had never once fired. Correct form
// mirrored from firstmate's bin/fm-teardown.sh `content_in_default`.
//
// Every inconclusive path returns false so teardown refuses rather than
// guesses: no origin/HEAD, no resolvable default ref, a merge conflict, or a
// git too old for --write-tree (which exits non-zero on the unknown flag).
export function isContentLanded(repoPath: string, headRef: string): boolean {
  const defaultBranch = sh("git", ["-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD"])
  if (!defaultBranch.ok) return false

  const branch = defaultBranch.out.replace("refs/remotes/origin/", "")

  // Refresh the remote-tracking ref first: this check exists for work that
  // landed upstream after the worktree last synced, which a stale
  // origin/<branch> cannot see. Best-effort — offline teardown falls through to
  // whatever ref already exists rather than refusing outright.
  const remoteRef = `refs/remotes/origin/${branch}`
  if (sh("git", ["-C", repoPath, "remote", "get-url", "origin"]).ok) {
    sh("git", ["-C", repoPath, "fetch", "--quiet", "origin", `+refs/heads/${branch}:${remoteRef}`])
  }

  // Prefer the remote-tracking ref (what "landed" actually means); fall back to
  // the local branch for a repo with no origin at all.
  let ref = remoteRef
  if (!sh("git", ["-C", repoPath, "rev-parse", "--quiet", "--verify", ref]).ok) {
    ref = `refs/heads/${branch}`
    if (!sh("git", ["-C", repoPath, "rev-parse", "--quiet", "--verify", ref]).ok) return false
  }

  const defaultTree = sh("git", ["-C", repoPath, "rev-parse", "--quiet", "--verify", `${ref}^{tree}`])
  if (!defaultTree.ok || defaultTree.out.length === 0) return false

  // --write-tree prints the merged tree OID on the first line; on conflict it
  // exits non-zero and prints the conflict report instead.
  const mergeTree = sh("git", ["-C", repoPath, "merge-tree", "--write-tree", ref, headRef])
  if (!mergeTree.ok) return false

  const merged = mergeTree.out.split("\n")[0]!.trim()
  return merged.length > 0 && merged === defaultTree.out
}

// The git half of a safe destroy: returns a refusal message if worktree still
// holds uncommitted changes, or commits that are neither pushed nor landed —
// null when it is safe to remove (or --force overrode the refusal). Exported so
// commands-variant.ts's teardown can share it: that path used to jump straight
// to `git worktree remove --force` with zero git checks, permanently destroying
// a variant's working tree while `parlay teardown` refused the identical
// situation (robots-cncx). `cmd` is the user-facing command prefix.
export function checkWorktreeGitSafety(
  cmd: string,
  agentId: string,
  worktree: string,
  force: boolean
): string | null {
  // Check for uncommitted changes.
  if (hasUncommitted(worktree)) {
    if (!force) return `${cmd}: ${agentId} has uncommitted changes. Run 'git diff' or --force to discard.`
    process.stderr.write(`warn: --force: discarding uncommitted changes in ${worktree}\n`)
  }

  // Check for unpushed commits.
  if (hasUnpushed(worktree)) {
    if (!force) {
      // Try to validate landed-content containment.
      const head = sh("git", ["-C", worktree, "rev-parse", "HEAD"])
      if (!head.ok || !isContentLanded(worktree, head.out)) {
        return `${cmd}: ${agentId} has unpushed commits not yet landed. Push or --force.`
      }
      process.stderr.write(`teardown ${agentId}: unpushed commits but content is landed.\n`)
    } else {
      process.stderr.write(`warn: --force: discarding unpushed commits in ${worktree}\n`)
    }
  }
  return null
}

export async function cmdTeardown(args: string[]) {
  if (helpWanted("teardown", args)) return
  const { positionals, opts } = parseArgs("teardown", args, ["--force"])
  const agentId = positionals[0]?.trim()
  if (!agentId) return die("parlay teardown: agent id required", EXIT_USAGE)

  const force = opts["--force"] === true
  const idHome = join(AGENTS_DIR, agentId)
  if (!existsSync(idHome)) {
    return die(`parlay teardown: agent '${agentId}' not found in ${idHome}`, EXIT_USAGE)
  }

  const fm = readIdentity(agentId)
  const worktree = fm.worktree
  const project = fm.project

  // If the agent has no worktree, it's not stranding work. Just deregister and cleanup.
  if (!worktree) {
    await fetch(`${serverUrl()}/api/chat/unregister`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: agentId }),
    }).catch(() => {})
    rmSync(idHome, { recursive: true, force: true })
    console.log(`agent ${agentId} torn down (no worktree)`)
    return
  }

  // Agent has a worktree — check for unlanded work.
  if (!existsSync(worktree)) {
    // Worktree already gone (stale reference).
    await fetch(`${serverUrl()}/api/chat/unregister`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: agentId }),
    }).catch(() => {})
    rmSync(idHome, { recursive: true, force: true })
    console.log(`agent ${agentId} torn down (worktree already gone)`)
    return
  }

  // Refuse to destroy uncommitted changes or unlanded commits.
  const refusal = checkWorktreeGitSafety("parlay teardown", agentId, worktree, force)
  if (refusal) return die(refusal, EXIT_USAGE)

  // Remove the worktree.
  if (project) {
    const r = sh("git", ["-C", project, "worktree", "remove", "--force", worktree])
    if (!r.ok) process.stderr.write(`warn: worktree remove failed — ${r.err}\n`)
  }

  // Deregister from relay.
  await fetch(`${serverUrl()}/api/chat/unregister`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: agentId }),
  }).catch(() => {})

  // Delete agent store (respecting ephemeral marker).
  // For now, always delete. (Firstmate keeps permanent agents' stores; parlay agents are ephemeral by default.)
  rmSync(idHome, { recursive: true, force: true })

  console.log(`agent ${agentId} torn down`)
}
