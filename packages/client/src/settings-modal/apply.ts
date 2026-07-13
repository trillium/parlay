import { type ParlaySettings } from './types'

export function applySettings(s: ParlaySettings) {
  let css = ''

  if (s.panelSide === 'right') {
    css += `
      #pa-drawer {
        left: auto !important; right: 0 !important;
        border-right: none !important;
        border-left: 1px solid var(--pa-border) !important;
        transform: translateX(100%) !important;
        box-shadow: -4px 0 24px rgba(0,0,0,.5) !important;
      }
      #pa-drawer.open { transform: translateX(0) !important; }
    `
  }

  if (s.triggerSide === 'left') {
    css += `
      #pa-trigger { right: auto !important; left: 22px !important; }
      #pa-ann-btn { right: auto !important; left: 22px !important; }
      #pa-settings-btn-gear { right: auto !important; left: 22px !important; }
    `
  }

  let el = document.getElementById('pa-settings-override') as HTMLStyleElement | null
  if (!el) {
    el = document.createElement('style')
    el.id = 'pa-settings-override'
    document.head.appendChild(el)
  }
  el.textContent = css

  // Sync body margin immediately — without this the old margin slot stays reserved
  const marginEl = document.getElementById('pa-layout-override') as HTMLStyleElement | null
  if (marginEl) {
    if (s.panelSide === 'right') {
      marginEl.textContent = 'body { margin-right: 380px !important; margin-left: 0 !important; max-width: calc(100vw - 380px) !important; width: auto !important; box-sizing: border-box !important; }'
    } else {
      marginEl.textContent = 'body { margin-left: 380px !important; margin-right: 0 !important; max-width: calc(100vw - 380px) !important; width: auto !important; box-sizing: border-box !important; }'
    }
  }
}

export function isPageEnabled(settings: ParlaySettings): boolean {
  if (settings.enabledProjects === 'all') return true
  const file = (window as any).__paLavishFile as string | undefined
  if (!file) return true
  const patterns = settings.enabledProjects as string[]
  return patterns.some(p => file.includes(p))
}
