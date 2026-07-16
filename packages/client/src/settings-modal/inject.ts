import { closeSettingsModal, commitSettings } from './lifecycle'
import { sendDebugSnapshot } from './debug'
import { wireSectionPersistence } from './section-persistence'

export function injectSettingsModal() {
  const overlay = document.createElement('div')
  overlay.id = 'pa-settings-overlay'
  overlay.innerHTML = `
    <div id="pa-settings-modal">
      <h2>Parlay Settings</h2>

      <details class="pa-settings-group" data-section="layout" open>
        <summary class="pa-settings-summary">Layout</summary>
        <div class="pa-settings-group-body">
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
            <div class="pa-settings-label">Text size <span id="pa-settings-textscale-val"></span></div>
            <input type="range" id="pa-settings-textscale" min="85" max="160" step="5">
            <div class="pa-settings-hint">Scales chat text in the Parlay panel.</div>
          </div>
        </div>
      </details>

      <details class="pa-settings-group" data-section="advanced">
        <summary class="pa-settings-summary">Advanced <span class="pa-settings-summary-tag">experimental</span></summary>
        <div class="pa-settings-group-body">
          <div class="pa-settings-section">
            <div class="pa-settings-label">Voice settle <span id="pa-settings-settle-val"></span></div>
            <input type="range" id="pa-settings-settle-ms" min="0" max="3000" step="50">
            <div class="pa-settings-hint">Debounce before eval sees your text, tuned to the dictation settle time so the server only ever sees stabilized input.</div>
          </div>
        </div>
      </details>

      <details class="pa-settings-group" data-section="projects">
        <summary class="pa-settings-summary">Projects</summary>
        <div class="pa-settings-group-body">
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
        </div>
      </details>

      <details class="pa-settings-group" data-section="voice">
        <summary class="pa-settings-summary">Voice</summary>
        <div class="pa-settings-group-body">
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
              <div class="pa-settings-label" style="margin-top:10px">Clear-input phrases</div>
              <textarea id="pa-settings-clear-phrases"
                placeholder="One phrase per line&#10;e.g. change inside in input&#10;e.g. clear the box"
              ></textarea>
              <div class="pa-settings-hint">If the input is nothing but one of these phrases (even repeated by dictation), it clears.</div>
              <div class="pa-settings-label" style="margin-top:10px">Stop-speech phrase</div>
              <input type="text" id="pa-settings-stop-phrase" placeholder="e.g. spoken pause">
              <div class="pa-settings-hint">Ending the input with this phrase instantly silences current speech.</div>
              <div class="pa-settings-all-wrap" style="margin-top:10px">
                <input type="checkbox" id="pa-settings-local-only-voice">
                <label for="pa-settings-local-only-voice">Local only (browser TTS, no Kokoro)</label>
              </div>
              <div class="pa-settings-hint">Always use browser speechSynthesis. Instant start, no server contact. Lower quality.</div>
              <div class="pa-settings-all-wrap" style="margin-top:6px">
                <input type="checkbox" id="pa-settings-hybrid-voice">
                <label for="pa-settings-hybrid-voice">Hybrid first-voice (experimental)</label>
              </div>
              <div class="pa-settings-hint">Local voice starts speaking instantly; hands off to Kokoro at the next sentence. Ignored when Local only is on.</div>
            </div>
          </div>
        </div>
      </details>

      <details class="pa-settings-group" data-section="commands">
        <summary class="pa-settings-summary">Voice Commands</summary>
        <div class="pa-settings-group-body">
          <div class="pa-settings-section">
            <div id="pa-settings-commands"></div>
            <div class="pa-settings-hint">Phrases per command, one per line. Submit/clear/stop use the Voice section. Agents and other tools can add commands via window.__parlay.registerCommand.</div>
          </div>
        </div>
      </details>

      <details class="pa-settings-group" data-section="debug">
        <summary class="pa-settings-summary">Debug</summary>
        <div class="pa-settings-group-body">
          <div class="pa-settings-section">
            <div class="pa-settings-hint">Sends a JSON snapshot of current client state to the active channel — for agent troubleshooting of voice, eval, and connection issues.</div>
            <button id="pa-settings-debug-snap" class="pa-settings-btn" style="margin-top:8px;width:100%">Send debug snapshot</button>
          </div>
        </div>
      </details>

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
  const clearPhrasesTa = document.getElementById('pa-settings-clear-phrases') as HTMLTextAreaElement
  voiceChk.addEventListener('change', () => {
    submitPhrasesTa.disabled = !voiceChk.checked; clearPhrasesTa.disabled = !voiceChk.checked; commitSettings()
  })
  let phraseDebounce: ReturnType<typeof setTimeout> | null = null
  submitPhrasesTa.addEventListener('input', () => { clearTimeout(phraseDebounce!); phraseDebounce = setTimeout(commitSettings, 400) })
  let clearDebounce: ReturnType<typeof setTimeout> | null = null
  clearPhrasesTa.addEventListener('input', () => { clearTimeout(clearDebounce!); clearDebounce = setTimeout(commitSettings, 400) })

  const localOnlyChk = document.getElementById('pa-settings-local-only-voice') as HTMLInputElement
  localOnlyChk.addEventListener('change', commitSettings)
  const hybridChk = document.getElementById('pa-settings-hybrid-voice') as HTMLInputElement
  hybridChk.addEventListener('change', commitSettings)

  const scaleIn = document.getElementById('pa-settings-textscale') as HTMLInputElement
  const scaleVal = document.getElementById('pa-settings-textscale-val')!
  scaleIn.addEventListener('input', () => {
    scaleVal.textContent = `${scaleIn.value}%`
    commitSettings()   // live preview — applySettings is idempotent and cheap
  })

  const settleIn = document.getElementById('pa-settings-settle-ms') as HTMLInputElement
  const settleVal = document.getElementById('pa-settings-settle-val')!
  settleIn.addEventListener('input', () => {
    settleVal.textContent = `${settleIn.value}ms`
    commitSettings()
  })
  wireSectionPersistence(overlay)
  document.getElementById('pa-settings-debug-snap')!.addEventListener('click', () => { void sendDebugSnapshot() })
}

