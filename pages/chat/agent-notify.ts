#!/usr/bin/env bun
/**
 * agent-notify.ts — Long-poll watcher for agent sessions.
 *
 * Replaces watch-inbox.ts (fswatch-based). Blocks on GET /api/chat/poll?after=<id>
 * which the Pulse chat module holds open for 30s before returning {timeout:true}.
 * On a real message: emits one line to stdout and loops immediately.
 * On timeout: loops silently.
 *
 * Usage:
 *   bun agent-notify.ts                       # emits CHAT_MSG|<id>|<role>|<text>
 *   Monitor({ command: "bun agent-notify.ts", persistent: true })
 *
 * Reply:
 *   POST /api/chat/reply  { text: "..." }
 *   or: bun agent-reply.ts "text"
 */

const PULSE   = process.env.PULSE_URL ?? 'http://localhost:4242';
// Optional: filter to messages sent to a specific agent channel.
// Usage: AGENT_CHANNEL=main-agent bun agent-notify.ts  OR  bun agent-notify.ts main-agent
const CHANNEL = process.env.AGENT_CHANNEL ?? process.argv[2] ?? '';

// Seed from current history so we only emit messages that arrive AFTER startup
let lastId = '';
try {
  const seed = await fetch(`${PULSE}/api/chat/history`, { signal: AbortSignal.timeout(5_000) });
  if (seed.ok) {
    const history = await seed.json() as Array<{ id?: string }>;
    if (Array.isArray(history) && history.length > 0) {
      lastId = history[history.length - 1].id ?? '';
    }
  }
} catch { /* pulse not yet up — start from tail */ }

async function poll(): Promise<void> {
  const params = new URLSearchParams()
  if (lastId)  params.set('after',   lastId)
  if (CHANNEL) params.set('channel', CHANNEL)
  const url = `${PULSE}/api/chat/poll${params.size ? `?${params}` : ''}`;
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(35_000) });
    if (!res.ok) {
      await new Promise(r => setTimeout(r, 2_000));
      return;
    }
    const data = await res.json() as { timeout?: boolean; id?: string; role?: string; text?: string };
    if (data.timeout) return; // normal 30s expiry — loop again
    if (data.id && data.role === 'user') {
      lastId = data.id;
      // Emit one line — text newlines escaped so Monitor sees a single-line event
      const safeText = (data.text ?? '').replace(/\n/g, '\\n');
      process.stdout.write(`CHAT_MSG|${data.id}|${data.role}|${safeText}\n`);
    }
  } catch {
    // Network error or timeout — back off briefly
    await new Promise(r => setTimeout(r, 3_000));
  }
}

// Infinite loop — each iteration is one long-poll round
while (true) {
  await poll();
}
