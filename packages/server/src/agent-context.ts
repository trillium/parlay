import { join } from "path"
import { existsSync, readFileSync } from "fs"

// Load agent context from JSON file (primary) or env var (fallback).
// Context file: ~/.parlay/agents/<id>/context.json
export function loadAgentContext(idFromBody?: string): { id: string; context?: Record<string, unknown> } | null {
  const id = (idFromBody || process.env.PARLAY_AGENT_ID || "").trim()
  if (!id) return null

  // Try JSON file first
  const home = process.env.HOME || process.env.USERPROFILE || ""
  const contextPath = join(home, ".parlay", "agents", id, "context.json")

  if (existsSync(contextPath)) {
    try {
      const context = JSON.parse(readFileSync(contextPath, "utf8"))
      return { id, context }
    } catch {
      // Fallback to env var if JSON parsing fails
    }
  }

  // Fallback: env var present, use it
  if (process.env.PARLAY_AGENT_ID) return { id, context: undefined }

  return null
}
