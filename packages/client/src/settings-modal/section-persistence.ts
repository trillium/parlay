const SECTION_STATE_KEY = 'pa-settings-sections'

function readSectionState(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(SECTION_STATE_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

export function wireSectionPersistence(root: HTMLElement) {
  const saved = readSectionState()
  const groups = [...root.querySelectorAll('details.pa-settings-group[data-section]')] as HTMLDetailsElement[]
  for (const group of groups) {
    const key = group.dataset.section!
    if (key in saved) group.open = saved[key]
    group.addEventListener('toggle', () => {
      const state = readSectionState()
      state[key] = group.open
      try {
        localStorage.setItem(SECTION_STATE_KEY, JSON.stringify(state))
      } catch {}
    })
  }
}
