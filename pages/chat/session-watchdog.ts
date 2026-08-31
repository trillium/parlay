#!/usr/bin/env bun
/**
 * session-watchdog.ts — monitors a tmux pane for Claude session health.
 *
 * Sends alerts via POST /api/chat/reply when:
 *   - Pane content matches a login/auth prompt pattern
 *   - Pane content is unchanged for STALE_THRESHOLD consecutive polls
 *
 * Usage:
 *   bun ~/pulse-pages/chat/session-watchdog.ts [tmux-target]
 *
 * tmux-target examples: "yolo:0", "yolo:claude", "%12" (pane ID)
 * Defaults to $TMUX_PANE if set, otherwise "yolo:0"
 */

const PULSE       = process.env.PULSE_URL ?? 'http://localhost:4242'
const POLL_MS     = 15_000   // check every 15s
const STALE_MAX   = 4        // 4 unchanged polls (~60s) before stale alert
const STALE_RESET = 10       // reset stale count after this many active polls

const LOGIN_PATTERN = /(?:log\s+in|sign\s+in|authenticate|session\s+expired|permission\s+denied|claude\.ai\/login|press\s+enter\s+to\s+continue|account\s+required)/i

const target = Bun.argv[2] ?? process.env.TMUX_PANE ?? 'yolo:0'

let lastHash    = ''
let staleCount  = 0
let activeCount = 0
let loginAlerted = false
let staleAlerted = false

async function alert(msg: string) {
  try {
    await fetch(`${PULSE}/api/chat/reply`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: `⚠️ Watchdog [${target}]: ${msg}` }),
    })
  } catch { /* pulse may be down */ }
}

async function getPaneContent(): Promise<string> {
  const proc = Bun.spawnSync(['tmux', 'capture-pane', '-p', '-t', target])
  if (proc.exitCode !== 0) throw new Error('tmux capture failed')
  return proc.stdout.toString()
}

async function hashStr(s: string): Promise<string> {
  const buf = new TextEncoder().encode(s)
  const h   = await crypto.subtle.digest('SHA-256', buf)
  return Array.from(new Uint8Array(h)).map(b => b.toString(16).padStart(2, '0')).join('').slice(0, 16)
}

process.stdout.write(`Session watchdog started — target: ${target}, poll: ${POLL_MS}ms\n`)

while (true) {
  await new Promise<void>(r => setTimeout(r, POLL_MS))

  let content: string
  try {
    content = await getPaneContent()
  } catch {
    // tmux not available or target gone — back off
    continue
  }

  const hash = await hashStr(content)

  // Login / auth pattern check
  if (LOGIN_PATTERN.test(content)) {
    if (!loginAlerted) {
      loginAlerted = true
      staleAlerted = false
      staleCount   = 0
      await alert('login prompt detected — Claude may need re-authentication')
    }
  } else {
    loginAlerted = false
  }

  // Stale content check
  if (hash === lastHash) {
    staleCount++
    activeCount = 0
    if (staleCount >= STALE_MAX && !staleAlerted && !loginAlerted) {
      staleAlerted = true
      const secs = Math.round((POLL_MS * staleCount) / 1000)
      await alert(`pane content unchanged for ~${secs}s — session may be stuck or waiting`)
    }
  } else {
    staleCount = 0
    staleAlerted = false
    activeCount++
  }

  lastHash = hash
}
