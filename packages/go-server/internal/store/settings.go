package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ParlaySettings matches ParlaySettings in docs/api-contract.md (§Settings).
type ParlaySettings struct {
	PanelSide          string              `json:"panelSide"`
	TriggerSide        string              `json:"triggerSide"`
	EnabledProjects    any                 `json:"enabledProjects"` // 'all' | string[]
	VoiceEnabled       bool                `json:"voiceEnabled"`
	VoiceSubmitPhrases []string            `json:"voiceSubmitPhrases"`
	VoiceClearPhrases  []string            `json:"voiceClearPhrases"`
	VoiceStopPhrase    string              `json:"voiceStopPhrase"`
	CommandPhrases     map[string][]string `json:"commandPhrases"`
	HybridVoice        bool                `json:"hybridVoice"`
	LocalOnlyVoice     bool                `json:"localOnlyVoice"`
	TextScale          float64             `json:"textScale"`
	VoiceSettleMs      int                 `json:"voiceSettleMs"`
	NoKeyboardMode     bool                `json:"noKeyboardMode"`
}

// DefaultSettings is served when no settings.json exists yet. Values mirror
// what a fresh client renders before any settings have ever been saved
// (packages/client/src/settings-modal), so first-run behavior is consistent
// whether the client's own DEFAULTS or this server-side fallback wins.
func DefaultSettings() ParlaySettings {
	return ParlaySettings{
		PanelSide:          "right",
		TriggerSide:        "right",
		EnabledProjects:    "all",
		VoiceEnabled:       false,
		VoiceSubmitPhrases: []string{},
		VoiceClearPhrases:  []string{},
		VoiceStopPhrase:    "",
		CommandPhrases:     map[string][]string{},
		HybridVoice:        false,
		LocalOnlyVoice:     false,
		TextScale:          1,
		VoiceSettleMs:      450,
		NoKeyboardMode:     false,
	}
}

// SettingsStore holds the single whole-document settings record
// (settings.json), atomically rewritten on every PUT.
type SettingsStore struct {
	mu       sync.RWMutex
	path     string
	settings ParlaySettings
}

func openSettingsStore(path string) (*SettingsStore, error) {
	ss := &SettingsStore{path: path, settings: DefaultSettings()}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ss, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	settings, err := decodeSettingsWithMigration(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	ss.settings = settings
	return ss, nil
}

// decodeSettingsWithMigration parses a settings document and folds the
// legacy singular voiceClearPhrase field into voiceClearPhrases (risk #8 in
// docs/scope-go-server.md §5). The client already migrates this on load,
// but only in memory — if a record was ever saved back before that client
// migration existed, or was written some other way, the file on disk can
// still be in the old shape. Doing the fold here too means an old-shaped
// file self-heals the moment this server serves or re-saves it, instead of
// depending on every future reader remembering to migrate.
func decodeSettingsWithMigration(data []byte) (ParlaySettings, error) {
	var s ParlaySettings
	if err := json.Unmarshal(data, &s); err != nil {
		return ParlaySettings{}, err
	}
	if len(s.VoiceClearPhrases) == 0 {
		var legacy struct {
			VoiceClearPhrase string `json:"voiceClearPhrase"`
		}
		// Best-effort: a legacy field alongside an otherwise-valid document
		// should not fail the whole load over this second decode.
		if err := json.Unmarshal(data, &legacy); err == nil && legacy.VoiceClearPhrase != "" {
			s.VoiceClearPhrases = []string{legacy.VoiceClearPhrase}
		}
	}
	return s, nil
}

// Get returns the current settings document.
func (ss *SettingsStore) Get() ParlaySettings {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.settings
}

// Replace performs a whole-document replace — PUT semantics, not a patch,
// matching the documented contract.
func (ss *SettingsStore) Replace(s ParlaySettings) (ParlaySettings, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return ParlaySettings{}, fmt.Errorf("marshal settings: %w", err)
	}
	if err := writeFileAtomic(ss.path, data, 0o644); err != nil {
		return ParlaySettings{}, fmt.Errorf("write %s: %w", ss.path, err)
	}
	ss.settings = s
	return ss.settings, nil
}
