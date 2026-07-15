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
  textScale:          number   // percent; 100 = default
  serverEvalEnabled:  boolean  // experimental (feat/server-side-eval): route input evaluation to the compiled Go engine. OFF = today's local pipeline, untouched.
  voiceSettleMs:      number   // eval up-channel debounce, tuned to the dictation-model settle time so the server only ever sees STABILIZED text (never mid-correction)
}

export const DEFAULTS: ParlaySettings = {
  panelSide:          'left',
  triggerSide:        'right',
  enabledProjects:    'all',
  voiceEnabled:       true,
  voiceSubmitPhrases: ['bravely', 'gravely', 'briefly', 'lap'],
  voiceClearPhrases:  ['change inside in input'],
  voiceStopPhrase:    'spoken pause',
  commandPhrases:     {},
  hybridVoice:        false,
  textScale:          100,
  serverEvalEnabled:  false,   // OFF by default — flip to true to route eval to the Go engine
  voiceSettleMs:      450,     // ~450ms: long enough for iOS live dictation correction to settle, short enough to feel responsive
}
