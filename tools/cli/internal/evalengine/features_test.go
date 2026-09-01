package evalengine

// Godog binding for features/eval-engine/profile-matching.feature. Exercises
// platformEligible (platforms.go) directly against a CommandManifest
// (manifest.go) — the eligibility filter eval-engine applies per incoming
// request platform.

import (
	"context"
	"fmt"
	"testing"

	"github.com/cucumber/godog"
)

type profileMatchingFeatureState struct {
	profile   CommandManifest
	eligible  bool
	evaluated bool
}

func (s *profileMatchingFeatureState) reset() {
	s.profile = CommandManifest{}
	s.eligible = false
	s.evaluated = false
}

func (s *profileMatchingFeatureState) aCommandProfileScopedToPlatform(id, platform string) error {
	s.profile = CommandManifest{ID: id, Platforms: []string{platform}}
	return nil
}

func (s *profileMatchingFeatureState) evalEngineChecksEligibilityForPlatform(platform string) error {
	s.eligible = platformEligible(&s.profile, platform)
	s.evaluated = true
	return nil
}

func (s *profileMatchingFeatureState) theProfileIsEligible() error {
	if !s.evaluated {
		return fmt.Errorf("eligibility was never evaluated")
	}
	if !s.eligible {
		return fmt.Errorf("expected profile %q to be eligible", s.profile.ID)
	}
	return nil
}

func (s *profileMatchingFeatureState) theProfileIsNotEligible() error {
	if !s.evaluated {
		return fmt.Errorf("eligibility was never evaluated")
	}
	if s.eligible {
		return fmt.Errorf("expected profile %q to not be eligible", s.profile.ID)
	}
	return nil
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	state := &profileMatchingFeatureState{}

	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		state.reset()
		return c, nil
	})

	ctx.Step(`^a command profile "([^"]*)" scoped to platform "([^"]*)"$`, state.aCommandProfileScopedToPlatform)
	ctx.Step(`^eval-engine checks eligibility for platform "([^"]*)"$`, state.evalEngineChecksEligibilityForPlatform)
	ctx.Step(`^the profile is eligible$`, state.theProfileIsEligible)
	ctx.Step(`^the profile is not eligible$`, state.theProfileIsNotEligible)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../../features/eval-engine/profile-matching.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
