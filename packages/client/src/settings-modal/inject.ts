import { closeSettingsModal, commitSettings } from './lifecycle'

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
        <textarea id="pa-settings-projects"
          placeholder="One filename substring per line&#10;e.g. invoice&#10;e.g. dashboard"
        ></textarea>
        <div class="pa-settings-hint">Parlay only activates when the file name contains one of these substrings.</div>
      </div>

      <div class="pa-settings-section">
        <div class="pa-settings-label">Voice auto-submit</div>
        <div id="pa-settings-voice-wrap">
          <div class="pa-settings-all-wrap">
            <input type="checkbox" id="pa-settings-voice-enabled">
            <label for="pa-settings-voice-enabled">Enabled</label>
          </div>
          <textarea id="pa-settings-submit-phrases"
            placeholder="One phrase per line&#10;e.g. bravely&#10;e.g. gravely"
          ></textarea>
          <div class="pa-settings-hint">When the message ends with one of these words, it auto-sends after 1s.</div>
          <div class="pa-settings-label" style="margin-top:10px">Clear-input phrase</div>
          <input type="text" id="pa-settings-clear-phrase" placeholder="e.g. change inside in input">
          <div class="pa-settings-hint">If the entire input matches this phrase exactly, the field is cleared.</div>
          <div class="pa-settings-label" style="margin-top:10px">Stop-speech phrase</div>
          <input type="text" id="pa-settings-stop-phrase" placeholder="e.g. spoken pause">
          <div class="pa-settings-hint">Ending the input with this phrase instantly silences current speech.</div>
        </div>
      </div>

      <div class="pa-settings-section">
        <div class="pa-settings-label">Text size <span id="pa-settings-textscale-val"></span></div>
        <input type="range" id="pa-settings-textscale" min="85" max="160" step="5">
        <div class="pa-settings-hint">Scales chat text in the Parlay panel.</div>
      </div>

      <div class="pa-settings-footer">
        <button class="pa-settings-btn" id="pa-settings-cancel">Close</button>
      </div>
    </div>
  `
  document.body.appendChild(overlay)

  overlay.addEventListener('click', (e) => { if (e.target === overlay) closeSettingsModal() })
  document.getElementById('pa-settings-cancel')!.addEventListener('click', closeSettingsModal)
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeSettingsModal() })

  overlay.querySelectorAll('input[type="radio"]').forEach(el => {
    el.addEventListener('change', commitSettings)
  })

  const allChk = document.getElementById('pa-settings-all') as HTMLInputElement
  const projectsTa = document.getElementById('pa-settings-projects') as HTMLTextAreaElement
  allChk.addEventListener('change', () => { projectsTa.disabled = allChk.checked; commitSettings() })
  let debounce: ReturnType<typeof setTimeout> | null = null
  projectsTa.addEventListener('input', () => { clearTimeout(debounce!); debounce = setTimeout(commitSettings, 400) })

  const voiceChk = document.getElementById('pa-settings-voice-enabled') as HTMLInputElement
  const submitPhrasesTa = document.getElementById('pa-settings-submit-phrases') as HTMLTextAreaElement
  const clearPhraseIn = document.getElementById('pa-settings-clear-phrase') as HTMLInputElement
  voiceChk.addEventListener('change', () => {
    submitPhrasesTa.disabled = !voiceChk.checked; clearPhraseIn.disabled = !voiceChk.checked; commitSettings()
  })
  let phraseDebounce: ReturnType<typeof setTimeout> | null = null
  submitPhrasesTa.addEventListener('input', () => { clearTimeout(phraseDebounce!); phraseDebounce = setTimeout(commitSettings, 400) })
  let clearDebounce: ReturnType<typeof setTimeout> | null = null
  clearPhraseIn.addEventListener('input', () => { clearTimeout(clearDebounce!); clearDebounce = setTimeout(commitSettings, 400) })

  const scaleIn = document.getElementById('pa-settings-textscale') as HTMLInputElement
  const scaleVal = document.getElementById('pa-settings-textscale-val')!
  scaleIn.addEventListener('input', () => {
    scaleVal.textContent = `${scaleIn.value}%`
    commitSettings()   // live preview — applySettings is idempotent and cheap
  })
}
