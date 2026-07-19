// ── Accessibility Panel ────────────────────────────────────────────────────────
// Text size scaling for both the Parlay drawer and the underlying webpage.
// Independent sliders for drawer content and page content.

export interface A11ySettings {
  drawerScale: number
  pageScale: number
}

const STORAGE_KEY = 'pa-a11y-settings'
const DEFAULT_SETTINGS: A11ySettings = { drawerScale: 100, pageScale: 100 }
const SCALE_STEPS = [100, 125, 150, 175, 200]

let currentSettings: A11ySettings = DEFAULT_SETTINGS
let panelEl: HTMLElement | null = null

function loadSettings(): A11ySettings {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    return stored ? JSON.parse(stored) : DEFAULT_SETTINGS
  } catch {
    return DEFAULT_SETTINGS
  }
}

function saveSettings(settings: A11ySettings) {
  currentSettings = settings
  localStorage.setItem(STORAGE_KEY, JSON.stringify(settings))
  applySettings(settings)
}

function applySettings(settings: A11ySettings) {
  const drawerScale = (settings.drawerScale / 100).toFixed(2)
  const pageScale = (settings.pageScale / 100).toFixed(2)
  document.documentElement.style.setProperty('--pa-drawer-scale', drawerScale)
  document.documentElement.style.setProperty('--pa-page-scale', pageScale)

  // Inject global CSS rules for scaling if not already present
  let styleEl = document.getElementById('pa-a11y-styles')
  if (!styleEl) {
    styleEl = document.createElement('style')
    styleEl.id = 'pa-a11y-styles'
    styleEl.textContent = `
      /* Drawer text scaling */
      #pa-drawer, .pa-drawer { font-size: calc(1em * var(--pa-drawer-scale, 1)); }
      #pa-drawer button, .pa-drawer button { font-size: calc(1em * var(--pa-drawer-scale, 1)); }
      #pa-drawer textarea { font-size: calc(1em * var(--pa-drawer-scale, 1)); }
      #pa-drawer select { font-size: calc(1em * var(--pa-drawer-scale, 1)); }
      #pa-drawer input { font-size: calc(1em * var(--pa-drawer-scale, 1)); }
      /* Page content scaling */
      body { --pa-page-scale: var(--pa-page-scale, 1); }
      body * { --pa-scale-applied: var(--pa-page-scale, 1); }
      p, div, span, h1, h2, h3, h4, h5, h6 { font-size: calc(1em * var(--pa-page-scale, 1)); }
      li, dd, dt { font-size: calc(1em * var(--pa-page-scale, 1)); }
      button, input, select, textarea { font-size: calc(1em * var(--pa-page-scale, 1)); }
    `
    document.head.appendChild(styleEl)
  }
}

export function initA11yPanel() {
  currentSettings = loadSettings()
  applySettings(currentSettings)
}

export function openA11yPanel() {
  openPanel()
}

function openPanel() {
  if (panelEl) return

  panelEl = document.createElement('div')
  panelEl.id = 'pa-a11y-panel'
  panelEl.style.cssText = `
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    background: var(--background, white);
    border: 1px solid var(--border, #ddd);
    border-radius: 8px;
    padding: 24px;
    z-index: 2147483647;
    max-width: 400px;
    width: 90%;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
    font-family: system-ui, sans-serif;
  `

  const closeBtn = document.createElement('button')
  closeBtn.textContent = '✕'
  closeBtn.style.cssText = `
    position: absolute;
    top: 12px;
    right: 12px;
    width: 32px;
    height: 32px;
    border: none;
    background: transparent;
    font-size: 20px;
    cursor: pointer;
    color: var(--foreground, #000);
  `
  closeBtn.addEventListener('click', closePanel)
  panelEl.appendChild(closeBtn)

  const title = document.createElement('h2')
  title.textContent = 'Accessibility Settings'
  title.style.cssText = 'margin: 0 0 20px 0; font-size: 18px; color: var(--foreground, #000);'
  panelEl.appendChild(title)

  // Drawer scale slider
  const drawerSection = createSliderSection(
    'Parlay Drawer Text Size',
    currentSettings.drawerScale,
    (val) => saveSettings({ ...currentSettings, drawerScale: val })
  )
  panelEl.appendChild(drawerSection)

  // Page scale slider
  const pageSection = createSliderSection(
    'Webpage Text Size',
    currentSettings.pageScale,
    (val) => saveSettings({ ...currentSettings, pageScale: val })
  )
  panelEl.appendChild(pageSection)

  // Reset button
  const resetBtn = document.createElement('button')
  resetBtn.textContent = 'Reset to Defaults'
  resetBtn.style.cssText = `
    margin-top: 20px;
    padding: 8px 16px;
    background: var(--muted, #f0f0f0);
    border: 1px solid var(--border, #ddd);
    border-radius: 4px;
    cursor: pointer;
    color: var(--foreground, #000);
    font-size: 14px;
  `
  resetBtn.addEventListener('click', () => {
    saveSettings(DEFAULT_SETTINGS)
    closePanel()
    openPanel() // Reopen to show reset values
  })
  panelEl.appendChild(resetBtn)

  document.body.appendChild(panelEl)

  // Close on backdrop click
  document.addEventListener('click', (e) => {
    if (e.target === panelEl) closePanel()
  })
}

function closePanel() {
  if (panelEl) {
    panelEl.remove()
    panelEl = null
  }
}

function createSliderSection(label: string, value: number, onChange: (val: number) => void): HTMLElement {
  const section = document.createElement('div')
  section.style.cssText = 'margin-bottom: 20px;'

  const labelEl = document.createElement('label')
  labelEl.style.cssText = 'display: block; margin-bottom: 8px; font-size: 14px; color: var(--foreground, #000); font-weight: 500;'
  labelEl.textContent = label

  const sliderContainer = document.createElement('div')
  sliderContainer.style.cssText = 'display: flex; align-items: center; gap: 12px;'

  const slider = document.createElement('input')
  slider.type = 'range'
  slider.min = String(SCALE_STEPS[0])
  slider.max = String(SCALE_STEPS[SCALE_STEPS.length - 1])
  slider.step = String(SCALE_STEPS[1] - SCALE_STEPS[0])
  slider.value = String(value)
  slider.style.cssText = 'flex: 1; cursor: pointer;'

  const valueDisplay = document.createElement('span')
  valueDisplay.style.cssText = 'min-width: 45px; text-align: right; font-size: 14px; color: var(--foreground, #000); font-weight: 500;'
  valueDisplay.textContent = `${value}%`

  slider.addEventListener('input', () => {
    const newValue = Number(slider.value)
    valueDisplay.textContent = `${newValue}%`
    onChange(newValue)
  })

  sliderContainer.appendChild(slider)
  sliderContainer.appendChild(valueDisplay)

  section.appendChild(labelEl)
  section.appendChild(sliderContainer)
  return section
}
