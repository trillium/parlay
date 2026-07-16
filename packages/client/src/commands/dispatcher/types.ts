// ── Action protocol types (client side; mirrors brain-v4vje §1 + actions.go) ───

export interface Action {
  verb: string
  args?: {
    start?: number
    end?: number
    text?: string
    triggerText?: string
    tail?: boolean
    requireTail?: string
    timerId?: string
    fireInMs?: number
    id?: string
    kind?: 'info' | 'warn'
    channel?: string
    url?: string
    reason?: string
    // Channel picker (see docs/CHANNEL_PICKER_CONTRACT.md)
    prompt?: string
    channels?: PickerChannel[]
  }
}

// A single row in the channel-picker list. `index` is 1-based and is the number
// the user speaks; `label` is the display name; `nickname` is a secondary hint.
export interface PickerChannel {
  index:    number
  id:       string
  label:    string
  nickname: string
}

export interface ActionEnvelope {
  v: number
  streamId: string
  seq: number
  baseVersion: number
  actions: Action[]
  timing?: {
    engineEvalNs?: number
    relayMs?: number
    serverOwnedFire?: boolean
  }
}

export type ApplyResult =
  | 'applied'
  | 'rejected-stale'      // echoed version older than current local version
  | 'rejected-expired'    // TTL exceeded
  | 'rejected-protocol'   // unknown major version
  | 'resync'              // seq gap — request a resync

export const PROTOCOL_V = 1
export const ACTION_TTL_MS = 1500   // an action older than this at receipt is dropped (network stall guard)

// The per-POST context the up-channel needs: the live tab set (server-side
// {agent} resolution), the device/stream ids, the voice gate, and the
// voice-settle tuning knob.
export interface EvalCtx {
  voiceEnabled: boolean
  settleMs: number
  tabs: { id: string; name: string; nicknames: string[] }[]
  device: string
  streamId: string
}
