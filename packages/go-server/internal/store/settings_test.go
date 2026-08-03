package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsStoreDefaultsWhenNoFile(t *testing.T) {
	ss, err := openSettingsStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("openSettingsStore: %v", err)
	}
	got := ss.Get()
	want := DefaultSettings()
	if got.PanelSide != want.PanelSide || got.VoiceSettleMs != want.VoiceSettleMs {
		t.Errorf("Get() = %+v, want defaults %+v", got, want)
	}
}

func TestSettingsStoreReplaceAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	ss1, err := openSettingsStore(path)
	if err != nil {
		t.Fatalf("openSettingsStore: %v", err)
	}
	s := DefaultSettings()
	s.PanelSide = "left"
	s.VoiceEnabled = true
	if _, err := ss1.Replace(s); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	ss2, err := openSettingsStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := ss2.Get()
	if got.PanelSide != "left" || !got.VoiceEnabled {
		t.Errorf("after reopen Get() = %+v, want PanelSide=left VoiceEnabled=true", got)
	}
}

func TestSettingsStoreMigratesLegacyVoiceClearPhrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacyDoc := `{
		"panelSide": "right",
		"triggerSide": "right",
		"enabledProjects": "all",
		"voiceEnabled": true,
		"voiceSubmitPhrases": ["go"],
		"voiceClearPhrase": "clear that",
		"voiceStopPhrase": "stop",
		"commandPhrases": {},
		"hybridVoice": false,
		"localOnlyVoice": false,
		"textScale": 1,
		"voiceSettleMs": 450,
		"noKeyboardMode": false
	}`
	if err := os.WriteFile(path, []byte(legacyDoc), 0o644); err != nil {
		t.Fatalf("seed legacy settings file: %v", err)
	}

	ss, err := openSettingsStore(path)
	if err != nil {
		t.Fatalf("openSettingsStore: %v", err)
	}
	got := ss.Get()
	if len(got.VoiceClearPhrases) != 1 || got.VoiceClearPhrases[0] != "clear that" {
		t.Errorf("VoiceClearPhrases = %v, want migrated [\"clear that\"] from legacy voiceClearPhrase", got.VoiceClearPhrases)
	}
}

func TestSettingsStorePrefersNewFieldOverLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	doc := `{"voiceClearPhrases": ["new one"], "voiceClearPhrase": "old one"}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("seed settings file: %v", err)
	}

	ss, err := openSettingsStore(path)
	if err != nil {
		t.Fatalf("openSettingsStore: %v", err)
	}
	got := ss.Get().VoiceClearPhrases
	if len(got) != 1 || got[0] != "new one" {
		t.Errorf("VoiceClearPhrases = %v, want [\"new one\"] (new field wins over legacy)", got)
	}
}
