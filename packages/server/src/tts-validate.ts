// POST /api/chat/tts/validate-splits — evaluate whether parlay's splitBlocksRaw
// output lands at natural spoken-pause boundaries.
// Calls the local Ollama server (gemma4 by default) so it runs offline, fast.
//
// Request:  { "text": string, "model"?: string }
// Response: { blocks, evaluation: { overall_score, verdict, issues, suggestion }, ms }
//
// The split algorithm here mirrors the pure splitBlocksRaw in
// packages/client/src/speech-segment.ts exactly — including the task-1h47
// link-boundary deferral, so a URL that straddles a sentence boundary stays whole
// and the block preview matches the live panel. Keep both in sync when the 60-char
// threshold, sentence regex, or link grammar changes.

import { CORS } from "./sse"

const OLLAMA_URL = process.env.OLLAMA_URL ?? "http://localhost:11434"
const DEFAULT_MODEL = "gemma4:latest"
const TIMEOUT_MS = 30_000

interface SplitBlock { synth: string; raw: string }

// Mirror of linkRanges in packages/client/src/speech-segment.ts — char ranges of
// links in the raw text, used to keep passage boundaries off the middle of an anchor.
function linkRanges(text: string): { start: number; end: number }[] {
  const re = /\[[^\]]+\]\((?:https?:\/\/[^\s)]+)\)|(?:https?:\/\/[^\s"']+)/g
  const ranges: { start: number; end: number }[] = []
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) ranges.push({ start: m.index, end: m.index + m[0].length })
  return ranges
}

// A boundary strictly inside a link would split the anchor across two blocks.
function insideLink(pos: number, links: { start: number; end: number }[]): boolean {
  return links.some((l) => pos > l.start && pos < l.end)
}

// Mirror of splitBlocksRaw in packages/client/src/speech-segment.ts. A block
// boundary that would land INSIDE a link is deferred until past the link, so a URL
// spanning two sentences stays whole in the passage it STARTS in (task-1h47).
function splitBlocksRaw(text: string): SplitBlock[] {
  const links = linkRanges(text)
  const blocks: SplitBlock[] = []
  let start = 0 // char offset of the start of the pending block within `text`
  for (const m of text.matchAll(/[^.!?\n]+[.!?]*\s*/g)) {
    const boundary = m.index + m[0].length
    const cur = text.slice(start, boundary)
    if (cur.trim().length >= 60 && !insideLink(boundary, links)) {
      blocks.push({ synth: cur.trim(), raw: cur })
      start = boundary
    }
  }
  const tail = text.slice(start)
  if (tail.trim()) blocks.push({ synth: tail.trim(), raw: tail })
  return blocks.length ? blocks : [{ synth: text.trim(), raw: text }]
}

const SCHEMA = {
  type: "object",
  properties: {
    overall_score: { type: "integer", description: "1–5 (5=all splits natural, 1=awkward mid-phrase cuts)" },
    verdict: { type: "string", enum: ["good", "acceptable", "poor"] },
    issues: { type: "array", items: { type: "string" }, description: "Specific problems quoting the awkward boundary text" },
    suggestion: { type: "string", description: "Alternative split strategy; empty string when verdict is good" },
  },
  required: ["overall_score", "verdict", "issues", "suggestion"],
}

async function ollamaGenerate(model: string, prompt: string): Promise<Record<string, unknown>> {
  const res = await fetch(`${OLLAMA_URL}/api/generate`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model, prompt, stream: false, format: SCHEMA, options: { temperature: 0.1 } }),
    signal: AbortSignal.timeout(TIMEOUT_MS),
  })
  if (!res.ok) throw new Error(`Ollama ${res.status}: ${await res.text()}`)
  const data = await res.json() as { response: string }
  return JSON.parse(data.response) as Record<string, unknown>
}

export function handleTTSValidateRequest(req: Request, pathname: string): Response | Promise<Response> | null {
  if (req.method !== "POST" || pathname !== "/api/chat/tts/validate-splits") return null
  return (async () => {
    const json = (b: unknown, status = 200) =>
      new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json", ...CORS } })
    let text: string, model: string
    try {
      const body = await req.json() as Record<string, string>
      text = String(body.text ?? "").trim()
      model = String(body.model ?? DEFAULT_MODEL).trim() || DEFAULT_MODEL
    } catch { return json({ error: "bad request" }, 400) }
    if (!text) return json({ error: "text required" }, 400)

    const blocks = splitBlocksRaw(text)
    const blockList = blocks.map((b, i) => `  Block ${i + 1}: "${b.synth}"`).join("\n")
    const prompt = [
      "You evaluate text splits for a text-to-speech (TTS) system.",
      "Each block is spoken as a separate utterance; splits must land at natural",
      "clause or sentence boundaries where a speaker would naturally pause.",
      "",
      `Original text:\n  "${text}"`,
      "",
      `Split into ${blocks.length} block(s):\n${blockList}`,
      "",
      "Evaluate whether the block boundaries feel like natural spoken pauses.",
      "Respond with overall_score (1–5), verdict, issues (concrete), and suggestion.",
    ].join("\n")

    try {
      const t0 = Date.now()
      const evaluation = await ollamaGenerate(model, prompt)
      return json({ blocks, evaluation, model, ms: Date.now() - t0 })
    } catch (err) {
      return json({ error: err instanceof Error ? err.message : "inference failed" }, 502)
    }
  })()
}
