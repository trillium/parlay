package remoteinput

import (
	"reflect"
	"testing"
)

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestGuardrailsDefaultsRefuseEverything(t *testing.T) {
	g, err := GuardrailsFromEnv(envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if g.RunEnabled || g.InjectEnabled {
		t.Fatalf("paths must default off: %+v", g)
	}
	if g.TokenConfigured() || g.TokenValid("") {
		t.Fatal("no token configured must never validate, even an empty one")
	}
	if g.AppAllowed("Keynote") || g.AppAllowed("") {
		t.Fatal("no apps are allowed by default")
	}
	if !reflect.DeepEqual(g.AllowedCommands(), []string{"slide_next", "slide_previous"}) {
		t.Fatalf("default commands = %v", g.AllowedCommands())
	}
}

func TestGuardrailsConfigNarrowsAndRejectsUnknown(t *testing.T) {
	g, err := GuardrailsFromEnv(envOf(map[string]string{
		TalonCommandsEnv: "slide_next", TalonAppsEnv: " Keynote , Slack ",
		TalonRunEnv: "true", TalonInjectEnv: "1", TalonTokenEnv: "s3cret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !g.CommandAllowed("slide_next") || g.CommandAllowed("slide_previous") {
		t.Fatalf("commands = %v", g.AllowedCommands())
	}
	if !g.AppAllowed("keynote") || !g.AppAllowed("Slack") || g.AppAllowed("Keynote Beta") || g.AppAllowed("Terminal") {
		t.Fatal("apps must match exactly, case-insensitively")
	}
	if !g.RunEnabled || !g.InjectEnabled || !g.TokenValid("s3cret") || g.TokenValid("s3cre") {
		t.Fatal("flags/token not applied")
	}

	// A name outside the catalogue cannot be configured in: config selects, never defines.
	if _, err := GuardrailsFromEnv(envOf(map[string]string{TalonCommandsEnv: "slide_next,rm_rf"})); err == nil {
		t.Fatal("unknown command must be a config error")
	}
	// Set-but-empty means none, not default.
	g, _ = GuardrailsFromEnv(envOf(map[string]string{TalonCommandsEnv: ""}))
	if len(g.AllowedCommands()) != 0 {
		t.Fatalf("empty list must allow nothing: %v", g.AllowedCommands())
	}
}

func TestTailnetOrigin(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:5000": true, "[::1]:5000": true,
		"100.64.0.1:1": true, "100.127.255.254:1": true, "[fd7a:115c:a1e0::1]:1": true,
		"100.63.255.255:1": false, "100.128.0.1:1": false,
		"192.168.1.5:1": false, "8.8.8.8:1": false, "[2001:db8::1]:1": false,
		"100.64.0.1": false, "garbage": false, "": false,
	} {
		if got := TailnetOrigin(addr); got != want {
			t.Errorf("TailnetOrigin(%q) = %v, want %v", addr, got, want)
		}
	}
}
