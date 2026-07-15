#!/usr/bin/env bun
// tools/bump-version.ts — autonomous PA_VERSION bumper, run from the pre-commit hook.
//
// Inspects the STAGED diff of packages/client/src (excluding version.ts itself),
// classifies its magnitude deterministically, bumps PA_VERSION, and re-stages
// version.ts so the bump rides in the same commit. Deterministic-before-AI: the
// classifier is a transparent rubric over the diff, not a model call.
//
//   small  → patch (x.y.Z)   fixes, tweaks, small edits
//   medium → minor (x.Y.0)   new file, net-new export, or a broad change
//   large  → major (X.0.0)   a file deleted, an export removed, or BREAKING
//
// Overrides:  PA_BUMP=major|minor|patch|none  forces the level (none = skip).
// No staged client-src changes ⇒ no bump (silent exit 0).
// Dry run:    --dry (or PA_BUMP_DRY=1) prints the verdict without writing/staging.

import { readFileSync, writeFileSync } from "fs"
import { execSync } from "child_process"

const VERSION_FILE = "packages/client/src/version.ts"
const SRC_PREFIX = "packages/client/src/"
const MINOR_LINE_THRESHOLD = 80   // a change this broad is feature-sized
const MINOR_FILE_THRESHOLD = 4    // touching this many files is feature-sized

type Level = "major" | "minor" | "patch" | "none"

function sh(cmd: string): string {
  try { return execSync(cmd, { encoding: "utf8" }).trim() } catch { return "" }
}

interface Changed { status: string; path: string }

// Staged client-src .ts files (exclude the version file itself), with A/M/D/R status.
function stagedClientChanges(): Changed[] {
  const out = sh("git diff --cached --name-status --diff-filter=ACMRD")
  if (!out) return []
  return out.split("\n").map(line => {
    const parts = line.split("\t")
    return { status: parts[0][0], path: parts[parts.length - 1] }
  }).filter(f =>
    f.path.startsWith(SRC_PREFIX) && f.path !== VERSION_FILE && f.path.endsWith(".ts"),
  )
}

function changedLineTotal(): number {
  const out = sh(`git diff --cached --numstat -- ${SRC_PREFIX}`)
  if (!out) return 0
  let total = 0
  for (const line of out.split("\n")) {
    const [ins, del, path] = line.split("\t")
    if (!path || path === VERSION_FILE || !path.endsWith(".ts")) continue
    total += (Number(ins) || 0) + (Number(del) || 0)
  }
  return total
}

// Count export-bearing lines added vs removed across the staged client diff,
// ignoring the diff's own +++/--- file headers. A net loss of exports is an API
// contraction (breaking); net-new exports signal a feature.
function exportDelta(): { added: number; removed: number } {
  const diff = sh(`git diff --cached -- ${SRC_PREFIX}`)
  let added = 0, removed = 0
  for (const line of diff.split("\n")) {
    if (line.startsWith("+++") || line.startsWith("---")) continue
    if (/^\+.*\bexport\b/.test(line)) added++
    else if (/^-.*\bexport\b/.test(line)) removed++
  }
  return { added, removed }
}

interface Verdict { level: Level; reason: string }

function classify(files: Changed[]): Verdict {
  const override = (process.env.PA_BUMP || "").toLowerCase() as Level
  if (["major", "minor", "patch", "none"].includes(override)) {
    return { level: override, reason: `PA_BUMP override → ${override}` }
  }
  if (files.length === 0) return { level: "none", reason: "no staged client-src changes" }

  const deleted = files.filter(f => f.status === "D").length
  const added = files.filter(f => f.status === "A").length
  const lines = changedLineTotal()
  const exp = exportDelta()
  const diffText = sh(`git diff --cached -- ${SRC_PREFIX}`)
  const breaking = /BREAKING[ _-]?CHANGE/i.test(diffText)

  if (deleted > 0)
    return { level: "major", reason: `${deleted} client file(s) deleted` }
  if (exp.removed > exp.added)
    return { level: "major", reason: `exports removed (${exp.removed} out, ${exp.added} in)` }
  if (breaking)
    return { level: "major", reason: "BREAKING CHANGE in diff" }

  if (added > 0)
    return { level: "minor", reason: `${added} new client file(s)` }
  if (exp.added > 0)
    return { level: "minor", reason: `${exp.added} net-new export(s)` }
  if (lines >= MINOR_LINE_THRESHOLD)
    return { level: "minor", reason: `${lines} lines changed (≥${MINOR_LINE_THRESHOLD})` }
  if (files.length >= MINOR_FILE_THRESHOLD)
    return { level: "minor", reason: `${files.length} files touched (≥${MINOR_FILE_THRESHOLD})` }

  return { level: "patch", reason: `${files.length} file(s), ${lines} lines` }
}

function bumpSemver(v: string, level: Exclude<Level, "none">): string {
  const [maj, min, pat] = v.split(".").map(Number)
  if (level === "major") return `${maj + 1}.0.0`
  if (level === "minor") return `${maj}.${min + 1}.0`
  return `${maj}.${min}.${pat + 1}`
}

function main(): void {
  const files = stagedClientChanges()
  const { level, reason } = classify(files)
  if (level === "none") return   // nothing to bump — silent

  const src = readFileSync(VERSION_FILE, "utf8")
  const m = src.match(/PA_VERSION\s*=\s*['"](\d+)\.(\d+)\.(\d+)['"]/)
  if (!m) { console.error("bump-version: could not find PA_VERSION in " + VERSION_FILE); return }
  const current = `${m[1]}.${m[2]}.${m[3]}`
  const next = bumpSemver(current, level)

  if (process.argv.includes("--dry") || process.env.PA_BUMP_DRY) {
    console.error(`🔎 would bump PA_VERSION ${current} → ${next} (${level}: ${reason})`)
    return
  }

  const updated = src.replace(
    /(PA_VERSION\s*=\s*['"])\d+\.\d+\.\d+(['"])/,
    `$1${next}$2`,
  )
  writeFileSync(VERSION_FILE, updated)
  sh(`git add ${VERSION_FILE}`)
  console.error(`🔖 PA_VERSION ${current} → ${next} (${level}: ${reason})`)
}

main()
