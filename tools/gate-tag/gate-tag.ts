#!/usr/bin/env bun
// gate-tag — auto-create the semver git tag for a feature-scope merge (task-prk).
//
// Finds the last v* tag, aggregates the HIGHEST semver bump across the commits
// since it (conventional-commit prefixes + BREAKING/`!`/[structural] markers, per
// ./semver.ts), computes the next version, and creates an ANNOTATED tag — so the
// merge gate never hand-tags. Handles the fast-forward-of-many-commits case (it
// scans the whole range). Idempotent + safe:
//   • if HEAD already carries a v* tag → no-op (report + exit 0).
//   • if the computed version tag already exists but NOT at HEAD → refuse (exit 1),
//     never force/overwrite.
//   • no commits since the last tag → nothing to do (exit 0).
//
// Usage:
//   gate-tag              dry-run: print the range, bump, and the tag it WOULD create
//   gate-tag --apply      create the annotated tag locally
//   gate-tag --apply --push   also `git push origin <tag>` (external — off by default)
//   gate-tag --from <tag> override the base tag (else the newest v* by version)
//
// Exit: 0 ok/no-op, 1 conflict/error, 2 usage.

import { execSync } from "child_process"
import { parseVersion, formatVersion, aggregateBump, classifyCommit, nextVersion, type Level } from "./semver"

function sh(cmd: string): string {
  return execSync(cmd, { encoding: "utf8" }).trim()
}
function shSafe(cmd: string): string {
  try { return sh(cmd) } catch { return "" }
}

interface Commit { hash: string; subject: string; body: string }

function pushTag(tag: string): number {
  try {
    sh(`git push origin ${tag}`)
    console.log(`gate-tag: pushed ${tag} to origin`)
    return 0
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    console.error(`gate-tag: push of ${tag} failed — ${detail}`)
    console.error(`  the local tag exists; re-run with --apply --push to retry the push.`)
    return 1
  }
}

// Newest v* tag by version order (not creation date — reorders survive).
// for-each-ref never columnizes (unlike `git tag --list`, which honors column.ui).
function lastTag(override?: string): string | null {
  if (override) return override
  const out = shSafe("git for-each-ref --sort=-v:refname --format='%(refname:short)' 'refs/tags/v[0-9]*'")
  const first = out.split("\n").filter(Boolean)[0]
  return first || null
}

function headVTags(): string[] {
  const out = shSafe("git for-each-ref --points-at HEAD --format='%(refname:short)' 'refs/tags/v[0-9]*'")
  return out.split("\n").filter((t) => /^v\d+\.\d+\.\d+$/.test(t))
}

function tagExists(tag: string): boolean {
  return shSafe(`git rev-parse -q --verify refs/tags/${tag}`) !== ""
}

// Commits in <base>..HEAD (or all history if no base), newest first.
function commitsSince(base: string | null): Commit[] {
  const range = base ? `${base}..HEAD` : "HEAD"
  // Records separated by \x1e, fields by \x1f (safe against newlines in bodies).
  const raw = shSafe(`git log ${range} --format=%H%x1f%s%x1f%b%x1e`)
  if (!raw) return []
  return raw.split("\x1e").map((r) => r.trim()).filter(Boolean).map((r) => {
    const [hash, subject, body] = r.split("\x1f")
    return { hash: hash ?? "", subject: subject ?? "", body: body ?? "" }
  })
}

// The first commit (newest) whose own level equals the aggregate — for the annotation.
function drivingCommit(commits: Commit[], level: Level): Commit | undefined {
  return commits.find((c) => classifyCommit(c.subject, c.body) === level)
}

function main(): number {
  const args = process.argv.slice(2)
  if (args.includes("-h") || args.includes("--help")) {
    console.log("gate-tag [--apply] [--push] [--from <tag>] — auto-tag the semver bump for a merge (task-prk)")
    return 0
  }
  const apply = args.includes("--apply")
  const push = args.includes("--push")
  const fromIdx = args.indexOf("--from")
  const from = fromIdx >= 0 ? args[fromIdx + 1] : undefined
  if (from !== undefined && !/^v?\d+\.\d+\.\d+$/.test(from)) {
    console.error(`gate-tag: --from '${from ?? ""}' must be a vX.Y.Z version tag.`)
    return 2
  }

  // Idempotency: already tagged at HEAD? If a push was requested, re-attempt it
  // (a prior run may have created the tag locally but failed to push) so a gate
  // retry recovers instead of no-op-exiting with the tag missing from origin.
  const existing = headVTags()
  if (existing.length) {
    if (push) {
      let rc = 0
      for (const t of existing) rc = pushTag(t) || rc
      return rc
    }
    console.log(`gate-tag: HEAD already tagged ${existing.join(", ")} — nothing to do.`)
    return 0
  }

  const base = lastTag(from)
  const current = base ? parseVersion(base) : { major: 0, minor: 0, patch: 0 }
  if (!current) {
    console.error(`gate-tag: base tag '${base}' is not a vX.Y.Z version.`)
    return 1
  }

  const commits = commitsSince(base)
  if (commits.length === 0) {
    console.log(`gate-tag: no commits since ${base ?? "(root)"} — nothing to tag.`)
    return 0
  }

  const level = aggregateBump(commits)
  if (level === "none") {
    console.log(`gate-tag: ${commits.length} commit(s) since ${base}, but bump is none — nothing to tag.`)
    return 0
  }

  const next = nextVersion(current, level)
  const tag = formatVersion(next)
  const driver = drivingCommit(commits, level)
  const shortHead = shSafe("git rev-parse --short HEAD")

  console.log(`gate-tag: ${base ?? "(root)"} → ${tag}  (${level} bump from ${commits.length} commit(s))`)
  console.log(`  driven by: ${driver ? `${driver.hash.slice(0, 8)} ${driver.subject}` : "(n/a)"}`)

  if (tagExists(tag)) {
    console.error(`gate-tag: refusing — ${tag} already exists but not at HEAD. Resolve manually (never force).`)
    return 1
  }

  const message =
    `parlay ${tag}\n\n` +
    `Auto-tagged by gate-tag (task-prk): ${level} bump across ${commits.length} commit(s) since ${base ?? "(root)"}.\n` +
    `Range: ${base ?? "(root)"}..${shortHead}\n` +
    (driver ? `Highest bump: ${driver.subject}\n` : "")

  if (!apply) {
    console.log(`  [dry-run] would create annotated tag ${tag}. Re-run with --apply to create it.`)
    return 0
  }

  // Pass the multi-line message on stdin (-F -) to avoid shell-escaping the body.
  execSync(`git tag -a ${tag} -F -`, { input: message })
  console.log(`gate-tag: created annotated tag ${tag}`)
  if (push) {
    return pushTag(tag)
  }
  console.log(`  (local only — push with: git push origin ${tag})`)
  return 0
}

process.exit(main())
