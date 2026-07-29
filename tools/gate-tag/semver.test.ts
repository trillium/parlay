import { test, expect } from "bun:test"
import { parseVersion, formatVersion, classifyCommit, aggregateBump, nextVersion } from "./semver"

test("parseVersion accepts v-prefixed and bare, rejects junk", () => {
  expect(parseVersion("v4.15.0")).toEqual({ major: 4, minor: 15, patch: 0 })
  expect(parseVersion("4.15.0")).toEqual({ major: 4, minor: 15, patch: 0 })
  expect(parseVersion("v4.15")).toBeNull()
  expect(parseVersion("nope")).toBeNull()
})

test("formatVersion round-trips", () => {
  expect(formatVersion({ major: 4, minor: 15, patch: 0 })).toBe("v4.15.0")
})

test("classifyCommit: feat and fix → minor (Trillium's scheme)", () => {
  expect(classifyCommit("feat(cli): add robots-tail")).toBe("minor")
  expect(classifyCommit("fix(relay): cursor poisoning")).toBe("minor")
})

test("classifyCommit: chore/docs/refactor/no-prefix → patch", () => {
  expect(classifyCommit("chore(gitignore): ignore junk")).toBe("patch")
  expect(classifyCommit("docs: update readme")).toBe("patch")
  expect(classifyCommit("refactor: extract helper")).toBe("patch")
  expect(classifyCommit("tweak a word")).toBe("patch")
})

test("classifyCommit: major via bang, BREAKING CHANGE, or [structural]", () => {
  expect(classifyCommit("feat!: drop legacy poll")).toBe("major")
  expect(classifyCommit("fix!: change endpoint shape")).toBe("major")
  expect(classifyCommit("feat: rework engine", "BREAKING CHANGE: manifest format changed")).toBe("major")
  expect(classifyCommit("feat: [structural] split the store")).toBe("major")
  expect(classifyCommit("feat: normal", "BREAKING-CHANGE: x")).toBe("major")
})

test("aggregateBump takes the highest across the range (ff-of-many)", () => {
  expect(aggregateBump([
    { subject: "chore: a" }, { subject: "feat: b" }, { subject: "docs: c" },
  ])).toBe("minor")
  expect(aggregateBump([
    { subject: "chore: a" }, { subject: "feat!: b" }, { subject: "feat: c" },
  ])).toBe("major")
  expect(aggregateBump([{ subject: "chore: a" }, { subject: "docs: b" }])).toBe("patch")
  expect(aggregateBump([])).toBe("none")
})

test("nextVersion bumps correctly and zeroes lower fields", () => {
  const v = { major: 4, minor: 15, patch: 0 }
  expect(formatVersion(nextVersion(v, "major"))).toBe("v5.0.0")
  expect(formatVersion(nextVersion(v, "minor"))).toBe("v4.16.0")
  expect(formatVersion(nextVersion(v, "patch"))).toBe("v4.15.1")
  expect(formatVersion(nextVersion(v, "none"))).toBe("v4.15.0")
})

test("nextVersion patch bump preserves patch count", () => {
  expect(formatVersion(nextVersion({ major: 4, minor: 12, patch: 1 }, "patch"))).toBe("v4.12.2")
})
