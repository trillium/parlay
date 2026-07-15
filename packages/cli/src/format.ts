// parlay CLI message formatting: truncation, sender label, and line rendering.

import { TRUNCATE_AT } from "./config"
import type { ChatMessage } from "./types"

export function truncate(text: string, max = TRUNCATE_AT): string {
  const oneLine = text.replace(/\n/g, " ⏎ ")
  if (oneLine.length <= max) return oneLine
  return `${oneLine.slice(0, max)}… (+${oneLine.length - max} chars)`
}

export function who(m: ChatMessage): string {
  return m.role === "agent" ? (m.channel ?? "agent") : (m.type === "alert" ? "alert" : "you")
}

export function fmtMsg(m: ChatMessage, full: boolean): string {
  const ts = m.ts?.slice(11, 19) ?? ""
  if (full) return `[${ts}] ${who(m).padEnd(12)} id=${m.id} channel=${m.channel ?? "-"}\n  ${m.text}`
  return `[${ts}] ${who(m).padEnd(12)} ${truncate(m.text)}`
}

export function nextStep(template: string) {
  console.log(`\nNext: ${template}`)
}
