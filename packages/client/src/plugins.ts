import { CHAT_BASE } from './config'
import { PA_VERSION } from './version'
import { buildContext } from './commands/ctx'
import { onSse } from './sse'
import { getDeviceId } from './sse'

// ── Plugin loader (client side) ──────────────────────────────────────────────
// Plugins are IIFE files served from /annotate/plugins/<id>.js, listed by
// GET /api/chat/plugins. Each calls window.__parlay.registerPlugin({id,
// version, minPanel, setup(api)}); the api is the sandboxed CommandContext
// plus SSE subscriptions (reconnect-safe), device id, and namespaced UI
// injection. See COMMANDS.md § Plugins.

interface PluginDef {
  id: string
  version: string
  minPanel?: string
  setup(api: any): void
}

function semverLt(a: string, b: string): boolean {
  const pa = a.split('.').map(Number), pb = b.split('.').map(Number)
  for (let i = 0; i < 3; i++) {
    if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) < (pb[i] ?? 0)
  }
  return false
}

function pluginApi(id: string) {
  return {
    ...buildContext(),
    sse: { on: onSse },
    device: getDeviceId,
    ui: {
      injectStyle(css: string) {
        const el = document.createElement('style')
        el.dataset.plugin = id
        el.textContent = css
        document.head.appendChild(el)
      },
    },
  }
}

export function initPlugins() {
  const pub = ((window as any).__parlay ??= {})
  pub.registerPlugin = (def: PluginDef) => {
    if (def.minPanel && semverLt(PA_VERSION, def.minPanel)) {
      console.warn(`[parlay] plugin ${def.id} needs panel ≥${def.minPanel} (running ${PA_VERSION}) — skipped`)
      return
    }
    try { def.setup(pluginApi(def.id)) } catch (e) { console.error(`[parlay] plugin ${def.id} setup failed`, e) }
  }
  // Load manifests + inject plugin scripts (deterministic order, per-plugin cache-bust)
  fetch(`${CHAT_BASE}/plugins`, { signal: AbortSignal.timeout(3_000) })
    .then(r => r.json())
    .then((manifests: Array<{ id: string; version: string }>) => {
      for (const m of manifests) {
        if (!/^[a-z0-9-]+$/.test(m.id)) continue
        const s = document.createElement('script')
        s.src = `/annotate/plugins/${m.id}.js?v=${encodeURIComponent(m.version)}`
        document.head.appendChild(s)
      }
    })
    .catch(() => { /* Pulse down — plugins just don't load this session */ })
}
