package spawn

// Godog binding for features/spawn/agent-spawn.feature. Exercises
// subprocessSpawn / subprocessStop / subprocessAlive directly (see
// subprocess_spawn.go) — the same functions runSubprocessSpawnCommand /
// runSubprocessStopCommand / runSubprocessPingCommand call from the
// subprocess-spawn / subprocess-stop / subprocess-ping CLI verbs.

import (
	"context"
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
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../../features/spawn/agent-spawn.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
