import { CHAT_BASE } from './config'
import { inputEl } from './dom'

// ── Pending image attachments (#17 addendum) ─────────────────────────────────
// Paste an image (or use 📎) → uploaded immediately → pending thumbnail chip
// above the input → the NEXT send carries images[] (server also appends the
// URLs to the text for the agent poll/monitor contract — see server uploads.ts
// for the canonical URL→filesystem mapping agents use to Read the files).

const pending: string[] = []

export function takePendingImages(): string[] {
  const urls = [...pending]
  pending.length = 0
  renderChips()
  return urls
}

function stripEl(): HTMLElement | null { return document.getElementById('pa-attach-strip') }

function renderChips() {
  const strip = stripEl()
  if (!strip) return
  strip.classList.toggle('visible', pending.length > 0)
  strip.innerHTML = pending.map((u, i) =>
    `<span class="pa-chip"><img src="${u.replace(/"/g, '%22')}" alt=""><button class="pa-chip-x" data-i="${i}" title="Remove">✕</button></span>`
  ).join('')
  strip.querySelectorAll('.pa-chip-x').forEach(btn => btn.addEventListener('click', () => {
    pending.splice(Number((btn as HTMLElement).dataset.i), 1)
    renderChips()
  }))
}

async function uploadFile(file: File): Promise<string | null> {
  try {
    const form = new FormData()
    form.append('file', file)
    const r = await fetch(`${CHAT_BASE}/upload`, { method: 'POST', body: form })
    const res = await r.json()
    return res.ok && res.url ? res.url : null
  } catch { return null }
}

async function addFiles(files: File[]) {
  const attachBtn = document.getElementById('pa-attach')
  if (attachBtn) attachBtn.textContent = '⏳'
  for (const f of files.slice(0, 8 - pending.length)) {
    const url = await uploadFile(f)
    if (url) { pending.push(url); renderChips() }
    else inputEl.placeholder = 'Upload failed — images only, 10MB max'
  }
  if (attachBtn) attachBtn.textContent = '📎'
}

export function wireAttachments() {
  // 📎 button → file picker (camera-capable on mobile) → pending chips
  const attachBtn = document.getElementById('pa-attach')
  const attachFile = document.getElementById('pa-attach-file') as HTMLInputElement | null
  if (attachBtn && attachFile) {
    attachBtn.addEventListener('click', () => attachFile.click())
    attachFile.addEventListener('change', () => {
      const files = [...(attachFile.files ?? [])]
      attachFile.value = ''
      if (files.length) void addFiles(files)
    })
  }
  // Paste an image anywhere in the input → same upload path, same chips
  inputEl.addEventListener('paste', (e: ClipboardEvent) => {
    const items = [...(e.clipboardData?.items ?? [])]
    const files = items.filter(i => i.type.startsWith('image/'))
      .map(i => i.getAsFile()).filter((f): f is File => !!f)
    if (!files.length) return
    e.preventDefault()   // image paste is an attach, not text
    void addFiles(files)
  })
}
