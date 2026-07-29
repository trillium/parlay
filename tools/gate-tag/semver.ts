// Pure semver core for the auto-tagger (task-prk). No git, no I/O — trivially
// unit-tested. The git/CLI half lives in ./gate-tag.ts.
//
// Bump rules (Trillium's parlay semver, 2026-07-17):
//   structural / BREAKING / `type!:`         → MAJOR (N+1.0.0)
//   feat: / fix:                             → MINOR (N.M+1.0)
//   chore/docs/refactor/… / plain word-change → PATCH (N.M.Z+1)
// A feature-scope merge aggregates the HIGHEST bump across the merged range, so a
// 60-commit fast-forward tags once at the strongest bump it contains.

export type Level = "major" | "minor" | "patch" | "none"

export interface Version {
  major: number
  minor: number
  patch: number
}

const LEVEL_RANK: Record<Level, number> = { none: 0, patch: 1, minor: 2, major: 3 }

// Parse "v4.15.0" / "4.15.0" → {major,minor,patch}. Returns null if unparseable.
export function parseVersion(tag: string): Version | null {
  const m = tag.trim().match(/^v?(\d+)\.(\d+)\.(\d+)$/)
  if (!m) return null
  return { major: Number(m[1]), minor: Number(m[2]), patch: Number(m[3]) }
}

export function formatVersion(v: Version): string {
  return `v${v.major}.${v.minor}.${v.patch}`
}

// Classify ONE commit message → its bump level (never "none"; every real commit
// is at least a patch under Trillium's rules).
export function classifyCommit(subject: string, body = ""): Exclude<Level, "none"> {
  const msg = `${subject}\n${body}`
  // MAJOR markers: a BREAKING CHANGE footer, a `type!:` bang, or a [major]/[structural] tag.
  if (/BREAKING[ _-]?CHANGE/i.test(msg)) return "major"
  const m = subject.match(/^(\w+)(?:\([^)]*\))?(!)?:/)
  if (m && m[2] === "!") return "major"
  if (/\[(?:major|structural)\]/i.test(subject)) return "major"
  // MINOR: feat or fix (Trillium's scheme puts BOTH at minor, unlike vanilla semver).
  const type = m ? m[1].toLowerCase() : ""
  if (type === "feat" || type === "fix") return "minor"
  // Everything else (chore/docs/refactor/style/perf/test/build/ci or no prefix) → patch.
  return "patch"
}

// Aggregate the highest bump across a set of commit messages. Empty → "none".
export function aggregateBump(commits: { subject: string; body?: string }[]): Level {
  let best: Level = "none"
  for (const c of commits) {
    const level = classifyCommit(c.subject, c.body ?? "")
    if (LEVEL_RANK[level] > LEVEL_RANK[best]) best = level
  }
  return best
}

// Apply a bump to a version. "none" is a no-op (returns the same version).
export function nextVersion(current: Version, level: Level): Version {
  switch (level) {
    case "major": return { major: current.major + 1, minor: 0, patch: 0 }
    case "minor": return { major: current.major, minor: current.minor + 1, patch: 0 }
    case "patch": return { major: current.major, minor: current.minor, patch: current.patch + 1 }
    case "none": return { ...current }
  }
}
