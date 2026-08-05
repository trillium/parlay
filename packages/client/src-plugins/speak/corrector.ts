// ── Speak plugin: pronunciation corrector UI (#19) ───────────────────────────
// When speak says something odd, the 🚩 expands the last-spoken sentence into
// an inline corrector: a simple substitution fix (persisted server-side,
// effective immediately) or an escalation to the active agent.

const ui = () => (window as any).__parlay.speechUi

export function injectCorrectorStyles(api: any) {
  api.ui.injectStyle(`
    .pa-corrector { margin: 6px 0 4px; padding: 9px 11px; border: 1px solid color-mix(in srgb, var(--pa-amber) 40%, var(--pa-border)); border-radius: 8px; background: color-mix(in srgb, var(--pa-amber) 6%, var(--pa-surf2)); font-family: var(--pa-mono); font-size: 11px; }
    .pa-corr-quote { color: var(--pa-muted); margin-bottom: 7px; font-style: italic; }
    .pa-corrector input { width: 100%; box-sizing: border-box; margin-bottom: 5px; padding: 6px 8px; border-radius: 6px; border: 1px solid var(--pa-border); background: var(--pa-ink); color: var(--pa-body); font-family: var(--pa-mono); font-size: 11px; }
    .pa-corr-btns { display: flex; gap: 6px; margin-top: 2px; }
    .pa-corr-btns button { padding: 5px 10px; border-radius: 6px; cursor: pointer; font-family: var(--pa-mono); font-size: 10.5px; border: 1px solid var(--pa-border); background: none; color: var(--pa-body); }
    .pa-corr-save { border-color: color-mix(in srgb, var(--pa-green) 45%, transparent) !important; color: var(--pa-green) !important; }
    .pa-corr-ask { border-color: color-mix(in srgb, var(--pa-blue) 45%, transparent) !important; color: var(--pa-blue) !important; }
    .pa-corr-split { border-color: color-mix(in srgb, var(--pa-amber) 45%, transparent) !important; color: var(--pa-amber) !important; }
  `)
}

export function openCorrector(api: any) {
  const last = ui().getLastSpoken()
  if (!last) return
  document.querySelectorAll('.pa-corrector').forEach(e => e.remove())

  // Anchor under the message paragraph, else under the bubble. task-1h47: passages
  // are inline `.pa-sb` spans in ONE `.pa-para` block now, so there is no per-block
  // element to anchor to — attach the corrector below the whole paragraph.
  const anchor = (last.msgId
    ? document.querySelector(`[data-pa-id="${last.msgId}"] .pa-para`)
    : null) ?? (last.msgId ? document.querySelector(`[data-pa-id="${last.msgId}"] .pa-bubble`) : null)
  if (!anchor) return

  const box = document.createElement('div')
  box.className = 'pa-corrector'
  box.innerHTML = `
    <div class="pa-corr-quote">“${last.sentence.slice(0, 140).replace(/</g, '&lt;')}”</div>
    <input class="pa-corr-from" placeholder="word/phrase it said wrong">
    <input class="pa-corr-to" placeholder="respell how it should sound (e.g. par lay)">
    <div class="pa-corr-btns">
      <button class="pa-corr-save">Save fix</button>
      <button class="pa-corr-ask">Ask agent why</button>
      <button class="pa-corr-split">Bad split</button>
      <button class="pa-corr-x">✕</button>
    </div>`
  anchor.after(box)
  const from = box.querySelector('.pa-corr-from') as HTMLInputElement
  const to = box.querySelector('.pa-corr-to') as HTMLInputElement

  box.querySelector('.pa-corr-save')!.addEventListener('click', async () => {
    if (!from.value.trim() || !to.value.trim()) { from.focus(); return }
    try {
      const r = await fetch('/api/chat/tts-correction', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from: from.value.trim(), to: to.value.trim(), sentence: last.sentence }),
      })
      const res = await r.json()
      box.querySelector('.pa-corr-quote')!.textContent = res.ok ? '✓ saved — takes effect on the next synthesis' : `failed: ${res.error}`
      if (res.ok) setTimeout(() => box.remove(), 1600)
    } catch { box.querySelector('.pa-corr-quote')!.textContent = 'failed — network' }
  })
  box.querySelector('.pa-corr-ask')!.addEventListener('click', () => {
    api.input.submit(`The speak system pronounced this oddly: "${last.sentence}" — why might the TTS get it wrong, and what substitution (word → phonetic respelling) should we save?`)
    box.remove()
  })
  // Segmentation complaints are a different problem class than pronunciation:
  // the SPLIT was wrong, not the speech. Reported with the neighboring blocks
  // so the splitter can be tuned against real cases.
  box.querySelector('.pa-corr-split')!.addEventListener('click', async () => {
    const blocks = last.msgId
      ? [...document.querySelectorAll(`[data-pa-id="${last.msgId}"] .pa-sb`)].map(s => s.textContent ?? '')
      : []
    const i = last.blockIdx ?? blocks.findIndex(b => b.includes(last.sentence.slice(0, 40)))
    try {
      await fetch('/api/chat/tts-report', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sentence: last.sentence,
          clipMeta: { kind: 'segmentation', blockIdx: i, prevBlock: blocks[i - 1] ?? null, nextBlock: blocks[i + 1] ?? null },
        }),
      })
      box.querySelector('.pa-corr-quote')!.textContent = '✓ split reported — the block boundaries around this phrase are logged for tuning'
      setTimeout(() => box.remove(), 1800)
    } catch { box.querySelector('.pa-corr-quote')!.textContent = 'failed — network' }
  })
  box.querySelector('.pa-corr-x')!.addEventListener('click', () => box.remove())
}
