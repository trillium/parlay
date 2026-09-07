package spawn

// Godog bindings for the features/spawn/ tree:
//
//   - agent-spawn.feature         — subprocessSpawn / subprocessStop /
//     subprocessAlive (subprocess_spawn.go), the same functions the
//     subprocess-spawn / subprocess-stop / subprocess-ping CLI verbs call.
//   - account-resolution.feature  — resolveAccountToken (account.go), the
//     spawn-pipeline account-token resolution that delegates to the canonical
//     juggle account package (tools/cli/internal/juggle/account.go).
//   - spawn-cwd.feature           — the default working directory contract
//     (spawn.go defaultSpawnOptions: no --cwd ⇒ $HOME).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type agentSpawnFeatureState struct {
	stateDir  string
	workdir   string
	agentID   string
	spawnErr  error
	secondErr error
}

func (s *agentSpawnFeatureState) reset() {
	s.stateDir = ""
	s.workdir = ""
	s.agentID = ""
	s.spawnErr = nil
	s.secondErr = nil
}

func (s *agentSpawnFeatureState) cleanup() {
	if s.stateDir != "" {
		_ = subprocessStop(s.stateDir)
	}
	if s.workdir != "" {
		_ = os.RemoveAll(s.workdir)
	}
}

func (s *agentSpawnFeatureState) aSubprocessAgentIsSpawned(agentID string) error {
	s.agentID = agentID
	root, err := os.MkdirTemp("", "parlay-bdd-spawn-")
	if err != nil {
		return err
	}
	s.stateDir = filepath.Join(root, "state")
	s.workdir = root
	s.spawnErr = subprocessSpawn(s.stateDir, agentID, "sleep 30", s.workdir, nil, "", "")
	if s.spawnErr != nil {
		return s.spawnErr
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if subprocessAlive(s.stateDir) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("agent %q did not become alive after spawn", agentID)
}

func (s *agentSpawnFeatureState) subprocessSpawnIsAttemptedAgainForTheSameAgent() error {
	s.secondErr = subprocessSpawn(s.stateDir, s.agentID, "sleep 30", s.workdir, nil, "", "")
	return nil
}

func (s *agentSpawnFeatureState) theSecondSpawnFailsWithAnError(fragment string) error {
	if s.secondErr == nil {
		return fmt.Errorf("expected the second spawn to fail, but it succeeded")
	}
	if !strings.Contains(s.secondErr.Error(), fragment) {
		return fmt.Errorf("expected error to contain %q, got: %v", fragment, s.secondErr)
	}
	return nil
}

func (s *agentSpawnFeatureState) subprocessStopIsRunForTheAgent() error {
	return subprocessStop(s.stateDir)
}

func (s *agentSpawnFeatureState) theAgentSProcessIsNoLongerAlive() error {
	if subprocessAlive(s.stateDir) {
		return fmt.Errorf("expected agent %q to no longer be alive", s.agentID)
	}
	return nil
}

// ── account-resolution.feature ──────────────────────────────────────────────────
//
// resolveAccountToken reads CCJUGGLER_ACCOUNTS_FILE (juggle.AccountFilePath) at
// call time, so each scenario points it at a scratch accounts.json. Every
// scenario here is a resolution FAILURE path: they all return before — or fail
// deterministically at — the macOS keychain call (juggle.GetToken shells to the
// `security` binary, which finds no x- prefixed entry on a real Mac and does not
// exist at all on other platforms), so the suite is green on every platform CI
// runs it on.

type accountResolutionFeatureState struct {
	tmpDir          string
	oldAccountsFile string
	hadAccountsFile bool
	resolveErr      error
}

func (s *accountResolutionFeatureState) reset() {
	s.tmpDir = ""
	s.resolveErr = nil
}

func (s *accountResolutionFeatureState) setup() error {
	dir, err := os.MkdirTemp("", "parlay-bdd-account-")
	if err != nil {
		return err
	}
	s.tmpDir = dir
	s.oldAccountsFile, s.hadAccountsFile = os.LookupEnv("CCJUGGLER_ACCOUNTS_FILE")
	return os.Setenv("CCJUGGLER_ACCOUNTS_FILE", filepath.Join(dir, "accounts.json"))
}

func (s *accountResolutionFeatureState) cleanup() {
	if s.hadAccountsFile {
		_ = os.Setenv("CCJUGGLER_ACCOUNTS_FILE", s.oldAccountsFile)
	} else {
		_ = os.Unsetenv("CCJUGGLER_ACCOUNTS_FILE")
	}
	if s.tmpDir != "" {
		_ = os.RemoveAll(s.tmpDir)
	}
}

func (s *accountResolutionFeatureState) writeAccounts(names ...string) error {
	list := make([]map[string]string, 0, len(names))
	for _, name := range names {
		list = append(list, map[string]string{
			"name":             name,
			"keychain_service": "ccjuggler-" + name,
			"keychain_account": "ccjuggler",
		})
	}
	data, err := json.Marshal(map[string]any{"accounts": list})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.tmpDir, "accounts.json"), data, 0o644)
}

func (s *accountResolutionFeatureState) noCcjugglerAccountsAreConfigured() error {
	return s.writeAccounts()
}

func (s *accountResolutionFeatureState) anAccountExistsInTheAccountsFile(name string) error {
	return s.writeAccounts(name)
}

func (s *accountResolutionFeatureState) ccjugglerResolvesTheTokenForAccount(name string) error {
	_, s.resolveErr = resolveAccountToken(name)
	return nil
}

func (s *accountResolutionFeatureState) resolutionFailsWithAnErrorNamingAccount(name string) error {
	if s.resolveErr == nil {
		return fmt.Errorf("expected resolveAccountToken(%q) to fail, but it succeeded", name)
	}
	if !strings.Contains(s.resolveErr.Error(), name) {
		return fmt.Errorf("expected the resolution error to name account %q, got: %v", name, s.resolveErr)
	}
	return nil
}

// ── spawn-cwd.feature ───────────────────────────────────────────────────────────
//
// defaultSpawnOptions defaults Cwd to os.Getenv("HOME") (spawn.go 131-140). The
// scenario points HOME at a scratch dir and asserts the default tracks it.

type cwdDefaultFeatureState struct {
	home    string
	oldHOME string
	hadHOME bool
	opts    SpawnOptions
}

func (s *cwdDefaultFeatureState) setup() error {
	dir, err := os.MkdirTemp("", "parlay-bdd-cwd-")
	if err != nil {
		return err
	}
	s.home = dir
	s.oldHOME, s.hadHOME = os.LookupEnv("HOME")
	return os.Setenv("HOME", dir)
}

func (s *cwdDefaultFeatureState) cleanup() {
	if s.hadHOME {
		_ = os.Setenv("HOME", s.oldHOME)
	} else {
		_ = os.Unsetenv("HOME")
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
	}
}

func (s *cwdDefaultFeatureState) aFreshUserHomeDirectory() error {
	return s.setup()
}

func (s *cwdDefaultFeatureState) spawnOptionsAreDefaulted() error {
	s.opts = defaultSpawnOptions()
	return nil
}

func (s *cwdDefaultFeatureState) theDefaultWorkingDirectoryIsTheCurrentUsersHome() error {
	if s.opts.Cwd != s.home {
		return fmt.Errorf("default cwd = %q, want the home directory %q", s.opts.Cwd, s.home)
	}
	return nil
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	state := &agentSpawnFeatureState{}

	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		state.reset()
		return c, nil
	})
	ctx.After(func(c context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		state.cleanup()
		return c, nil
	})

	ctx.Step(`^a subprocess agent "([^"]*)" is spawned$`, state.aSubprocessAgentIsSpawned)
	ctx.Step(`^subprocess-spawn is attempted again for the same agent$`, state.subprocessSpawnIsAttemptedAgainForTheSameAgent)
	ctx.Step(`^the second spawn fails with an "([^"]*)" error$`, state.theSecondSpawnFailsWithAnError)
	ctx.Step(`^subprocess-stop is run for the agent$`, state.subprocessStopIsRunForTheAgent)
	ctx.Step(`^the agent's process is no longer alive$`, state.theAgentSProcessIsNoLongerAlive)

	account := &accountResolutionFeatureState{}
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		account.reset()
		return c, account.setup()
	})
	ctx.After(func(c context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		account.cleanup()
		return c, nil
	})
	ctx.Step(`^no ccjuggler accounts are configured$`, account.noCcjugglerAccountsAreConfigured)
	ctx.Step(`^an account "([^"]*)" exists in the accounts file$`, account.anAccountExistsInTheAccountsFile)
	ctx.Step(`^ccjuggler resolves the token for account "([^"]*)"$`, account.ccjugglerResolvesTheTokenForAccount)
	ctx.Step(`^resolution fails with an error naming account "([^"]*)"$`, account.resolutionFailsWithAnErrorNamingAccount)

	cwd := &cwdDefaultFeatureState{}
	ctx.After(func(c context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		cwd.cleanup()
		return c, nil
	})
	ctx.Step(`^a fresh user home directory$`, cwd.aFreshUserHomeDirectory)
	ctx.Step(`^spawn options are defaulted$`, cwd.spawnOptionsAreDefaulted)
	ctx.Step(`^the default working directory is the current user's home directory$`, cwd.theDefaultWorkingDirectoryIsTheCurrentUsersHome)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format: "pretty",
			Paths: []string{
				"../../../../features/spawn/agent-spawn.feature",
				"../../../../features/spawn/account-resolution.feature",
				"../../../../features/spawn/spawn-cwd.feature",
			},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
