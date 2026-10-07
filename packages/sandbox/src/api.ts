/**
 * The sandbox's client for the three server surfaces it uses.
 *
 * Everything here goes to the SAME routes production uses — there is no sandbox
 * endpoint and no sandbox-only protocol:
 *
 *   POST /api/chat/eval         evaluate a string (preview) or deliver it (fire)
 *   GET  /api/chat/action-log   the command log, with filters
 *   POST /api/chat/off-switch   turn a connection or an action off / back on
 *
 * PREVIEW vs FIRE is decided by the stream id and nothing else. The server
 * treats a `sandbox-preview-` stream as never-deliverable: it evaluates it
 * against the real engine and refuses to broadcast the result — and refuses the
 * engine's own deferred submit fire for that stream too. A `sandbox-fire-`
 * stream has no such marker and is delivered exactly like a live panel's.
 *
 * The marker lives on the wire, so the distinction is visible in every log
 * record rather than only in this file.
 */

import type { SandboxConfig } from './configs'

export const PREVIEW_PREFIX = 'sandbox-preview-'

export const FIRE_PREFIX = 'sandbox-fire-'

export interface ParlayAction {
  verb: string
  args?: Record<string, unknown>
}

export interface EvalResponse {
  ok?: boolean
  refused?: string
  action?: string
  device?: string
  sseClients?: number
  actions?: ParlayAction[]
  engineEvalNs?: number
  fired?: string
  error?: string
  hint?: string
}

export interface OffSwitchResponse {
  ok: boolean
  kind: string
  id: string
  off: boolean
  changed: boolean
  targets: OffTarget[]
  error?: string
}

/** A monotonic per-page nonce, so each preview/fire gets its own stream and two
 *  evaluations of the same string cannot be mistaken for one another (the
 *  engine's per-stream version counter would misread a reused id). */
let nonce = 0

export function nextNonce(): number {
  nonce += 1
  return nonce
}

export function previewStreamId(configId: string): string {
  return `${PREVIEW_PREFIX}${configId}-${nextNonce()}`
}

export function fireStreamId(configId: string): string {
  return `${FIRE_PREFIX}${configId}-${nextNonce()}`
}

/** isPreviewStream mirrors the server's own test, for the page's labels. */
export function isPreviewStream(streamId: string): boolean {
  return streamId.startsWith(PREVIEW_PREFIX)
}

export interface EvalBody {
  streamId: string
  device: string
  version: number
  text: string
  cursor: { anchor: number; active: number }
  reason: string
  voiceEnabled: boolean
  tabs: unknown[]
  platform?: string
  commands?: unknown
}

/**
 * buildEvalBody assembles the request exactly as the production input wrapper
 * does (`packages/input`): full buffer, monotonic version, cursor at the end,
 * `voiceEnabled` true because command matching is gated on it, and the tab set
 * the engine resolves `{agent}` against.
 *
 * `text` is sent and never stored by the sandbox: the log records what the
 * string RESOLVED TO, not what it was, and nothing here keeps a copy.
 */
export function buildEvalBody(opts: {
  config: SandboxConfig
  text: string
  streamId: string
  device: string
  version: number
  tabs?: { id: string; name: string; nicknames: string[] }[]
  reason?: string
}): EvalBody {
  return {
    streamId: opts.streamId,
    device: opts.device,
    version: opts.version,
    text: opts.text,
    cursor: { anchor: opts.text.length, active: opts.text.length },
    reason: opts.reason ?? (isPreviewStream(opts.streamId) ? 'sandbox-preview' : 'sandbox-fire'),
    voiceEnabled: true,
    tabs: opts.tabs ?? [],
    platform: opts.config.platform || undefined,
    commands: opts.config.manifest,
  }
}

async function postJSON<T>(fetchImpl: typeof fetch, url: string, body: unknown): Promise<T> {
  const res = await fetchImpl(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const text = await res.text()
  let parsed: unknown
  try {
    parsed = text ? JSON.parse(text) : {}
  } catch {
    throw new Error(`${url}: server replied HTTP ${res.status} with a non-JSON body`)
  }
  if (!res.ok) {
    const err = (parsed as { error?: string }).error
    throw new Error(`${url}: HTTP ${res.status}${err ? ` — ${err}` : ''}`)
  }
  return parsed as T
}

/** evaluate sends one string for evaluation. Preview or fire is decided by the
 *  stream id the caller built, not by a flag here. */
export function evaluate(fetchImpl: typeof fetch, body: EvalBody, server = ''): Promise<EvalResponse> {
  return postJSON<EvalResponse>(fetchImpl, `${server}/api/chat/eval`, body)
}

export function setOff(
  fetchImpl: typeof fetch,
  target: { kind: string; id: string; off: boolean },
  server = '',
  by = 'sandbox',
): Promise<OffSwitchResponse> {
  return postJSON<OffSwitchResponse>(fetchImpl, `${server}/api/chat/off-switch`, {
    kind: target.kind,
    id: target.id,
    off: target.off,
    by,
    surface: 'website',
  })
}
