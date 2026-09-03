#!/usr/bin/env bun
// server.ts — SMS voice-flow prototype backend for discussion #240.
//
// Binds 127.0.0.1 ONLY — never exposed off-machine, so no auth story is
// needed. Serves the static page/JS and three endpoints that shell out to
// the `imsg` CLI (or, in --demo mode, seeded fake data — see demo-data.js).
//
// Flags:
//   --demo         serve fake seeded data, never touch imsg or chat.db
//   --allow-send   required (together with the page's LIVE toggle) before
//                  /api/send will actually invoke `imsg send`. Without it,
//                  every /api/send call is a dry run no matter what the
//                  client requests — this is the server-side half of the
//                  double gate described in README.md.
//   --port=N       listen port (default 8787)

import { assignStandIns } from './grammar.js';
import { DEMO_CHATS, DEMO_HISTORY } from './demo-data.js';

const argv = process.argv.slice(2);
const DEMO = argv.includes('--demo');
const ALLOW_SEND = argv.includes('--allow-send');
const portArg = argv.find((a) => a.startsWith('--port='));
const PORT = portArg ? Number(portArg.slice('--port='.length)) : 8787;

const DIR = new URL('.', import.meta.url).pathname;

async function runImsg(args: string[]): Promise<{ ok: boolean; stdout: string; stderr: string }> {
  const proc = Bun.spawn(['imsg', ...args], { stdout: 'pipe', stderr: 'pipe' });
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);
  return { ok: exitCode === 0, stdout, stderr };
}

function parseJsonLines(text: string): any[] {
  const rows: any[] = [];
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    try {
      rows.push(JSON.parse(trimmed));
    } catch {
      // skip a non-JSON log line rather than fail the whole response
    }
  }
  return rows;
}

function quoteArg(arg: string): string {
  if (/^[A-Za-z0-9_@+.\/:-]+$/.test(arg)) return arg;
  return `'${arg.replace(/'/g, `'\\''`)}'`;
}

function formatCommand(args: string[]): string {
  return ['imsg', ...args].map(quoteArg).join(' ');
}

async function getChats() {
  if (DEMO) {
    const withStandIns = assignStandIns(DEMO_CHATS.map(({ id, displayName }) => ({ id, displayName })));
    return withStandIns.map((c, i) => ({ ...c, lastMessage: DEMO_CHATS[i].lastMessage, lastActivity: DEMO_CHATS[i].lastMessageAt }));
  }
  const { ok, stdout, stderr } = await runImsg(['chats', '--limit', '15', '--json']);
  if (!ok) throw new Error(`imsg chats failed: ${stderr.slice(0, 500)}`);
  const rows = parseJsonLines(stdout);
  const bare = rows.map((r) => ({ id: r.id, displayName: r.name?.trim() ? r.name : r.identifier }));
  const withStandIns = assignStandIns(bare);
  return withStandIns.map((c, i) => ({ ...c, lastMessage: null, lastActivity: rows[i].last_message_at }));
}

async function getHistory(chatId: number) {
  if (DEMO) {
    return DEMO_HISTORY[chatId] ?? [];
  }
  const { ok, stdout, stderr } = await runImsg(['history', '--chat-id', String(chatId), '--limit', '20', '--json']);
  if (!ok) throw new Error(`imsg history failed: ${stderr.slice(0, 500)}`);
  const rows = parseJsonLines(stdout);
  return rows
    .map((r) => ({ sender: r.is_from_me ? 'me' : r.sender, text: r.text, isFromMe: !!r.is_from_me, createdAt: r.created_at }))
    .reverse();
}

async function postSend(chatId: number, text: string, armed: boolean) {
  const args = ['send', '--chat-id', String(chatId), '--text', text, '--service', 'auto'];
  const command = formatCommand(args);

  if (DEMO) {
    return { ranLive: false, command, reason: 'demo mode never sends — this is fake seeded data' };
  }
  if (!armed) {
    return { ranLive: false, command, reason: 'dry run — flip the LIVE toggle to arm sending' };
  }
  if (!ALLOW_SEND) {
    return { ranLive: false, command, reason: 'server was not started with --allow-send' };
  }
  const { ok, stdout, stderr } = await runImsg(args);
  return { ranLive: true, command, ok, output: ok ? stdout.trim() : stderr.trim() };
}

const STATIC_FILES: Record<string, string> = {
  '/': 'index.html',
  '/index.html': 'index.html',
  '/grammar.js': 'grammar.js',
};

Bun.serve({
  hostname: '127.0.0.1',
  port: PORT,
  async fetch(req) {
    const url = new URL(req.url);

    const staticFile = STATIC_FILES[url.pathname];
    if (staticFile && req.method === 'GET') {
      const type = staticFile.endsWith('.js') ? 'text/javascript' : 'text/html';
      return new Response(Bun.file(DIR + staticFile), { headers: { 'content-type': type } });
    }

    if (url.pathname === '/api/chats' && req.method === 'GET') {
      try {
        const chats = await getChats();
        return Response.json({ chats, demo: DEMO });
      } catch (err) {
        return Response.json({ error: String(err) }, { status: 500 });
      }
    }

    if (url.pathname === '/api/history' && req.method === 'GET') {
      const chatId = Number(url.searchParams.get('chatId'));
      if (!Number.isFinite(chatId)) return Response.json({ error: 'chatId is required' }, { status: 400 });
      try {
        const history = await getHistory(chatId);
        return Response.json({ history });
      } catch (err) {
        return Response.json({ error: String(err) }, { status: 500 });
      }
    }

    if (url.pathname === '/api/send' && req.method === 'POST') {
      let body: any;
      try {
        body = await req.json();
      } catch {
        return Response.json({ error: 'invalid JSON body' }, { status: 400 });
      }
      const chatId = Number(body.chatId);
      const text = String(body.text ?? '');
      const armed = body.armed === true;
      if (!Number.isFinite(chatId) || !text) {
        return Response.json({ error: 'chatId and text are required' }, { status: 400 });
      }
      try {
        const result = await postSend(chatId, text, armed);
        return Response.json(result);
      } catch (err) {
        return Response.json({ error: String(err) }, { status: 500 });
      }
    }

    return new Response('not found', { status: 404 });
  },
});

console.log(
  `sms-voice-prototype listening on http://127.0.0.1:${PORT}` +
    (DEMO ? ' [demo mode: fake data, never sends]' : '') +
    (!DEMO && ALLOW_SEND ? ' [--allow-send: live sending can be armed from the page]' : !DEMO ? ' [dry-run only: start with --allow-send to permit live sending]' : ''),
);
