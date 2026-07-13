import { type ParlaySettings } from './types'
import { getSettings, saveSettings } from './io'
import { applySettings } from './apply'

export function openSettingsModal() {
  const overlay = document.getElementById('pa-settings-overlay')!
  const s = getSettings()

  const panelLeft  = document.getElementById('pa-ps-left')  as HTMLInputElement
  const panelRight = document.getElementById('pa-ps-right') as HTMLInputElement
  const trigLeft   = document.getElementById('pa-ts-left')  as HTMLInputElement
  const trigRight  = document.getElementById('pa-ts-right') as HTMLInputElement
  if (s.panelSide === 'right') { panelRight.checked = true } else { panelLeft.checked = true }
  if (s.triggerSide === 'left') { trigLeft.checked = true } else { trigRight.checked = true }

  const allChk = document.getElementById('pa-settings-all') as HTMLInputElement
  const projectsTa = document.getElementById('pa-settings-projects') as HTMLTextAreaElement
  if (s.enabledProjects === 'all') {
    allChk.checked = true; projectsTa.disabled = true; projectsTa.value = ''
  } else {
    allChk.checked = false; projectsTa.disabled = false
    projectsTa.value = (s.enabledProjects as string[]).join('\n')
  }

  const voiceChk = document.getElementById('pa-settings-voice-enabled') as HTMLInputElement
  const submitPhrasesTa = document.getElementById('pa-settings-submit-phrases') as HTMLTextAreaElement
  const clearPhrasesTa = document.getElementById('pa-settings-clear-phrases') as HTMLTextAreaElement
  voiceChk.checked = s.voiceEnabled
  submitPhrasesTa.value = s.voiceSubmitPhrases.join('\n')
  submitPhrasesTa.disabled = !s.voiceEnabled
  clearPhrasesTa.value = (s.voiceClearPhrases ?? []).join('\n')
  clearPhrasesTa.disabled = !s.voiceEnabled
  const stopPhraseIn = document.getElementById('pa-settings-stop-phrase') as HTMLInputElement
  stopPhraseIn.value = s.voiceStopPhrase ?? 'spoken pause'
  stopPhraseIn.disabled = !s.voiceEnabled
  ;(document.getElementById('pa-settings-hybrid-voice') as HTMLInputElement).checked = !!s.hybridVoice

  const scaleIn = document.getElementById('pa-settings-textscale') as HTMLInputElement
  const scaleVal = document.getElementById('pa-settings-textscale-val')!
  scaleIn.value = String(s.textScale || 100)
  scaleVal.textContent = `${scaleIn.value}%`

  overlay.classList.add('open')
}

export function closeSettingsModal() {
  document.getElementById('pa-settings-overlay')?.classList.remove('open')
}

export async function commitSettings() {
  const panelSide   = (document.querySelector('input[name="panelSide"]:checked')   as HTMLInputElement | null)?.value as 'left' | 'right' ?? 'left'
  const triggerSide = (document.querySelector('input[name="triggerSide"]:checked') as HTMLInputElement | null)?.value as 'left' | 'right' ?? 'right'
  const allChk      = document.getElementById('pa-settings-all')           as HTMLInputElement
  const projectsTa  = document.getElementById('pa-settings-projects')      as HTMLTextAreaElement
  const voiceChk    = document.getElementById('pa-settings-voice-enabled') as HTMLInputElement
  const submitTa    = document.getElementById('pa-settings-submit-phrases') as HTMLTextAreaElement
  const clearTa     = document.getElementById('pa-settings-clear-phrases')  as HTMLTextAreaElement

  const enabledProjects: 'all' | string[] = allChk.checked
    ? 'all'
    : projectsTa.value.split('\n').map(l => l.trim()).filter(Boolean)

  const scaleIn = document.getElementById('pa-settings-textscale') as HTMLInputElement | null
  const textScale = Math.min(160, Math.max(85, parseInt(scaleIn?.value ?? '100', 10) || 100))

  const next: ParlaySettings = {
    panelSide, triggerSide, enabledProjects,
    voiceEnabled:       voiceChk.checked,
    voiceSubmitPhrases: submitTa.value.split('\n').map(l => l.trim()).filter(Boolean),
    voiceClearPhrases:  clearTa.value.split('\n').map(l => l.trim()).filter(Boolean),
    voiceStopPhrase:    (document.getElementById('pa-settings-stop-phrase') as HTMLInputElement | null)?.value.trim() ?? 'spoken pause',
    hybridVoice:        (document.getElementById('pa-settings-hybrid-voice') as HTMLInputElement | null)?.checked ?? false,
    textScale,
  }
  applySettings(next)
  await saveSettings(next)
}
