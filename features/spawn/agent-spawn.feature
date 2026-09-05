# Grounded in tools/cli/internal/spawn/subprocess_spawn.go (subprocessSpawn /
# subprocessStop / subprocessAlive) — the detached-process launcher behind
# `parlay subprocess-spawn` / `subprocess-stop` / `subprocess-ping`
# (deprecated aliases: gascity-spawn / gascity-stop / gascity-ping).
@REQ-SPAWN-001
Feature: subprocess agent spawn lifecycle
  As parlay's spawn pipeline
  I want to start, track, and stop a detached agent session
  So that duplicate sessions are rejected and stopped sessions leave no process behind

  Scenario: Spawning the same agent id twice is rejected
    Given a subprocess agent "x-agent-dup" is spawned
    When subprocess-spawn is attempted again for the same agent
    Then the second spawn fails with an "already running" error

  Scenario: Stopping a spawned agent leaves no live process
    Given a subprocess agent "x-agent-lifecycle" is spawned
    When subprocess-stop is run for the agent
    Then the agent's process is no longer alive
