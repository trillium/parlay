# Grounded in tools/cli/internal/evalengine/matcher.go — the compiled phrase→regex
# layer (compilePhrases / phraseCore / modeRegex) behind every command's matching.
# These tolerance properties are proven at the unit level in matcher_test.go; this
# feature pins them as a regression contract at the behavior level.
@REQ-MATCH-001
Feature: matcher hit confidence
  As eval-engine's matching layer
  I want a hit to land only when dictation is close enough to the phrase core
  So that the firing command is always the one the user actually said

  Scenario: An exact phrase always hits
    Given a command phrase "pizza slice" in "whole" mode
    When dictation from the user is "pizza slice"
    Then the phrase matches with "pizza slice" as the matched core

  Scenario: Dictation that drops an interior short word still hits
    Given a command phrase "go to home" in "whole" mode
    When dictation from the user is "go home"
    Then the phrase matches

  Scenario: A required long interior word can never be dropped
    Given a command phrase "open the workspace pane" in "whole" mode
    When dictation from the user is "open the pane"
    Then the phrase does not match

  Scenario: Punctuation between phrase words is tolerated
    Given a command phrase "flag speech" in "whole" mode
    When dictation from the user is "flag, speech"
    Then the phrase matches

  Scenario: The matched core is the phrase, never the surrounding context
    Given a command phrase "spoken pause" in "trailing" mode
    When dictation from the user is "quiet down spoken pause"
    Then the phrase matches with "spoken pause" as the matched core