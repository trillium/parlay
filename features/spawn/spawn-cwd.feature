# Grounded in tools/cli/internal/spawn/spawn.go (defaultSpawnOptions, line 131):
# a spawned agent's working directory defaults to $HOME when --cwd is not given.
# The flag-override path (parseTailFlags) is exercised by the CLI; this feature
# pins the default contract.
@REQ-SPAWN-002
Feature: spawn default working directory
  As parlay's spawn pipeline
  I want a spawned agent to start in a sensible default directory
  So that an agent launched without an explicit --cwd still has a working directory

  Scenario: The default working directory is the current user's home
    Given a fresh user home directory
    When spawn options are defaulted
    Then the default working directory is the current user's home directory