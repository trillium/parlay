export interface ParlaySettings {
  panelSide:          'left' | 'right'
  triggerSide:        'left' | 'right'
  enabledProjects:    'all' | string[]
  voiceEnabled:       boolean
  voiceSubmitPhrases: string[]
  voiceClearPhrases:  string[] // any of these (alone, repeated) clears the input
  voiceStopPhrase:    string   // trailing phrase that hard-stops current speech
  hybridVoice:        boolean  // experimental: local voice speaks block 1 while Kokoro renders
  textScale:          number   // percent; 100 = default
}

export const DEFAULTS: ParlaySettings = {
  panelSide:          'left',
  triggerSide:        'right',
  enabledProjects:    'all',
  voiceEnabled:       true,
  voiceSubmitPhrases: ['bravely', 'gravely', 'briefly', 'lap'],
  voiceClearPhrases:  ['change inside in input'],
  voiceStopPhrase:    'spoken pause',
  hybridVoice:        false,
  textScale:          100,
}
