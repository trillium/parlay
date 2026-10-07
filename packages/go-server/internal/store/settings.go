package store

import (
	"fmt"
	"os"
	"sync"

	"encoding/json"
	"parlay/go-server/internal/atomicfile"
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

// DefaultSettings is served when no settings.json exists yet — i.e. on every
// first run, since nothing writes the file until a client PUTs one. The client
// spreads these over its own DEFAULTS (packages/client/src/settings-modal/io.ts),
// so each value here OVERRIDES the client's default for a fresh install, which
// makes the units load-bearing rather than cosmetic.
//
// textScale is the field that bites: the client divides it by 100
// (`(s.textScale || 100) / 100` in settings-modal/apply.ts) and treats it as a
// percent with 100 = default, and its save path clamps to [85, 160]. Serving 1
// here rendered every reading surface at 0.1px on an install that had never
// opened the settings modal. Keep it at 100 and keep the test in this package
// that ties it to the client's clamp.
//
// Two fields deliberately differ from the client's own DEFAULTS and are not
// drift: PanelSide ("right" here, "left" there) and VoiceEnabled (false here,
// true there) — a server must not open dictation on a fresh install just
// because the web client's fallback does.
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
		TextScale:          100,
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
// legacy singular voiceClearPhrase field into voiceClearPhrases (see the
// settings section of docs/api-contract.md). The client already migrates
// this on load, but only in memory — if a record was ever saved back
// before that client migration existed, or was written some other way, the
// file on disk can still be in the old shape. Doing the fold here too means
// an old-shaped file self-heals the moment this server serves or re-saves
// it, instead of depending on every future reader remembering to migrate.
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
	if err := atomicfile.Write(ss.path, data, 0o644); err != nil {
		return ParlaySettings{}, fmt.Errorf("write %s: %w", ss.path, err)
	}
	ss.settings = s
	return ss.settings, nil
}
