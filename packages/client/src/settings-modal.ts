import { CHAT_BASE } from './config'

// ── Types ─────────────────────────────────────────────────────────────────────

export interface ParlaySettings {
  panelSide:          'left' | 'right'
  triggerSide:        'left' | 'right'
  enabledProjects:    'all' | string[]
  voiceEnabled:       boolean
  voiceSubmitPhrases: string[]
  voiceClearPhrase:   string
}

const DEFAULTS: ParlaySettings = {
  panelSide:          'left',
  triggerSide:        'right',
  enabledProjects:    'all',
  voiceEnabled:       true,
  voiceSubmitPhrases: ['bravely', 'gravely', 'briefly', 'lap'],
  voiceClearPhrase:   'change inside in input',
}

let _settings: ParlaySettings = { ...DEFAULTS }

export function getSettings(): ParlaySettings { return _settings }

// ── Server I/O ────────────────────────────────────────────────────────────────

export async function loadSettings(): Promise<ParlaySettings> {
  try {
    const r = await fetch(`${CHAT_BASE}/parlay/settings`, { signal: AbortSignal.timeout(3_000) })
    if (r.ok) _settings = { ...DEFAULTS, ...(await r.json()) }
  } catch { /* server not ready — use defaults */ }
  return _settings
}

async function saveSettings(s: ParlaySettings): Promise<void> {
  _settings = s
  try {
    await fetch(`${CHAT_BASE}/parlay/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(s),
    })
  } catch {}
}

// ── CSS override application ──────────────────────────────────────────────────

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

  // Sync body margin immediately if the panel is already open (desktop mode).
  // Without this the old margin slot stays reserved and the drawer overlays content.
  const marginEl = document.getElementById('pa-layout-override') as HTMLStyleElement | null
  if (marginEl) {
    if (s.panelSide === 'right') {
      marginEl.textContent = 'body { margin-right: 380px !important; margin-left: 0 !important; max-width: calc(100vw - 380px) !important; width: auto !important; box-sizing: border-box !important; }'
    } else {
      marginEl.textContent = 'body { margin-left: 380px !important; margin-right: 0 !important; max-width: calc(100vw - 380px) !important; width: auto !important; box-sizing: border-box !important; }'
    }
  }
}

// ── Project filter ────────────────────────────────────────────────────────────

export function isPageEnabled(settings: ParlaySettings): boolean {
  if (settings.enabledProjects === 'all') return true
  const file = (window as any).__paLavishFile as string | undefined
  if (!file) return true   // non-Lavish page — always activate
  const patterns = settings.enabledProjects as string[]
  return patterns.some(p => file.includes(p))
}

// ── Modal DOM ─────────────────────────────────────────────────────────────────

export function injectSettingsModal() {
  const overlay = document.createElement('div')
  overlay.id = 'pa-settings-overlay'
  overlay.innerHTML = `
    <div id="pa-settings-modal">
      <h2>Parlay Settings</h2>

      <div class="pa-settings-section">
        <div class="pa-settings-label">Panel side</div>
        <div class="pa-settings-row">
          <div class="pa-settings-radio">
            <input type="radio" name="panelSide" id="pa-ps-left" value="left">
            <label for="pa-ps-left">Left</label>
          </div>
          <div class="pa-settings-radio">
            <input type="radio" name="panelSide" id="pa-ps-right" value="right">
            <label for="pa-ps-right">Right</label>
          </div>
        </div>
      </div>

      <div class="pa-settings-section">
        <div class="pa-settings-label">Trigger button side</div>
        <div class="pa-settings-row">
          <div class="pa-settings-radio">
            <input type="radio" name="triggerSide" id="pa-ts-left" value="left">
            <label for="pa-ts-left">Left</label>
          </div>
          <div class="pa-settings-radio">
            <input type="radio" name="triggerSide" id="pa-ts-right" value="right">
            <label for="pa-ts-right">Right</label>
          </div>
        </div>
      </div>

      <div class="pa-settings-section">
        <div class="pa-settings-label">Active projects</div>
        <div id="pa-settings-all-wrap">
          <input type="checkbox" id="pa-settings-all">
          <label for="pa-settings-all">All projects</label>
        </div>
        <textarea
          id="pa-settings-projects"
          placeholder="One filename substring per line&#10;e.g. invoice&#10;e.g. dashboard"
        ></textarea>
        <div class="pa-settings-hint">Parlay only activates when the Lavish file name contains one of these substrings.</div>
      </div>

      <div class="pa-settings-section">
        <div class="pa-settings-label">Voice auto-submit</div>
        <div id="pa-settings-voice-wrap">
          <div class="pa-settings-all-wrap">
            <input type="checkbox" id="pa-settings-voice-enabled">
            <label for="pa-settings-voice-enabled">Enabled</label>
          </div>
          <textarea
            id="pa-settings-submit-phrases"
            placeholder="One phrase per line&#10;e.g. bravely&#10;e.g. gravely"
          ></textarea>
          <div class="pa-settings-hint">When the message ends with one of these words, it auto-sends after 1s.</div>
          <div class="pa-settings-label" style="margin-top:10px">Clear-input phrase</div>
          <input
            type="text"
            id="pa-settings-clear-phrase"
            placeholder="e.g. change inside in input"
          >
          <div class="pa-settings-hint">If the entire input matches this phrase exactly, the field is cleared.</div>
        </div>
      </div>

      <div class="pa-settings-footer">
        <button class="pa-settings-btn" id="pa-settings-cancel">Close</button>
      </div>
    </div>
  `
  document.body.appendChild(overlay)

  // Close on backdrop click, close button, or Escape
  overlay.addEventListener('click', (e) => { if (e.target === overlay) closeSettingsModal() })
  document.getElementById('pa-settings-cancel')!.addEventListener('click', closeSettingsModal)
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeSettingsModal() })

  // Auto-save on every radio change
  overlay.querySelectorAll('input[type="radio"]').forEach(el => {
    el.addEventListener('change', commitSettings)
  })

  // All-projects checkbox: toggle textarea + auto-save
  const allChk = document.getElementById('pa-settings-all') as HTMLInputElement
  const projectsTa = document.getElementById('pa-settings-projects') as HTMLTextAreaElement
  allChk.addEventListener('change', () => {
    projectsTa.disabled = allChk.checked
    commitSettings()
  })

  // Projects textarea: debounced auto-save
  let debounce: ReturnType<typeof setTimeout> | null = null
  projectsTa.addEventListener('input', () => {
    clearTimeout(debounce!)
    debounce = setTimeout(commitSettings, 400)
  })

  // Talon enabled toggle
  const voiceChk = document.getElementById('pa-settings-voice-enabled') as HTMLInputElement
  const submitPhrasesTa = document.getElementById('pa-settings-submit-phrases') as HTMLTextAreaElement
  const clearPhraseIn = document.getElementById('pa-settings-clear-phrase') as HTMLInputElement
  voiceChk.addEventListener('change', () => {
    submitPhrasesTa.disabled = !voiceChk.checked
    clearPhraseIn.disabled  = !voiceChk.checked
    commitSettings()
  })

  // Submit phrases: debounced auto-save
  let phraseDebounce: ReturnType<typeof setTimeout> | null = null
  submitPhrasesTa.addEventListener('input', () => {
    clearTimeout(phraseDebounce!)
    phraseDebounce = setTimeout(commitSettings, 400)
  })

  // Clear phrase: debounced auto-save
  let clearDebounce: ReturnType<typeof setTimeout> | null = null
  clearPhraseIn.addEventListener('input', () => {
    clearTimeout(clearDebounce!)
    clearDebounce = setTimeout(commitSettings, 400)
  })
}

// ── Modal open/close ──────────────────────────────────────────────────────────

export function openSettingsModal() {
  const overlay = document.getElementById('pa-settings-overlay')!
  const s = _settings

  // Populate radio buttons
  const panelLeft  = document.getElementById('pa-ps-left')  as HTMLInputElement
  const panelRight = document.getElementById('pa-ps-right') as HTMLInputElement
  const trigLeft   = document.getElementById('pa-ts-left')  as HTMLInputElement
  const trigRight  = document.getElementById('pa-ts-right') as HTMLInputElement
  if (s.panelSide === 'right') { panelRight.checked = true } else { panelLeft.checked = true }
  if (s.triggerSide === 'left') { trigLeft.checked = true } else { trigRight.checked = true }

  // Populate projects
  const allChk = document.getElementById('pa-settings-all') as HTMLInputElement
  const projectsTa = document.getElementById('pa-settings-projects') as HTMLTextAreaElement
  if (s.enabledProjects === 'all') {
    allChk.checked = true
    projectsTa.disabled = true
    projectsTa.value = ''
  } else {
    allChk.checked = false
    projectsTa.disabled = false
    projectsTa.value = (s.enabledProjects as string[]).join('\n')
  }

  // Populate talon settings
  const voiceChk = document.getElementById('pa-settings-voice-enabled') as HTMLInputElement
  const submitPhrasesTa = document.getElementById('pa-settings-submit-phrases') as HTMLTextAreaElement
  const clearPhraseIn = document.getElementById('pa-settings-clear-phrase') as HTMLInputElement
  voiceChk.checked = s.voiceEnabled
  submitPhrasesTa.value = s.voiceSubmitPhrases.join('\n')
  submitPhrasesTa.disabled = !s.voiceEnabled
  clearPhraseIn.value = s.voiceClearPhrase
  clearPhraseIn.disabled = !s.voiceEnabled

  overlay.classList.add('open')
}

export function closeSettingsModal() {
  document.getElementById('pa-settings-overlay')?.classList.remove('open')
}

async function commitSettings() {
  const panelSide   = (document.querySelector('input[name="panelSide"]:checked')   as HTMLInputElement | null)?.value as 'left' | 'right' ?? 'left'
  const triggerSide = (document.querySelector('input[name="triggerSide"]:checked') as HTMLInputElement | null)?.value as 'left' | 'right' ?? 'right'
  const allChk      = document.getElementById('pa-settings-all')       as HTMLInputElement
  const projectsTa  = document.getElementById('pa-settings-projects')  as HTMLTextAreaElement
  const voiceChk    = document.getElementById('pa-settings-voice-enabled') as HTMLInputElement
  const submitTa    = document.getElementById('pa-settings-submit-phrases') as HTMLTextAreaElement
  const clearIn     = document.getElementById('pa-settings-clear-phrase')   as HTMLInputElement

  const enabledProjects: 'all' | string[] = allChk.checked
    ? 'all'
    : projectsTa.value.split('\n').map(l => l.trim()).filter(Boolean)

  const next: ParlaySettings = {
    panelSide,
    triggerSide,
    enabledProjects,
    voiceEnabled:       voiceChk.checked,
    voiceSubmitPhrases: submitTa.value.split('\n').map(l => l.trim()).filter(Boolean),
    voiceClearPhrase:   clearIn.value.trim(),
  }
  applySettings(next)
  await saveSettings(next)
}
