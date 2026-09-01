# Grounded in tools/cli/internal/evalengine/platforms.go (platformEligible).
# A command profile (CommandManifest) declares which platforms it is eligible
# on via its "platforms" field; platformEligible is the filter eval-engine
# applies per incoming request platform. Registered platforms today: "parlay"
# (the default) and "herdr" (platforms.go platformRegistry).
@REQ-EVAL-001
Feature: eval-engine command-profile platform eligibility

  Scenario: A profile scoped to a platform matches a request for that platform
    Given a command profile "x-herdr-only" scoped to platform "herdr"
    When eval-engine checks eligibility for platform "herdr"
    Then the profile is eligible

  Scenario: A profile scoped to a platform does not match a request for a different platform
    Given a command profile "x-herdr-only" scoped to platform "herdr"
    When eval-engine checks eligibility for platform "parlay"
    Then the profile is not eligible
