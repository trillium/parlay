package spawn

import (
	"strings"
	"testing"
)

func TestParseResetArgs(t *testing.T) {
	opts, err := parseResetArgs([]string{"--reboot", "--cmd", "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Reboot || opts.Cmd != "echo hi" || opts.Dry {
		t.Errorf("got %+v", opts)
	}
}

func TestParseResetArgsDry(t *testing.T) {
	opts, err := parseResetArgs([]string{"--dry"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Dry || opts.Reboot {
		t.Errorf("got %+v", opts)
	}
}

func TestParseResetArgsUnknown(t *testing.T) {
	if _, err := parseResetArgs([]string{"--bogus"}); err == nil {
		t.Error("expected error for unknown flag")
	}
}

func TestParseResetArgsCmdRequiresValue(t *testing.T) {
	if _, err := parseResetArgs([]string{"--cmd"}); err == nil {
		t.Error("expected error for --cmd with no value")
	}
}

// buildWatcherScript's __TOKEN__ substitution must not leak into the
// watcher's OWN runtime $-expressions (e.g. $(date ...), $_keep, $1) —
// those must survive verbatim for the detached watcher's bash to evaluate.
func TestBuildWatcherScriptSubstitution(t *testing.T) {
	script := buildWatcherScript(12345, "identity --launch 'go-bin-cli'", "/tmp/receipt.log", "go-bin-cli", "http://127.0.0.1:9", true)

	mustContain := []string{
		"watching claude PID 12345 for exit",
		`--argjson pid 12345`,
		`--arg cmd "identity --launch 'go-bin-cli'"`,
		`--arg agent "go-bin-cli"`,
		`>> "/tmp/receipt.log"`,
		`if [ "1" = "1" ]; then`, // REBOOT flag substituted to "1"
		`curl -s -m 3 "http://127.0.0.1:9/api/chat/subscribers"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(script, want) {
			t.Errorf("watcher script missing %q\n--- script ---\n%s", want, script)
		}
	}

	// Runtime-evaluated tokens must survive literally, unexpanded by Go.
	mustSurviveLiteral := []string{
		`$(date '+%H:%M:%S')`,
		`$_keep`,
		`$_verified`,
		`${CLOSED:-0}`,
	}
	for _, want := range mustSurviveLiteral {
		if !strings.Contains(script, want) {
			t.Errorf("watcher script lost runtime token %q\n--- script ---\n%s", want, script)
		}
	}
}

func TestBuildWatcherScriptNoReboot(t *testing.T) {
	script := buildWatcherScript(1, "", "/tmp/r.log", "", "http://x", false)
	if !strings.Contains(script, `if [ "0" = "1" ]; then`) {
		t.Errorf("expected REBOOT=0 branch, got:\n%s", script)
	}
	if !strings.Contains(script, "no --reboot; staying dead. Clean end.") {
		t.Errorf("expected clean-end branch present in script")
	}
}
