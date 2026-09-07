# Grounded in tools/cli/internal/evalengine/engine.go runPass: a buffer that no
# command matches (exact, tolerant, or captured) fires nothing — matching is
# fail-soft, so ordinary dictation is left untouched.
@REQ-MATCH-002
Feature: unmatched buffers fire no command
  As eval-engine
  I want unknown or near-miss phrases to pass through untouched
  So that ordinary dictation is never swallowed by a misfiring command

  Scenario: Totally unrecognized text fires nothing
    Given a voice-enabled buffer "completely unrelated words"
    When the buffer is evaluated
    Then the response fires no command
    And the response emits no actions

  Scenario: A prefix does not trigger a whole-mode command
    Given a voice-enabled buffer "please switch to marcus"
    When the buffer is evaluated with an agent tab "marcus"
    Then the response fires no command