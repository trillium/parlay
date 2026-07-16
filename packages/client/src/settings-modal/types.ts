export interface ParlaySettings {
  panelSide:          'left' | 'right'
  triggerSide:        'left' | 'right'
  enabledProjects:    'all' | string[]
  voiceEnabled:       boolean
  voiceSubmitPhrases: string[]
  voiceClearPhrases:  string[] // any of these (alone, repeated) clears the input
  voiceStopPhrase:    string   // trailing phrase that hard-stops current speech
  commandPhrases:     Record<string, string[]>  // command-id → user phrase overrides (see src/commands/)
  hybridVoice:        boolean  // experimental: local voice speaks block 1 while Kokoro renders
  localOnlyVoice:     boolean  // always use browser speechSynthesis; never contact Kokoro
  textScale:          number   // percent; 100 = default
  voiceSettleMs:      number   // eval up-channel debounce, tuned to the dictation-model settle time so the server only ever sees STABILIZED text (never mid-correction)
}

export const DEFAULTS: ParlaySettings = {
  panelSide:          'left',
  triggerSide:        'right',
  enabledProjects:    'all',
  voiceEnabled:       true,
  voiceSubmitPhrases: ['bravely', 'gravely', 'briefly', 'lap'],
  voiceClearPhrases:  ['change inside input', 'change inside in input'],
  voiceStopPhrase:    'spoken pause',
  commandPhrases:     {},
  hybridVoice:        false,
  localOnlyVoice:     false,
  textScale:          100,
  voiceSettleMs:      450,     // ~450ms: long enough for iOS live dictation correction to settle, short enough to feel responsive
}
