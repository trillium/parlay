// Shapes exchanged with the two upstreams, and the next_step text the bridge
// hands back to the agent.

export interface NativeResult {
  status: string
  dom_snapshot?: string
  layout_warnings?: unknown[]
  session_ended?: boolean
  ended_by?: string
  prompts?: Array<{ tag?: string; text?: string; scenePath?: string; previewPath?: string }>
}

export interface ParlayMsg {
  timeout?: boolean
  id?: string
  role?: string
  text?: string
}

export type Prompt = NonNullable<NativeResult["prompts"]>[number]

/**
 * If 4387 is unreachable, resolving to a never-settling promise drops it from
 * the race without aborting Parlay or creating a tight restart loop.
 *
 * Note the consequence for anything that races against this: a dropped leg
 * never settles, so it cannot be the thing that breaks an await. The deadline
 * in index.ts is a separate leg of the race for exactly that reason.
 */
export function drop<T>(): Promise<T> {
  return new Promise<T>(() => {})
}

export function nextStep(file: string, ended: boolean, prompts: Prompt[] = []): string {
  if (ended) {
    return `The session has ended. Stop polling ${file} — deliver remaining updates in this conversation. Run \`lavish ${file} --reopen\` only if the user explicitly asks for further visual review.`
  }
  const hasWhiteboard = prompts.some(p => p.tag === "whiteboard")
  const whiteboardNote = hasWhiteboard
    ? `This feedback includes whiteboard edits (tag "whiteboard"): read the edit summary in the prompt text first; only open scenePath (.excalidraw JSON) or previewPath (PNG) if the summary isn't enough. Apply edits by updating the Mermaid source in ${file} — Lavish live-reloads it. Never write back to the .excalidraw scene file. `
    : ""
  return `${whiteboardNote}Apply the requested changes to ${file}. Now run \`lavish poll ${file} --agent-reply "<your reply>"\` to send your reply and wait for the next message. Re-running is always safe — queued feedback is never lost.`
}
