import { esc } from './config'
import { listCommands } from './commands'

// ── Commands reference modal ──────────────────────────────────────────────────
// Shows every registered command with its phrases, mode, and description.
// Opened by the 'commands-active' voice command; dismissed with Esc or backdrop.

export function injectCommandsModal() {
  const overlay = document.createElement('div')
  overlay.id = 'pa-cmds-overlay'
  overlay.innerHTML = `
    <div id="pa-cmds-modal">
      <div id="pa-cmds-header">
        <span>Commands</span>
        <button id="pa-cmds-close" title="Close (Esc)">×</button>
      </div>
      <div id="pa-cmds-body"></div>
    </div>
  `
  document.body.appendChild(overlay)
  overlay.addEventListener('click', e => { if (e.target === overlay) closeCommandsModal() })
  document.getElementById('pa-cmds-close')!.addEventListener('click', closeCommandsModal)
  document.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Escape' && overlay.classList.contains('open')) closeCommandsModal()
  })
  injectCommandsStyles()
}

function injectCommandsStyles() {
  const s = document.createElement('style')
  s.textContent = `
    #pa-cmds-overlay{position:fixed;inset:0;background:rgba(0,0,0,.55);z-index:10010;display:none;align-items:center;justify-content:center}
    #pa-cmds-overlay.open{display:flex}
    #pa-cmds-modal{background:var(--pa-surf,#1e293b);border:1px solid var(--pa-border,#334155);border-radius:10px;width:min(92vw,680px);max-height:80vh;display:flex;flex-direction:column;overflow:hidden}
    #pa-cmds-header{display:flex;align-items:center;justify-content:space-between;padding:.75rem 1rem;border-bottom:1px solid var(--pa-border,#334155);font-size:.8rem;font-weight:600;color:var(--pa-muted,#94a3b8);letter-spacing:.06em;text-transform:uppercase}
    #pa-cmds-close{background:none;border:none;cursor:pointer;color:var(--pa-dim,#64748b);font-size:1.2rem;line-height:1;padding:.1rem .3rem}
    #pa-cmds-close:hover{color:var(--pa-body,#e2e8f0)}
    #pa-cmds-body{overflow-y:auto;padding:.5rem 0}
    .pa-cmd-row{display:grid;grid-template-columns:1fr 1.4fr auto;gap:.5rem 1rem;align-items:start;padding:.55rem 1rem;border-bottom:1px solid color-mix(in srgb,var(--pa-border,#334155) 40%,transparent)}
    .pa-cmd-row:last-child{border-bottom:none}
    .pa-cmd-phrases{display:flex;flex-direction:column;gap:.2rem}
    .pa-cmd-phrase{font-family:var(--pa-mono,monospace);font-size:.74rem;color:var(--pa-accent,#14b8a6);background:rgba(20,184,166,.08);border:1px solid rgba(20,184,166,.2);border-radius:3px;padding:.1rem .35rem;white-space:nowrap;width:fit-content}
    .pa-cmd-desc{font-size:.78rem;color:var(--pa-muted,#94a3b8);line-height:1.4;padding-top:.05rem}
    .pa-cmd-mode{font-size:.65rem;font-family:var(--pa-mono,monospace);color:var(--pa-dim,#64748b);white-space:nowrap;padding-top:.15rem}
  `
  document.head.appendChild(s)
}

export function openCommandsModal() {
  const overlay = document.getElementById('pa-cmds-overlay')
  const body    = document.getElementById('pa-cmds-body')
  if (!overlay || !body) return
  const cmds = listCommands().slice().sort((a, b) => a.priority - b.priority)
  body.innerHTML = cmds.map(cmd => `
    <div class="pa-cmd-row">
      <div class="pa-cmd-phrases">${cmd.phrases.map(p => `<span class="pa-cmd-phrase">${esc(p)}</span>`).join('')}</div>
      <div class="pa-cmd-desc">${esc(cmd.description)}</div>
      <div class="pa-cmd-mode">${esc(cmd.matchMode)}</div>
    </div>`).join('')
  overlay.classList.add('open')
}

export function closeCommandsModal() {
  document.getElementById('pa-cmds-overlay')?.classList.remove('open')
}
