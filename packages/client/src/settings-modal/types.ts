export interface ParlaySettings {
  panelSide:          'left' | 'right'
  triggerSide:        'left' | 'right'
  enabledProjects:    'all' | string[]
  voiceEnabled:       boolean
  voiceSubmitPhrases: string[]
  voiceClearPhrase:   string
  textScale:          number   // percent; 100 = default
}

export const DEFAULTS: ParlaySettings = {
  panelSide:          'left',
  triggerSide:        'right',
  enabledProjects:    'all',
  voiceEnabled:       true,
  voiceSubmitPhrases: ['bravely', 'gravely', 'briefly', 'lap'],
  voiceClearPhrase:   'change inside in input',
  textScale:          100,
}
