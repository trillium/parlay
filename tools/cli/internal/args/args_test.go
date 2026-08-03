package args

import (
	"reflect"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func TestParsePositionalsOnly(t *testing.T) {
	r := Parse("test", []string{"foo", "bar"}, nil, nil)
	if !reflect.DeepEqual(r.Positionals, []string{"foo", "bar"}) {
		t.Errorf("Positionals = %v", r.Positionals)
	}
	if len(r.Opts) != 0 {
		t.Errorf("Opts = %v, want empty", r.Opts)
	}
}

func TestParseBooleanFlag(t *testing.T) {
	r := Parse("test", []string{"--full", "extra"}, []string{"--full"}, nil)
	if !r.Bool("--full") {
		t.Error("expected --full to be present")
	}
	if !reflect.DeepEqual(r.Positionals, []string{"extra"}) {
		t.Errorf("Positionals = %v", r.Positionals)
	}
}

func TestParseValueFlag(t *testing.T) {
	r := Parse("test", []string{"--key", "slug-1", "note text"}, nil, []string{"--key"})
	v, ok := r.String("--key")
	if !ok || v != "slug-1" {
		t.Errorf("String(--key) = %q, %v", v, ok)
	}
	if !reflect.DeepEqual(r.Positionals, []string{"note text"}) {
		t.Errorf("Positionals = %v", r.Positionals)
	}
}

func TestParseDoubleDashEndsFlagParsing(t *testing.T) {
	r := Parse("test", []string{"--", "--full", "-x"}, []string{"--full"}, nil)
	if !reflect.DeepEqual(r.Positionals, []string{"--full", "-x"}) {
		t.Errorf("Positionals = %v, want flag-looking tokens preserved after --", r.Positionals)
	}
	if r.Bool("--full") {
		t.Error("--full should not be recognized as a flag after --")
	}
}

func TestParseUnknownFlagDies(t *testing.T) {
	oldExit := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()
	defer func() { httpc.Exit = oldExit }()

	code, ok := testsupport.Capture(func() {
		Parse("send", []string{"--bogus"}, []string{"--full"}, []string{"--key"})
	})
	if !ok {
		t.Fatal("expected an unknown flag to die")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
}

func TestParseValueFlagMissingValueDies(t *testing.T) {
	oldExit := httpc.Exit
	httpc.Exit = testsupport.RecordingExit()
	defer func() { httpc.Exit = oldExit }()

	code, ok := testsupport.Capture(func() {
		Parse("status", []string{"--key"}, nil, []string{"--key"})
	})
	if !ok {
		t.Fatal("expected a trailing value flag with no value to die")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
}
