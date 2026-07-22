import { test, expect } from "bun:test"
import { mkdtempSync, writeFileSync, appendFileSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { parseCreatedId, readNewLines } from "./tail"

test("parseCreatedId extracts a robots id from an emit line", () => {
  expect(parseCreatedId('{"id":"robots-7pg","created_at":"2026-07-22T00:00:00Z","source":"robots-create"}')).toBe("robots-7pg")
})

test("parseCreatedId rejects non-robots ids, malformed json, and missing id", () => {
  expect(parseCreatedId('{"id":"task-abc"}')).toBeNull()
  expect(parseCreatedId("not json")).toBeNull()
  expect(parseCreatedId('{"created_at":"x"}')).toBeNull()
  expect(parseCreatedId('{"id":"robots-"}')).toBeNull()
})

test("readNewLines reads only bytes past the offset (byte-offset tail)", () => {
  const dir = mkdtempSync(join(tmpdir(), "robots-tail-"))
  const f = join(dir, "events.jsonl")
  writeFileSync(f, '{"id":"robots-aaa"}\n')
  const first = readNewLines(f, 0)
  expect(first.lines).toEqual(['{"id":"robots-aaa"}'])
  // From the advanced offset, nothing new until we append.
  expect(readNewLines(f, first.offset).lines).toEqual([])
  appendFileSync(f, '{"id":"robots-bbb"}\n')
  const second = readNewLines(f, first.offset)
  expect(second.lines).toEqual(['{"id":"robots-bbb"}']) // only the NEW line, not robots-aaa
})

test("readNewLines restarts from 0 on truncation (size < offset)", () => {
  const dir = mkdtempSync(join(tmpdir(), "robots-tail-"))
  const f = join(dir, "events.jsonl")
  writeFileSync(f, '{"id":"robots-xxx"}\n{"id":"robots-yyy"}\n')
  const big = readNewLines(f, 0).offset
  writeFileSync(f, '{"id":"robots-zzz"}\n') // truncate + rewrite (size < big)
  const r = readNewLines(f, big)
  expect(r.lines).toEqual(['{"id":"robots-zzz"}'])
})

test("readNewLines on a missing file is a no-op", () => {
  expect(readNewLines(join(tmpdir(), "nope-does-not-exist.jsonl"), 5)).toEqual({ lines: [], offset: 5 })
})
