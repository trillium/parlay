// Mirrors packages/cli/src/commands-remote.test.ts's cases. State isolated
// to a tmp PARLAY_STATE_HOME per test so this never touches a real
// ~/.parlay/config.json on the machine running the suite.
package commands

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func TestRemoteBareReportsDefault(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	out := captureStdout(t, func() { Remote(nil) })
	if !strings.Contains(out, "http://localhost:4242") || !strings.Contains(out, "source: default") {
		t.Errorf("Remote(nil) output = %q, want default URL + source", out)
	}
}

func TestRemoteSetPersistsAndReflects(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	out := captureStdout(t, func() { Remote([]string{"set", "http://mini1.tailnet.ts.net:31337"}) })
	if !strings.Contains(out, "persisted default server") {
		t.Errorf("Remote(set) output = %q, want a persisted-server confirmation", out)
	}

	out = captureStdout(t, func() { Remote(nil) })
	if !strings.Contains(out, "http://mini1.tailnet.ts.net:31337") || !strings.Contains(out, "source: config") {
		t.Errorf("Remote(nil) after set = %q, want persisted URL + source: config", out)
	}
}

func TestRemoteSetRejectsInvalidURL(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	code, exited := withExitTrap(t, func() { Remote([]string{"set", "not-a-url"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("Remote(set, invalid url) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

func TestRemoteSetWithNoURLIsUsageError(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	code, exited := withExitTrap(t, func() { Remote([]string{"set"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("Remote(set) with no url exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

func TestRemoteClearFallsBackToDefault(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	captureStdout(t, func() { Remote([]string{"set", "http://macbook:31337"}) })
	out := captureStdout(t, func() { Remote([]string{"clear"}) })
	if !strings.Contains(out, "cleared") {
		t.Errorf("Remote(clear) output = %q, want a cleared confirmation", out)
	}

	out = captureStdout(t, func() { Remote(nil) })
	if !strings.Contains(out, "http://localhost:4242") || !strings.Contains(out, "source: default") {
		t.Errorf("Remote(nil) after clear = %q, want default URL + source", out)
	}
}

func TestRemoteEnvWinsOverPersisted(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	captureStdout(t, func() { Remote([]string{"set", "http://macbook:31337"}) })
	t.Setenv("PARLAY_SERVER", "http://env-wins:9999")

	out := captureStdout(t, func() { Remote(nil) })
	if !strings.Contains(out, "http://env-wins:9999") || !strings.Contains(out, "source: env") {
		t.Errorf("Remote(nil) with env override = %q, want env URL + source: env", out)
	}
}

func TestRemoteUnknownSubcommandIsUsageError(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "")

	code, exited := withExitTrap(t, func() { Remote([]string{"bogus"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("Remote(bogus) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}
