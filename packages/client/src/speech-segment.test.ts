import { test, expect } from "bun:test"
import { splitBlocksRaw, linkRanges } from "./speech-segment"

test("linkRanges finds bare and markdown links", () => {
  const t = "see https://a.com/x and [label](https://b.com/y) end"
  const r = linkRanges(t)
  expect(r.length).toBe(2)
  expect(t.slice(r[0].start, r[0].end)).toBe("https://a.com/x")
  expect(t.slice(r[1].start, r[1].end)).toBe("[label](https://b.com/y)")
})

test("raw blocks always concatenate back to the original text (pre-wrap invariant)", () => {
  const t = "First sentence here that is plenty long. Second one is also quite long indeed. Third short."
  expect(splitBlocksRaw(t).map((b) => b.raw).join("")).toBe(t)
})

test("a URL with internal dots is never split across passages (task-1h47)", () => {
  // A long first clause forces a break boundary right around the link; the URL's
  // internal '.'s would otherwise let the sentence splitter cut it in half.
  const url = "https://example.com/a.b.c/page"
  const t = `Here is a long enough opening clause to force a passage break near the link ${url} here, and then a good deal more trailing text that is itself plenty long to stand as its own passage.`
  const blocks = splitBlocksRaw(t)
  // The full URL appears intact in exactly one passage — never fragmented.
  expect(blocks.filter((b) => b.raw.includes(url)).length).toBe(1)
  // No passage holds a broken half of the link.
  for (const b of blocks) {
    if (b.raw.includes("https://example.com")) expect(b.raw).toContain(url)
  }
})

test("leading newline/punctuation keeps offsets aligned so a link is never split", () => {
  // A leading '\n' is dropped by the sentence regex; the boundary offset must
  // still track the ACTUAL position in `text` (matchAll m.index) or insideLink
  // would check the wrong spot and cut the URL.
  const url = "https://example.com/a.b.c/page"
  const t = `\n\nHere is a long enough opening clause to force a passage break near the link ${url} here, and then a good deal more trailing text that is plenty long to stand on its own.`
  const blocks = splitBlocksRaw(t)
  expect(blocks.filter((b) => b.raw.includes(url)).length).toBe(1)
  for (const b of blocks) {
    if (b.raw.includes("https://example.com")) expect(b.raw).toContain(url)
  }
  // Raw-concat invariant holds even with the dropped leading newline.
  expect(blocks.map((b) => b.raw).join("")).toBe(t)
})

test("multiple passages still form when there are no links (normal segmentation)", () => {
  const t = "Sentence one is long enough to be a block. Sentence two is also long enough here. Three."
  expect(splitBlocksRaw(t).length).toBeGreaterThan(1)
})
