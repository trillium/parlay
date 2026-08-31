import { readFileSync } from "fs"
import { join } from "path"
import { CORS } from "./sse"

// Serve parlay-ui.js — include in any Pulse page via:
//   <script src="/api/chat/parlay-ui.js"></script>
// or, same-origin against this server, its documented top-level path
// (docs/api-contract.md): <script src="/parlay-ui.js"></script>.
// Provides: __paPageId, __paRegisterInput, syntax highlight via data-lang.

const UI_JS = (() => {
  try { return readFileSync(join(import.meta.dir, "parlay-ui.js"), "utf8") }
  catch { return "// parlay-ui.js missing from server bundle" }
})()

export function handleParlayUiRequest(req: Request, pathname: string): Response | null {
  if (pathname !== "/api/chat/parlay-ui.js" && pathname !== "/parlay-ui.js") return null
  return new Response(UI_JS, {
    headers: {
      "Content-Type": "application/javascript; charset=utf-8",
      "Cache-Control": "no-cache",
      ...CORS,
    },
  })
}
