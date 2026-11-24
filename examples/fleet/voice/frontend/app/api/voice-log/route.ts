import { NextResponse } from "next/server";
import { appendFileSync, readFileSync, renameSync, writeFileSync } from "fs";
import { homedir } from "os";
import { join } from "path";

export const dynamic = "force-dynamic";
const DIR = process.env.VOICEMODE_DIR || join(homedir(), ".voicemode");
const at = (name: string) => join(DIR, name);

export async function GET() {
  let phase = "idle";
  let lines: unknown[] = [];
  try { phase = readFileSync(at("page-state.txt"), "utf8").trim() || "idle"; } catch {}
  try {
    lines = readFileSync(at("page-log.jsonl"), "utf8").trim().split("\n").slice(-30).map((l) => JSON.parse(l));
  } catch {}
  let session: unknown = {};
  let turns: unknown[] = [];
  try {
    const t = JSON.parse(readFileSync(at("turns.json"), "utf8"));
    session = t.session || {};
    turns = t.turns || [];
  } catch {}
  return NextResponse.json({ phase, lines, session, turns }, { headers: { "Cache-Control": "no-store" } });
}

// Client-side diagnostics: the page posts events here so an agent can read them.
//
// A "presence" post is the page saying "I am still here" every few seconds. It
// REWRITES one small file (page-presence.json: one entry per open page, keyed
// by that page's identity, stale entries dropped) instead of appending, so the
// health check can tell "page closed" from "page open but off the line" and the
// event log does not grow all day.
const PRESENCE_KEEP_S = 120;
export async function POST(req: Request) {
  try {
    const b = await req.json();
    const ua = req.headers.get("user-agent")?.slice(0, 80);
    if (b && b.event === "presence") {
      const p = b.presence && typeof b.presence === "object" ? b.presence : {};
      const now = Math.floor(Date.now() / 1000);
      const key = typeof p.identity === "string" && /^[A-Za-z0-9_-]{1,64}$/.test(p.identity) ? p.identity : "unknown";
      let pages: Record<string, { epoch?: number }> = {};
      try {
        const have = JSON.parse(readFileSync(at("page-presence.json"), "utf8"));
        if (have && typeof have.pages === "object" && have.pages) pages = have.pages;
      } catch {}
      for (const k of Object.keys(pages)) {
        if (!(typeof pages[k]?.epoch === "number" && now - (pages[k].epoch as number) <= PRESENCE_KEEP_S)) delete pages[k];
      }
      pages[key] = { at: new Date().toISOString(), epoch: now, ua, ...p } as { epoch: number };
      const tmp = at("page-presence.json.tmp");
      writeFileSync(tmp, JSON.stringify({ pages }));
      renameSync(tmp, at("page-presence.json"));
    } else {
      const line = JSON.stringify({ at: new Date().toISOString(), ua, ...b }) + "\n";
      appendFileSync(at("page-client.log"), line);
    }
  } catch {}
  return NextResponse.json({ ok: true });
}
