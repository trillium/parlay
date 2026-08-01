import { esc } from './config'
import { navigateWorkspace } from './commands/ctx'
import { onSse } from './sse'
import { getSettings } from './settings-modal'

// ── Fuzzy page-nav picker ─────────────────────────────────────────────────────
// A command-palette-style modal (mirrors the settings modal's overlay) that
// fuzzy-searches the local Pulse pages (GET /api/chat/pages) and navigates the
// workspace to /<tag>/ on select. Keyboard-first: type to filter, ↑↓ to move,
// ↵ to open, esc to close.

interface PageEntry { tag: string; title: string }

let pages: PageEntry[] = []
let filtered: PageEntry[] = []
let selIdx = 0
let loadedAt = 0
const PAGES_TTL = 30_000

export function injectPageNav() {
  const overlay = document.createElement('div')
  overlay.id = 'pa-nav-overlay'
  overlay.innerHTML = `
    <div id="pa-nav-modal">
      <input type="text" id="pa-nav-search" placeholder="Jump to page…" autocomplete="off" spellcheck="false">
      <div id="pa-nav-list"></div>
      <div class="pa-nav-hint">↑↓ move · ↵ open · esc close</div>
    </div>
  `
  document.body.appendChild(overlay)

  const search = document.getElementById('pa-nav-search') as HTMLInputElement
  overlay.addEventListener('click', e => { if (e.target === overlay) closePageNav() })
  search.addEventListener('input', () => applyFilter(search.value))
  search.addEventListener('keydown', onKey)
}

export async function openPageNav() {
  const overlay = document.getElementById('pa-nav-overlay')
  const search = document.getElementById('pa-nav-search') as HTMLInputElement | null
  if (!overlay || !search) return
  overlay.classList.add('open')
  search.value = ''
  if (Date.now() - loadedAt > PAGES_TTL) await loadPages()
  applyFilter('')
  if (!getSettings().noKeyboardMode) setTimeout(() => search.focus(), 30)
}

export function closePageNav() {
  document.getElementById('pa-nav-overlay')?.classList.remove('open')
}

async function loadPages() {
  try {
    const r = await fetch('/api/chat/pages')
    const j = await r.json()
    pages = Array.isArray(j.pages) ? j.pages : []
    loadedAt = Date.now()
  } catch {
    pages = []
  }
}

// Fuzzy subsequence score: every query char must appear in order. Rewards
// contiguous runs, word-boundary starts, and earlier matches. -1 = no match.
function score(q: string, text: string): number {
  if (!q) return 0
  const t = text.toLowerCase()
  let ti = 0, s = 0, streak = 0
  for (const ch of q.toLowerCase()) {
    const idx = t.indexOf(ch, ti)
    if (idx < 0) return -1
    streak = idx === ti ? streak + 1 : 0
    s += 10 - Math.min(idx - ti, 8) + streak * 3
    if (idx === 0 || /[^a-z0-9]/.test(t[idx - 1])) s += 6
    ti = idx + 1
  }
  return s
}

function applyFilter(q: string) {
  const query = q.trim()
  if (!query) {
    filtered = pages.slice()
  } else {
    filtered = pages
      .map(p => ({ p, sc: Math.max(score(query, p.tag), score(query, p.title)) }))
      .filter(x => x.sc >= 0)
      .sort((a, b) => b.sc - a.sc)
      .map(x => x.p)
  }
  selIdx = 0
  render()
}

function render() {
  const list = document.getElementById('pa-nav-list')
  if (!list) return
  list.innerHTML = ''
  if (!filtered.length) {
    list.innerHTML = '<div class="pa-nav-empty">No pages match</div>'
    return
  }
  filtered.forEach((p, i) => {
    const row = document.createElement('button')
    row.className = 'pa-nav-row' + (i === selIdx ? ' sel' : '')
    row.innerHTML = `<span class="pa-nav-tag">/${esc(p.tag)}/</span><span class="pa-nav-title">${esc(p.title)}</span>`
    row.addEventListener('click', () => go(p))
    row.addEventListener('mousemove', () => { if (selIdx !== i) { selIdx = i; render() } })
    list.appendChild(row)
  })
  const sel = list.querySelector('.pa-nav-row.sel') as HTMLElement | null
  sel?.scrollIntoView({ block: 'nearest' })
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown') { e.preventDefault(); selIdx = Math.min(selIdx + 1, filtered.length - 1); render() }
  else if (e.key === 'ArrowUp') { e.preventDefault(); selIdx = Math.max(selIdx - 1, 0); render() }
  else if (e.key === 'Enter') { e.preventDefault(); if (filtered[selIdx]) go(filtered[selIdx]) }
  else if (e.key === 'Escape') { e.preventDefault(); closePageNav() }
}

function go(p: PageEntry) {
  closePageNav()
  navigateWorkspace(`/${p.tag}/`)
}

// Server-pushed page list updates — no polling needed. fs.watch on ~/pulse-pages/
// fires when a directory is added/removed; the server diffs and broadcasts the delta.
onSse('pages_patch', (patch: { added?: PageEntry[]; removed?: string[] }) => {
  if (patch.removed?.length) {
    const gone = new Set(patch.removed)
    pages = pages.filter(p => !gone.has(p.tag))
  }
  if (patch.added?.length) {
    const have = new Set(pages.map(p => p.tag))
    for (const p of patch.added) if (!have.has(p.tag)) pages.push(p)
    pages.sort((a, b) => a.tag.localeCompare(b.tag))
    loadedAt = Date.now()   // treat the patch as a fresh load; suppress the next TTL fetch
  }
  // Re-render live if the picker is open
  const overlay = document.getElementById('pa-nav-overlay')
  if (overlay?.classList.contains('open')) {
    const search = document.getElementById('pa-nav-search') as HTMLInputElement | null
    applyFilter(search?.value ?? '')
  }
})
