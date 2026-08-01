// ── Mobile on-screen console ─────────────────────────────────────────────────
// Fallback for when the remote debug log (./debug-log.ts) isn't enough and the
// captain needs to see live console output on the phone itself (e.g. to
// screenshot it). Loads eruda from CDN on demand — zero bundle cost when
// unused, default off.
//
// Enable via ?paConsole=1 in the URL (sticky — persisted to localStorage), or
// a ~600ms long-press on the drawer trigger button.

const ERUDA_VERSION = '3.4.3'
const ERUDA_CDN = `https://cdn.jsdelivr.net/npm/eruda@${ERUDA_VERSION}/eruda.min.js`
const ERUDA_SRI = 'sha384-F7xQBvh3l6dG/mMD6QPIeVmXtzWT4Ce3ZDu8ysPuzMWMx9bFOIMGnRPUhLuQipss'
const STORAGE_KEY = 'pa-console-enabled'
const LONG_PRESS_MS = 600

let loadPromise: Promise<void> | null = null

function loadEruda(): Promise<void> {
  if (loadPromise) return loadPromise
  loadPromise = new Promise((resolve, reject) => {
    const script = document.createElement('script')
    script.src = ERUDA_CDN
    script.integrity = ERUDA_SRI
    script.crossOrigin = 'anonymous'
    script.onload = () => { (window as any).eruda?.init(); resolve() }
    script.onerror = () => { loadPromise = null; reject(new Error('failed to load eruda')) }
    document.head.appendChild(script)
  })
  return loadPromise
}

export function toggleMobileConsole(): void {
  const eruda = (window as any).eruda
  if (eruda) {
    eruda.show()
    return
  }
  loadEruda().catch(() => {})
}

export function initMobileConsole(longPressTarget: HTMLElement | null): void {
  try {
    const params = new URLSearchParams(location.search)
    if (params.get('paConsole') === '1') {
      localStorage.setItem(STORAGE_KEY, '1')
    }
    if (localStorage.getItem(STORAGE_KEY) === '1') {
      loadEruda().catch(() => {})
    }
  } catch {}

  if (!longPressTarget) return
  let pressTimer: ReturnType<typeof setTimeout> | null = null
  const start = () => {
    pressTimer = setTimeout(() => {
      pressTimer = null
      toggleMobileConsole()
      try { localStorage.setItem(STORAGE_KEY, '1') } catch {}
    }, LONG_PRESS_MS)
  }
  const cancel = () => { if (pressTimer) { clearTimeout(pressTimer); pressTimer = null } }
  longPressTarget.addEventListener('touchstart', start, { passive: true })
  longPressTarget.addEventListener('touchend', cancel)
  longPressTarget.addEventListener('touchcancel', cancel)
  longPressTarget.addEventListener('touchmove', cancel)
  longPressTarget.addEventListener('mousedown', start)
  longPressTarget.addEventListener('mouseup', cancel)
  longPressTarget.addEventListener('mouseleave', cancel)
}
