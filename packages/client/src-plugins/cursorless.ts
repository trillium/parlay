// ── Cursorless plugin (client half) ──────────────────────────────────────────
// Answers the cursorless-everywhere 4-op contract against the Parlay input:
// Talon → POST /api/chat/plugin/cursorless/rpc → SSE cursorless_rpc → here →
// POST .../response. Edits go through api.input.setText, which deliberately
// skips the command pass so a Cursorless edit ending in a submit word can
// never auto-send.

;(window as any).__parlay?.registerPlugin?.({
  id: 'cursorless',
  version: '0.1.0',
  minPanel: '3.6.0',
  setup(api: any) {
    api.ui.injectStyle('.pa-cursorless-flash { outline: 2px solid var(--pa-amber); border-radius: 4px; }')

    const respond = (rpcId: string, result: unknown) =>
      fetch('/api/chat/plugin/cursorless/response', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ rpcId, result }),
      }).catch(() => {})

    api.sse.on('cursorless_rpc', (msg: { rpcId: string; op: string; args: any }) => {
      // Single-responder rule: every Pulse page loads this plugin and shares
      // the device id — only the chat-app shell (or whichever page actually
      // has focus) may answer, or duplicate edits mangle each other via
      // draft-sync cross-echo.
      if (!location.pathname.startsWith('/chat-app') && !document.hasFocus()) return
      const { rpcId, op, args } = msg
      try {
        if (op === 'getEditorState') {
          const sel = api.input.selection()
          respond(rpcId, { text: api.input.value(), selections: [sel] })
        } else if (op === 'setSelections') {
          const s = (args?.selections ?? [])[0]
          if (s) api.input.setSelection(s.anchor ?? 0, s.active ?? 0)
          respond(rpcId, { ok: true })
        } else if (op === 'editText') {
          api.input.setText(String(args?.text ?? api.input.value()))
          const s = (args?.selections ?? [])[0]
          if (s) api.input.setSelection(s.anchor ?? 0, s.active ?? 0)
          respond(rpcId, { ok: true })
        } else if (op === 'flashRanges') {
          const input = document.getElementById('pa-input')
          input?.classList.add('pa-cursorless-flash')
          setTimeout(() => input?.classList.remove('pa-cursorless-flash'), 180)
          respond(rpcId, { ok: true })
        } else {
          respond(rpcId, { error: `unknown op: ${op}` })
        }
      } catch (e) { respond(rpcId, { error: String(e) }) }
    })
  },
})
