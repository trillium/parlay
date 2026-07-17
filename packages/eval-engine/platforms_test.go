package main

import "testing"

// evalPlatform runs one eval on a named platform surface.
func evalPlatform(e *Engine, text string, ver int64, tabs []Tab, platform string) EvalResponse {
	return e.Eval(EvalRequest{
		StreamID: "platform-test", Version: ver, Text: text,
		VoiceEnabled: true, Reason: "input", Tabs: tabs, Platform: platform,
	})
}

// TestClearWorksOnBothSurfaces is the headline of the rescope: "change inside input"
// clears the focused input on BOTH Parlay and Herdr, via the same abstract clear
// verb — the effect is the surface's, the command is one piece of data.
func TestClearWorksOnBothSurfaces(t *testing.T) {
	e := NewEngine()
	for _, platform := range []string{"parlay", "herdr", "" /* default = parlay */} {
		r := evalPlatform(e, "change inside input", 1, nil, platform)
		if r.Fired != "clear" || !hasVerb(r, "clear") {
			t.Fatalf("platform %q: expected clear to fire, got %q (%v)", platform, r.Fired, verbs(r))
		}
	}
}

// TestParlayOnlyCommandNotEligibleOnHerdr proves Parlay-scoped commands (the default)
// are invisible on other surfaces: a Herdr buffer saying "channel list" fires nothing.
func TestParlayOnlyCommandNotEligibleOnHerdr(t *testing.T) {
	e := NewEngine()
	tabs := pickerTabs()
	// On Parlay it opens the picker.
	if r := evalPlatform(e, "channel list", 1, tabs, "parlay"); r.Fired != "channel-list" {
		t.Fatalf("channel-list should fire on parlay, got %q", r.Fired)
	}
	// On Herdr it is not eligible → nothing fires.
	if r := evalPlatform(e, "channel list", 2, tabs, "herdr"); r.Fired != "" {
		t.Fatalf("channel-list must NOT fire on herdr, got %q (%v)", r.Fired, verbs(r))
	}
	// switch-tab (Parlay tab op) also must not fire on Herdr.
	if r := evalPlatform(e, "switch to mayor", 3, tabs, "herdr"); r.Fired != "" {
		t.Fatalf("switch-tab must NOT fire on herdr, got %q (%v)", r.Fired, verbs(r))
	}
}

// TestDefaultPlatformIsParlay proves an omitted request platform behaves exactly as
// Parlay — the backward-compatibility guarantee for existing callers.
func TestDefaultPlatformIsParlay(t *testing.T) {
	e := NewEngine()
	tabs := pickerTabs()
	if r := evalPlatform(e, "channel list", 1, tabs, ""); r.Fired != "channel-list" {
		t.Fatalf("default (empty) platform should behave as parlay, got %q", r.Fired)
	}
}

// TestPlatformScopedManifestValidation covers the load-time verb/handler-per-platform
// checks: a Herdr-scoped command may only emit verbs Herdr implements.
func TestPlatformScopedManifestValidation(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{"herdr clear is valid", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["wipe"],"mode":"whole","priority":1,"platforms":["herdr"],
			 "emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, false},

		{"herdr setText is valid", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["type {page}"],"mode":"whole","priority":1,"platforms":["herdr"],
			 "emit":{"kind":"sequence","actions":[{"verb":"setText","args":{"text":"{page}"}}]}}]}`, false},

		{"herdr openChannelPicker is REJECTED", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["list"],"mode":"whole","priority":1,"platforms":["herdr"],
			 "emit":{"kind":"sequence","actions":[{"verb":"openChannelPicker"}]}}]}`, true},

		{"herdr switchTab is REJECTED", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go {agent}"],"mode":"whole","priority":1,"platforms":["herdr"],
			 "emit":{"kind":"sequence","actions":[{"verb":"switchTab","args":{"channel":{"resolve":"agent","from":"{agent}"}}}]}}]}`, true},

		{"multi-platform must satisfy BOTH (navigate not on herdr)", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go {page}"],"mode":"whole","priority":1,"platforms":["parlay","herdr"],
			 "emit":{"kind":"sequence","actions":[{"verb":"navigate","args":{"url":"{page}"}}]}}]}`, true},

		{"herdr submit handler is REJECTED (no handlers on herdr)", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go"],"mode":"trailing","priority":1,"platforms":["herdr"],
			 "emit":{"kind":"handler","handler":"submit"}}]}`, true},

		{"unknown platform is REJECTED", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["wipe"],"mode":"whole","priority":1,"platforms":["atari2600"],
			 "emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"parlay submit handler is valid", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go"],"mode":"trailing","priority":1,"platforms":["parlay"],
			 "emit":{"kind":"handler","handler":"submit","config":{"delayMs":1000}}}]}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseManifest([]byte(c.json))
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", c.wantErr, err)
			}
		})
	}
}

// TestResponseEchoesPlatform proves the engine records the request's surface and
// echoes it on the response — the observable half of "it knows which surface": a
// Herdr request comes back tagged herdr, an untagged request comes back parlay. This
// is the same platform the stream carries onto an async submit fire.
func TestResponseEchoesPlatform(t *testing.T) {
	e := NewEngine()
	if got := evalPlatform(e, "hello there", 1, nil, "herdr").Platform; got != "herdr" {
		t.Fatalf("herdr request should echo platform=herdr, got %q", got)
	}
	if got := evalPlatform(e, "hello there", 2, nil, "").Platform; got != "parlay" {
		t.Fatalf("untagged request should echo the default platform=parlay, got %q", got)
	}
}

// TestHerdrScopedOverrideFiresOnlyOnHerdr wires the whole dimension end to end: a
// per-request override defines a Herdr-only command; it fires on a Herdr request and
// is invisible on a Parlay request.
func TestHerdrScopedOverrideFiresOnlyOnHerdr(t *testing.T) {
	e := NewEngine()
	override := `{"schema":"parlay.commands/v1","version":"h","commands":[
		{"id":"herdr-wipe","phrases":["scrub the field"],"mode":"whole","priority":1,"platforms":["herdr"],
		 "emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`

	rHerdr := e.Eval(EvalRequest{StreamID: "s", Version: 1, Text: "scrub the field",
		VoiceEnabled: true, Platform: "herdr", Commands: []byte(override)})
	if rHerdr.Fired != "herdr-wipe" {
		t.Fatalf("herdr-scoped override should fire on herdr, got %q", rHerdr.Fired)
	}

	rParlay := e.Eval(EvalRequest{StreamID: "s2", Version: 1, Text: "scrub the field",
		VoiceEnabled: true, Platform: "parlay", Commands: []byte(override)})
	if rParlay.Fired != "" {
		t.Fatalf("herdr-scoped override must NOT fire on parlay, got %q", rParlay.Fired)
	}
}
